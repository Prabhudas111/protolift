// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package env is the environment context: one project folder, its cloud
// box, its sync and its ports. Manager is the only thing the command line
// depends on; the layers below (domain → application → ports → adapters)
// stay hidden behind it.
package env

import (
	"context"

	"github.com/Prabhudas111/protolift/internal/env/application"
	"github.com/Prabhudas111/protolift/internal/env/domain"
)

// Re-exported request types, so callers never import the layers.
type (
	Target       = application.Target
	UpRequest    = application.UpRequest
	StatusReport = application.StatusReport
	Deps         = application.Deps
	Box          = domain.Box
	Arch         = domain.Arch
)

// Chip families, for flags.
const (
	ArchARM   = domain.ArchARM
	ArchIntel = domain.ArchIntel
)

// Manager is the facade over the use cases.
type Manager struct{ svc *application.Service }

// NewManager wires the use cases to their ports.
func NewManager(d Deps) (*Manager, error) {
	s, err := application.New(d)
	if err != nil {
		return nil, err
	}
	return &Manager{svc: s}, nil
}

// Init sets up the AWS account and region once.
func (m *Manager) Init(ctx context.Context) error { return m.svc.Init(ctx) }

// Up creates or wakes the box, syncs, starts and mirrors ports.
func (m *Manager) Up(ctx context.Context, r UpRequest) error { return m.svc.Up(ctx, r) }

// Status reports the project's box, cost and sync.
func (m *Manager) Status(ctx context.Context, t Target) (StatusReport, error) {
	return m.svc.Status(ctx, t)
}

// List shows every protolift box in the region.
func (m *Manager) List(ctx context.Context) ([]Box, error) { return m.svc.List(ctx) }

// Stop stops the box and keeps its disk.
func (m *Manager) Stop(ctx context.Context, t Target) error { return m.svc.Stop(ctx, t) }

// Resize changes the box size.
func (m *Manager) Resize(ctx context.Context, t Target, size string) error {
	return m.svc.Resize(ctx, t, size)
}

// Down deletes the box and everything it made.
func (m *Manager) Down(ctx context.Context, t Target) error { return m.svc.Down(ctx, t) }

// Uninstall removes the one-time account pieces.
func (m *Manager) Uninstall(ctx context.Context) error { return m.svc.Uninstall(ctx) }

// Config returns the settings file path, creating it if missing.
func (m *Manager) Config(ctx context.Context, t Target) (string, error) { return m.svc.Config(ctx, t) }

// SSH opens a terminal on the box.
func (m *Manager) SSH(ctx context.Context, t Target) error { return m.svc.SSH(ctx, t) }

// Logs follows logs on the box.
func (m *Manager) Logs(ctx context.Context, t Target, service string) error {
	return m.svc.Logs(ctx, t, service)
}
