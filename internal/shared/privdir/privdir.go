// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

// Package privdir owns "where may protolift put a unix socket". Socket paths
// are limited (104 bytes on macOS, 108 on Linux), so a socket cannot always
// live under a long home folder. This package is the one place that decides,
// for every adapter that opens sockets (ssh sharing, the sync daemon).
package privdir

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SocketMax is the socket path limit used everywhere (the smaller, macOS).
const SocketMax = 104

// Fits reports whether a socket at path is short enough.
func Fits(path string) bool { return len(path) < SocketMax }

// Short returns a short, owner-only folder for sockets, /tmp/protolift-<uid>
// plus sub. ok=false when the folder is not safely ours (made by someone
// else, or open to others): callers then turn the feature off rather than
// share sockets in a place another user controls.
func Short(sub string) (string, bool) {
	base := fmt.Sprintf("/tmp/protolift-%d", os.Getuid())
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", false
	}
	st, err := os.Lstat(base)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		return "", false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Getuid() {
		return "", false
	}
	dir := filepath.Join(base, sub)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	return dir, true
}
