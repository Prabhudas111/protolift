// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

var fast = Policy{Attempts: 4, Base: time.Millisecond, Max: 2 * time.Millisecond}

func TestDoStopsOnSuccessAndPermanent(t *testing.T) {
	n := 0
	err := Do(context.Background(), fast, func(context.Context) (bool, error) {
		n++
		if n < 3 {
			return true, errors.New("flaky")
		}
		return false, nil
	})
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n = 0
	perm := errors.New("permanent")
	if err := Do(context.Background(), fast, func(context.Context) (bool, error) { n++; return false, perm }); !errors.Is(err, perm) || n != 1 {
		t.Fatalf("permanent error must not retry: n=%d err=%v", n, err)
	}
}

func TestDoGivesUpAndHonoursCancel(t *testing.T) {
	n := 0
	if err := Do(context.Background(), fast, func(context.Context) (bool, error) { n++; return true, errors.New("x") }); err == nil || n != 4 {
		t.Fatalf("must stop after 4 attempts: n=%d err=%v", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := Policy{Attempts: 5, Base: time.Hour, Max: time.Hour}
	if err := Do(ctx, slow, func(context.Context) (bool, error) { return true, errors.New("x") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel must end the wait, got %v", err)
	}
}

func TestBackoffCaps(t *testing.T) {
	if Backoff(0, time.Second, 8*time.Second) != time.Second || Backoff(10, time.Second, 8*time.Second) != 8*time.Second {
		t.Fatal("backoff must start at base and cap at max")
	}
}
