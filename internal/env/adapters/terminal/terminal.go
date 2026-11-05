// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package terminal implements ports.UI for a person at a terminal.
package terminal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Prabhudas111/protolift/internal/env/ports"
)

// UI writes progress to Out and reads answers from In.
type UI struct {
	In      io.Reader
	OutW    io.Writer
	Yes     bool // --yes: answer every question with yes
	Quiet   bool // --quiet: only warnings and errors
	TTY     bool // In is an interactive terminal
	mu      sync.Mutex
	scanner *bufio.Scanner
}

var _ ports.UI = (*UI)(nil)

// New wires stdin/stdout and detects whether a person can answer.
func New(yes, quiet bool) *UI {
	st, err := os.Stdin.Stat()
	tty := err == nil && st.Mode()&os.ModeCharDevice != 0
	return &UI{In: os.Stdin, OutW: os.Stdout, Yes: yes, Quiet: quiet, TTY: tty}
}

func (u *UI) line(prefix, msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintln(u.OutW, prefix+msg)
}

// Step marks a stage of work.
func (u *UI) Step(msg string) {
	if !u.Quiet {
		u.line("→ ", msg)
	}
}

// Info is detail under a step.
func (u *UI) Info(msg string) {
	if !u.Quiet {
		u.line("  ", msg)
	}
}

// Warn is something the user should know but that does not stop the work.
func (u *UI) Warn(msg string) { u.line("! ", msg) }

// Confirm asks a yes/no question. Without a terminal it refuses rather
// than guess, and says how to answer from a script.
func (u *UI) Confirm(question string) bool {
	if u.Yes {
		u.line("? ", question+" yes (--yes)")
		return true
	}
	if !u.TTY {
		u.line("! ", question+" no terminal to answer; run with --yes to agree from a script")
		return false
	}
	u.mu.Lock()
	fmt.Fprint(u.OutW, "? "+question+" [y/N] ")
	if u.scanner == nil {
		u.scanner = bufio.NewScanner(u.In)
	}
	ok := u.scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(u.scanner.Text()))
	u.mu.Unlock()
	return ok && (answer == "y" || answer == "yes")
}

// Out is where plain output (status tables) goes.
func (u *UI) Out() io.Writer { return u.OutW }
