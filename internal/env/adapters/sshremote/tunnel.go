// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package sshremote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
	"github.com/Prabhudas111/protolift/internal/shared/retry"
)

// Tunnel limits.
const (
	connectWait   = 90 * time.Second // ssh over SSM can take a while
	probeEvery    = 300 * time.Millisecond
	backoffBase   = 2 * time.Second
	backoffMax    = 30 * time.Second
	stableAfter   = time.Minute // a connection this old resets the backoff
	probeDialWait = 500 * time.Millisecond
)

// CheckFree fails with PORT_BUSY on the first laptop port in use.
func (r *Remote) CheckFree(ps []int) error {
	for _, p := range ps {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			how := "`lsof -i :" + strconv.Itoa(p) + "` shows what uses it"
			if runtime.GOOS == "windows" {
				how = "`netstat -ano | findstr :" + strconv.Itoa(p) + "` shows what uses it"
			}
			return errs.Wrap(errs.CodePortBusy, err, fmt.Sprintf("port %d is already in use on your laptop", p),
				"stop the app using it and run `protolift up` again. "+how)
		}
		if err := l.Close(); err != nil {
			return errs.Wrap(errs.CodeInternal, err, "cannot release a test port", "")
		}
	}
	return nil
}

// Mirror forwards each port to the same port on the box and reconnects
// with capped backoff until ctx ends. It reports state on events.
func (r *Remote) Mirror(ctx context.Context, b domain.Box, ps []int, events chan<- ports.TunnelEvent) error {
	if len(ps) == 0 {
		<-ctx.Done()
		return nil
	}
	if err := r.ensureConfig(b); err != nil {
		return err
	}
	// The mirror owns its own connection. Through a shared one, ssh would
	// hand the forwards to the shared master and exit at once, and the
	// ports would vanish when that master expires.
	args := []string{"-N", "-o", "ExitOnForwardFailure=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none"}
	for _, p := range ps {
		s := strconv.Itoa(p)
		args = append(args, "-L", s+":127.0.0.1:"+s)
	}
	fail := 0
	for ctx.Err() == nil {
		started := time.Now()
		err := r.runForward(ctx, b, args, ps[0], events)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > stableAfter {
			fail = 0
		}
		fail++
		send(events, ports.TunnelEvent{Attempt: fail, Err: err})
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(retry.Backoff(fail-1, backoffBase, backoffMax)):
		}
	}
	return nil
}

// runForward runs one ssh forward until it exits. It announces Connected
// once the first local port accepts, i.e. ssh is past authentication.
func (r *Remote) runForward(ctx context.Context, b domain.Box, args []string, probe int, events chan<- ports.TunnelEvent) error {
	cmd := exec.CommandContext(ctx, r.o.SSH, r.base(b, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return errs.Wrap(errs.CodeToolMissing, err, "cannot start ssh", "")
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(connectWait)
	tick := time.NewTicker(probeEvery)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			return tunnelClosed(err, stderr.String())
		case <-deadline:
			// Still trying. Ssh's own ConnectTimeout will end it if stuck.
		case <-tick.C:
			c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(probe), probeDialWait)
			if err != nil {
				continue
			}
			if err := c.Close(); err != nil {
				continue
			}
			send(events, ports.TunnelEvent{Connected: true})
			return tunnelClosed(<-exited, stderr.String())
		}
	}
}

// tunnelClosed describes why the port tunnel ended, with ssh's own words.
func tunnelClosed(err error, stderr string) error {
	tail := strings.TrimSpace(stderr)
	if len(tail) > stderrTail {
		tail = "…" + tail[len(tail)-stderrTail:]
	}
	msg := "the port tunnel closed"
	if tail != "" {
		msg += ": " + tail
	}
	if err == nil {
		err = errors.New("ssh exited")
	}
	return errs.Wrap(errs.CodeNetwork, err, msg, "protolift reconnects by itself. Check your internet if it keeps happening")
}

// send never blocks the tunnel on a slow reader.
func send(ch chan<- ports.TunnelEvent, e ports.TunnelEvent) {
	select {
	case ch <- e:
	default:
	}
}
