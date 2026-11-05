// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package application holds the use cases: init, up, status, stop, resize,
// down, uninstall, config, ssh and logs. Each use case owns the order of
// steps and the policy. The ports do the work. No use case imports a
// provider SDK.
package application

import (
	"context"
	"fmt"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Time limits for each kind of step. A step that passes its limit fails
// with TIMEOUT and says which step it was.
const (
	TimeoutQuick   = 30 * time.Second // one command on the box
	TimeoutBoot    = 15 * time.Minute // first boot installs packages
	TimeoutSetup   = 30 * time.Minute // npm ci / go mod download
	TimeoutStart   = 30 * time.Minute // compose builds images
	TimeoutPorts   = 3 * time.Minute  // services begin to listen
	TimeoutInspect = 20 * time.Second // registry checks for chip choice
)

// Deps are the ports a Service needs. Every field is required.
type Deps struct {
	Cloud   ports.Cloud
	Remote  ports.Remote
	Tunnel  ports.Tunnel
	Sync    ports.Sync
	Store   ports.Store
	Images  ports.ImageInspector
	Scripts ports.Scripts
	UI      ports.UI
	Clock   ports.Clock
	Profile string // AWS profile, remembered per env
	Project string // Google Cloud project, remembered per env
}

// Service runs the use cases.
type Service struct{ d Deps }

// New checks every dependency is wired, so a wiring mistake fails at start
// and not halfway through creating a box.
func New(d Deps) (*Service, error) {
	missing := []string{}
	for name, ok := range map[string]bool{
		"Cloud": d.Cloud != nil, "Remote": d.Remote != nil, "Tunnel": d.Tunnel != nil, "Sync": d.Sync != nil,
		"Store": d.Store != nil, "Images": d.Images != nil, "Scripts": d.Scripts != nil, "UI": d.UI != nil, "Clock": d.Clock != nil,
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, errs.New(errs.CodeInternal, fmt.Sprintf("protolift is wired without %v", missing), "this is a bug. Please report it")
	}
	return &Service{d: d}, nil
}

// Target names the project a command acts on.
type Target struct {
	Dir  string
	Name string // optional, for several envs of one folder
}

// loadProject loads the project and its box, if any.
func (s *Service) loadProject(ctx context.Context, t Target) (domain.Project, domain.Box, bool, error) {
	p, err := s.d.Store.LoadProject(ctx, t.Dir, t.Name)
	if err != nil {
		return domain.Project{}, domain.Box{}, false, err
	}
	b, ok, err := s.d.Cloud.FindBox(ctx, p.ID)
	if err != nil {
		return p, domain.Box{}, false, err
	}
	return p, b, ok, nil
}

func (s *Service) requireBox(ctx context.Context, t Target) (domain.Project, domain.Box, error) {
	p, b, ok, err := s.loadProject(ctx, t)
	if err != nil {
		return p, b, err
	}
	if !ok {
		return p, b, errs.New(errs.CodeNotFound, fmt.Sprintf("%s has no box in %s", p.Name, s.d.Cloud.Region()),
			"run `protolift up` to create one")
	}
	return p, b, nil
}

func (s *Service) requireRunning(ctx context.Context, t Target) (domain.Project, domain.Box, error) {
	p, b, err := s.requireBox(ctx, t)
	if err != nil {
		return p, b, err
	}
	if b.State != domain.BoxRunning {
		return p, b, errs.New(errs.CodeConflict, fmt.Sprintf("the box is %s", b.State), "run `protolift up` first")
	}
	return p, b, nil
}

// step runs fn under its own time limit and names the step on timeout.
func step(ctx context.Context, limit time.Duration, name string, fn func(ctx context.Context) error) error {
	c, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	err := fn(c)
	if err != nil && errs.CodeOf(err) == errs.CodeTimeout && ctx.Err() == nil {
		return errs.Wrap(errs.CodeTimeout, err, fmt.Sprintf("%s took longer than %s", name, limit),
			"check your internet connection. Run `protolift logs` or `protolift ssh` to look at the box")
	}
	return err
}
