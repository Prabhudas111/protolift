// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package errs

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestCodeOfThroughWrapping(t *testing.T) {
	base := New(CodePortBusy, "port 3000 is in use", "stop it")
	wrapped := fmt.Errorf("up: %w", base)
	if CodeOf(wrapped) != CodePortBusy || ExitCode(wrapped) != 6 {
		t.Fatalf("code=%s exit=%d", CodeOf(wrapped), ExitCode(wrapped))
	}
	if CodeOf(fmt.Errorf("x: %w", context.Canceled)) != CodeCancelled || ExitCode(context.Canceled) != 130 {
		t.Fatal("cancel must map to CANCELLED / 130")
	}
	if CodeOf(context.DeadlineExceeded) != CodeTimeout {
		t.Fatal("deadline must map to TIMEOUT")
	}
	if CodeOf(errors.New("boom")) != CodeInternal || ExitCode(nil) != 0 {
		t.Fatal("plain error must be INTERNAL, nil must exit 0")
	}
}

func TestAsAlwaysHasAMessage(t *testing.T) {
	e := As(errors.New("boom"))
	if e.Code != CodeInternal || e.Message == "" || e.Fix == "" {
		t.Fatalf("As = %+v", e)
	}
	cause := errors.New("dial tcp: timeout")
	w := Wrap(CodeNetwork, cause, "could not reach AWS", "check internet")
	if !errors.Is(w, cause) {
		t.Fatal("Wrap must keep the cause for errors.Is")
	}
}
