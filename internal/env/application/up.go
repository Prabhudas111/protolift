// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// UpRequest is `protolift up`.
type UpRequest struct {
	Target
	Size     string      // overrides settings for a new box
	Arch     domain.Arch // overrides detection for a new box
	NoEnv    bool        // do not copy .env files
	Detached bool        // set everything up, do not mirror ports
}

// Up creates or wakes the box, syncs, starts the stack and mirrors ports.
// It returns when ctx ends (Ctrl-C) or, with Detached, once ready.
func (s *Service) Up(ctx context.Context, r UpRequest) error {
	p, err := s.d.Store.LoadProject(ctx, r.Dir, r.Name)
	if err != nil {
		return err
	}
	if err := domain.ValidateFolders(p.Folders); err != nil {
		return err
	}
	if err := s.d.Cloud.Ready(ctx); err != nil {
		return err
	}
	rec, _, err := s.d.Store.LoadEnv(p.ID)
	if err != nil {
		return err
	}
	rec.ID, rec.Name, rec.Region, rec.Profile = p.ID, p.Name, s.d.Cloud.Region(), s.d.Profile

	b, err := s.ensureBox(ctx, p, r, &rec)
	if err != nil {
		return err
	}
	if err := s.waitBooted(ctx, b); err != nil {
		return err
	}
	if err := s.d.Remote.PutFile(ctx, b, s.d.Scripts.IdleConfigPath(), s.d.Scripts.IdleConfig(p.Settings), "0644"); err != nil {
		return err
	}
	s.d.UI.Step("syncing your files (the first time copies everything)")
	if err := s.d.Sync.Ensure(ctx, b, p, s.d.UI.Info); err != nil {
		return err
	}
	if !r.NoEnv {
		if err := s.pushEnvFiles(ctx, b, p, &rec, true); err != nil {
			return err
		}
	}
	ps, err := s.startFolders(ctx, b, p, &rec)
	if err != nil {
		return err
	}
	if err := s.d.Store.SaveEnv(rec); err != nil {
		return err
	}
	if err := s.d.Tunnel.CheckFree(rec.Ports); err != nil {
		return err
	}
	s.summary(p, b, ps)
	if r.Detached {
		return nil
	}
	return s.mirror(ctx, b, p, &rec, r.NoEnv)
}

// ensureBox finds, wakes or creates the box. A new box is confirmed with
// its size and price first; nothing is billed without a yes.
func (s *Service) ensureBox(ctx context.Context, p domain.Project, r UpRequest, rec *ports.EnvRecord) (domain.Box, error) {
	b, ok, err := s.d.Cloud.FindBox(ctx, p.ID)
	if err != nil {
		return b, err
	}
	if ok {
		if r.Size != "" {
			if want, err := domain.SizeByName(r.Size); err == nil && want.InstanceType(b.Arch) != b.InstanceType {
				s.d.UI.Warn(fmt.Sprintf("this box is %s; to change it run `protolift resize %s`", b.InstanceType, want.Name))
			}
		}
		if b.State != domain.BoxRunning {
			s.d.UI.Step("waking your box")
			if err := s.d.Cloud.StartBox(ctx, b); err != nil {
				return b, err
			}
		}
		rec.BoxID, rec.InstanceType, rec.Arch = b.ID, b.InstanceType, b.Arch
		return b, s.d.Cloud.WaitReachable(ctx, b, s.d.UI.Info)
	}

	arch, why, err := s.chooseArch(ctx, p, r.Arch)
	if err != nil {
		return b, err
	}
	size, sizeWhy, err := s.chooseSize(p, r.Size)
	if err != nil {
		return b, err
	}
	itype := size.InstanceType(arch)
	s.d.UI.Info("chip: " + why)
	s.d.UI.Info("size: " + sizeWhy)
	q := fmt.Sprintf("Create box %s (%s) in %s · %s · stops after %d min idle?",
		size, itype, s.d.Cloud.Region(), domain.PriceText(s.d.Cloud.Region(), itype), p.Settings.IdleMinutes)
	if !s.d.UI.Confirm(q) {
		return b, errs.New(errs.CodeCancelled, "no box was created", "")
	}
	s.d.UI.Step("creating your box")
	b, err = s.d.Cloud.CreateBox(ctx, ports.BoxSpec{
		EnvID: p.ID, Name: p.Name, InstanceType: itype, Arch: arch, BootScript: s.d.Scripts.Boot(p.Settings),
	})
	if err != nil {
		return b, err
	}
	rec.BoxID, rec.InstanceType, rec.Arch = b.ID, itype, arch
	// Saved now, so a failure later in `up` still lets `down` find the box.
	if err := s.d.Store.SaveEnv(*rec); err != nil {
		return b, err
	}
	return b, s.d.Cloud.WaitReachable(ctx, b, s.d.UI.Info)
}

func (s *Service) chooseArch(ctx context.Context, p domain.Project, flag domain.Arch) (domain.Arch, string, error) {
	switch {
	case flag != "":
		return flag, string(flag) + " (you chose it)", nil
	case p.Settings.Arch != "":
		return p.Settings.Arch, string(p.Settings.Arch) + " (from your settings)", nil
	}
	h, err := s.d.Store.Hints(p)
	if err != nil {
		return "", "", err
	}
	s.d.UI.Step("checking which chips your images support")
	support := map[string]domain.ArchSupport{}
	c, cancel := context.WithTimeout(ctx, TimeoutInspect)
	defer cancel()
	type res struct {
		img string
		sup domain.ArchSupport
	}
	all := append(append([]string{}, h.Images...), h.BaseImages...)
	out := make(chan res, len(all))
	for _, img := range all {
		go func(img string) { out <- res{img, s.d.Images.Support(c, img)} }(img)
	}
	for range all {
		r := <-out
		support[r.img] = r.sup
	}
	choice := domain.ChooseArch(h, support)
	return choice.Arch, choice.Reason, nil
}

func (s *Service) chooseSize(p domain.Project, flag string) (domain.Size, string, error) {
	name, from := flag, "you chose it"
	if name == "" && p.Settings.Size != "" {
		name, from = p.Settings.Size, "from your settings"
	}
	if name != "" {
		sz, err := domain.SizeByName(name)
		return sz, fmt.Sprintf("%s (%s)", sz, from), err
	}
	h, err := s.d.Store.Hints(p)
	if err != nil {
		return domain.Size{}, "", err
	}
	gb := domain.EstimateMemoryGB(h)
	sz := domain.SmallestFitting(gb)
	return sz, fmt.Sprintf("%s (your stack needs about %.0f GB; change with --size)", sz, gb), nil
}

// waitBooted waits for the first-boot script and reports its log on failure.
func (s *Service) waitBooted(ctx context.Context, b domain.Box) error {
	return step(ctx, TimeoutBoot, "preparing the box", func(ctx context.Context) error {
		announced := false
		for {
			out, err := s.d.Remote.Run(ctx, b, s.d.Scripts.BootState(), TimeoutQuick)
			if err != nil {
				return err
			}
			state, log, _ := strings.Cut(strings.TrimSpace(out), "\n")
			switch state {
			case "ready":
				// First boot adds the box user to the docker group, and a
				// shared connection opened before that (by this or an
				// earlier run) cannot use Docker. A fresh login always can.
				return s.d.Remote.Reset(ctx, b)
			case "failed":
				return errs.New(errs.CodeRemoteFailed, "preparing the box failed:\n"+log,
					"run `protolift down` and `protolift up` to start fresh; if it fails again, report the log above")
			}
			if !announced {
				s.d.UI.Step("installing Docker and tools on the new box (about 2-3 minutes, only the first time)")
				announced = true
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	})
}
