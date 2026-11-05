// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package retry runs an operation again with capped exponential backoff.
// It is the only retry loop in protolift, so every retry has a limit.
package retry

import (
	"context"
	"math/rand/v2"
	"time"
)

// Policy bounds a retry loop by attempts and by total time.
type Policy struct {
	Attempts int           // total tries, including the first
	Base     time.Duration // first wait
	Max      time.Duration // longest single wait
}

// Default suits network calls that fail for a few seconds at a time.
var Default = Policy{Attempts: 5, Base: 500 * time.Millisecond, Max: 8 * time.Second}

// Do calls fn until it succeeds, fn reports the error is permanent, the
// attempts run out, or ctx ends. It returns the last error.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context) (retryable bool, err error)) error {
	var err error
	wait := p.Base
	for i := 0; i < p.Attempts; i++ {
		var retryable bool
		retryable, err = fn(ctx)
		if err == nil || !retryable || i == p.Attempts-1 {
			return err
		}
		// Full jitter keeps many clients from retrying in lockstep.
		d := time.Duration(rand.Int64N(int64(wait) + 1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
		wait *= 2
		if wait > p.Max {
			wait = p.Max
		}
	}
	return err
}

// Backoff returns the wait before reconnect number n (0-based), capped.
// Used by long-running loops (the port tunnel) that retry forever.
func Backoff(n int, base, max time.Duration) time.Duration {
	d := base
	for i := 0; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}
