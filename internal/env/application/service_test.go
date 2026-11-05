// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func TestNewRefusesMissingPorts(t *testing.T) {
	if _, err := New(Deps{}); errCode(err) != errs.CodeInternal {
		t.Fatalf("want INTERNAL for missing ports, got %v", err)
	}
}

func TestUpCreatesSyncsStartsAndMirrors(t *testing.T) {
	w := newWorld(true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		for !w.calls.has("mirror") {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	if err := w.svc.Up(ctx, UpRequest{Target: Target{Dir: "."}}); err != nil {
		t.Fatal(err)
	}
	// postgres (1) + 1 built service (1.5) + system (2) = 4.5 GB → S, on ARM.
	for _, want := range []string{"create:m7g.large", "sync", "run:SETUP /work/app", "run:START /work/app", "mirror"} {
		if !w.calls.has(want) {
			t.Fatalf("missing step %q in %v", want, w.calls.calls)
		}
	}
	if got := w.remote.put["/work/app/.env"]; got != "0600:SECRET=1" {
		t.Fatalf(".env must be copied owner-only, got %q", got)
	}
	if strings.Contains(w.output(), "SECRET=1") {
		t.Fatal(".env contents must never be printed")
	}
	if w.store.rec == nil || len(w.store.rec.Ports) != 1 || w.store.rec.Ports[0] != 3000 {
		t.Fatalf("ports not saved: %+v", w.store.rec)
	}
	if len(w.ui.asked) != 1 || !strings.Contains(w.ui.asked[0], "about $") {
		t.Fatalf("a new box must be confirmed with its price, asked %v", w.ui.asked)
	}
}

func TestUpNothingBilledWithoutYes(t *testing.T) {
	w := newWorld(false)
	err := w.svc.Up(context.Background(), UpRequest{Target: Target{Dir: "."}, Detached: true})
	if errCode(err) != errs.CodeCancelled {
		t.Fatalf("want CANCELLED, got %v", err)
	}
	for _, c := range w.calls.calls {
		if strings.HasPrefix(c, "create") {
			t.Fatal("a box was created without a yes")
		}
	}
}

func TestUpNeedsInit(t *testing.T) {
	w := newWorld(true)
	w.cloud.ready = errs.New(errs.CodeNotSetUp, "not set up", "run init")
	if err := w.svc.Up(context.Background(), UpRequest{Detached: true}); errCode(err) != errs.CodeNotSetUp {
		t.Fatalf("want NOT_SET_UP, got %v", err)
	}
}

func TestUpStopsOnBusyPort(t *testing.T) {
	w := newWorld(true)
	w.tunnel.busy = errs.New(errs.CodePortBusy, "port 3000 is in use", "")
	if err := w.svc.Up(context.Background(), UpRequest{Detached: true}); errCode(err) != errs.CodePortBusy {
		t.Fatalf("want PORT_BUSY, got %v", err)
	}
}

func TestUpWakesStoppedBoxAndSkipsUnchangedSetup(t *testing.T) {
	w := newWorld(true)
	w.cloud.box = &domain.Box{ID: "i-0123456789abcdef0", EnvID: "abc1234567", State: domain.BoxStopped, InstanceType: "m7g.xlarge", Arch: domain.ArchARM}
	w.store.SaveEnv(portsRecord("h1"))
	if err := w.svc.Up(context.Background(), UpRequest{Detached: true}); err != nil {
		t.Fatal(err)
	}
	if !w.calls.has("start") || w.calls.has("run:SETUP /work/app") {
		t.Fatalf("want start and no setup rerun, got %v", w.calls.calls)
	}
	if len(w.ui.asked) != 0 {
		t.Fatalf("an existing box needs no confirmation, asked %v", w.ui.asked)
	}
}

func TestDownRemovesEverything(t *testing.T) {
	w := newWorld(true)
	w.cloud.box = &domain.Box{ID: "i-0123456789abcdef0", EnvID: "abc1234567", Name: "protolift-app", State: domain.BoxRunning}
	w.store.SaveEnv(portsRecord(""))
	if err := w.svc.Down(context.Background(), Target{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"unsync", "delete", "forget"} {
		if !w.calls.has(want) {
			t.Fatalf("down missing %q: %v", want, w.calls.calls)
		}
	}
	if w.store.rec != nil {
		t.Fatal("local state must be removed")
	}
}

func TestDownAsksFirst(t *testing.T) {
	w := newWorld(false)
	w.cloud.box = &domain.Box{ID: "i-0123456789abcdef0", Name: "protolift-app", State: domain.BoxRunning}
	if err := w.svc.Down(context.Background(), Target{}); errCode(err) != errs.CodeCancelled || w.calls.has("delete") {
		t.Fatalf("down without yes must delete nothing: %v %v", err, w.calls.calls)
	}
}

func TestStatusAdvisesSmallerSize(t *testing.T) {
	w := newWorld(true)
	w.cloud.box = &domain.Box{ID: "i-0123456789abcdef0", State: domain.BoxRunning, InstanceType: "m7g.2xlarge", Arch: domain.ArchARM,
		Region: "us-east-1", StartedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	r, err := w.svc.Status(context.Background(), Target{})
	if err != nil {
		t.Fatal(err)
	}
	// 3.1 GB peak × 1.3 → fits S; the box is L.
	if !strings.Contains(r.Advice, "size S") || !strings.Contains(r.CostSoFar, "$0.65") {
		t.Fatalf("status = %+v", r)
	}
}

func TestResizeNeedsKnownSize(t *testing.T) {
	w := newWorld(true)
	if err := w.svc.Resize(context.Background(), Target{}, "HUGE"); errCode(err) != errs.CodeInvalidInput {
		t.Fatalf("want INVALID_INPUT, got %v", err)
	}
}

func TestStepNamesTheSlowStep(t *testing.T) {
	err := step(context.Background(), 10*time.Millisecond, "starting web", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	e := errs.As(err)
	if e.Code != errs.CodeTimeout || !strings.Contains(e.Message, "starting web") {
		t.Fatalf("want TIMEOUT naming the step, got %v", err)
	}
}
