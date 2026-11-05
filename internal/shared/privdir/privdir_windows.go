// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

//go:build windows

// Package privdir: on Windows protolift opens no unix sockets of its own
// (ssh has no connection sharing. Mutagen uses a named pipe), so any path fits.
package privdir

// SocketMax matches the unix build for callers that compare lengths.
const SocketMax = 104

// Fits is always true on Windows.
func Fits(string) bool { return true }

// Short is unused on Windows: there are no sockets to place.
func Short(string) (string, bool) { return "", false }
