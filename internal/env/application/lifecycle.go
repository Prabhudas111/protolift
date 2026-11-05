// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package application

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Init sets up the AWS account and region once.
func (s *Service) Init(ctx context.Context) error {
	acct, err := s.d.Cloud.Account(ctx)
	if err != nil {
		return err
	}
	s.d.UI.Step(fmt.Sprintf("setting up %s: %s in %s (safe to run again)", s.d.Cloud.Provider(), acct, s.d.Cloud.Region()))
	if err := s.d.Cloud.Init(ctx, s.d.UI.Info); err != nil {
		return err
	}
	s.d.UI.Step("done. Next: run `protolift up` in your project folder")
	return nil
}

// Stop stops the box and keeps its disk. Sync pauses so nothing retries.
func (s *Service) Stop(ctx context.Context, t Target) error {
	p, b, err := s.requireBox(ctx, t)
	if err != nil {
		return err
	}
	if err := s.d.Sync.Pause(ctx, p.ID); err != nil {
		return err
	}
	s.d.UI.Step("stopping the box (your files and data stay on its disk)")
	if err := s.d.Cloud.StopBox(ctx, b); err != nil {
		return err
	}
	if err := s.closeRun(b); err != nil {
		return err
	}
	s.d.UI.Step("stopped. `protolift up` starts it again")
	return nil
}

// Resize changes the box size. The disk and data stay.
func (s *Service) Resize(ctx context.Context, t Target, size string) error {
	sz, err := domain.SizeByName(size)
	if err != nil {
		return err
	}
	p, b, err := s.requireBox(ctx, t)
	if err != nil {
		return err
	}
	itype := s.d.Cloud.InstanceType(sz, b.Arch)
	if ok, err := s.d.Cloud.Supports(ctx, itype); err != nil {
		return err
	} else if !ok {
		return errs.New(errs.CodeCapacity, fmt.Sprintf("%s does not offer %s", s.d.Cloud.Region(), itype), "pick another size")
	}
	if itype == b.InstanceType {
		s.d.UI.Info("the box is already " + sz.String())
		return nil
	}
	q := fmt.Sprintf("Resize to %s (%s, %s)? The box stops while it changes.", sz, itype, s.d.Cloud.Price(itype).Text())
	if !s.d.UI.Confirm(q) {
		return errs.New(errs.CodeCancelled, "the size was not changed", "")
	}
	if err := s.d.Sync.Pause(ctx, p.ID); err != nil {
		return err
	}
	s.d.UI.Step("changing the size")
	if err := s.d.Cloud.ResizeBox(ctx, b, itype); err != nil {
		return err
	}
	rec, _, err := s.d.Store.LoadEnv(p.ID)
	if err != nil {
		return err
	}
	rec.InstanceType = itype
	if err := s.d.Store.SaveEnv(rec); err != nil {
		return err
	}
	s.d.UI.Step("resized. Run `protolift up` to start it")
	return nil
}

// Down deletes the box, its disk, its alarm, its sync and local state.
func (s *Service) Down(ctx context.Context, t Target) error {
	p, b, ok, err := s.loadProject(ctx, t)
	if err != nil {
		return err
	}
	if !ok {
		s.d.UI.Info(p.Name + " has no box. Cleaning up local files only")
	} else if !s.d.UI.Confirm(fmt.Sprintf("Delete box %s and everything on its disk? Your laptop files are not touched.", b.Name)) {
		return errs.New(errs.CodeCancelled, "nothing was deleted", "")
	}
	if err := s.d.Sync.Remove(ctx, p.ID); err != nil {
		return err
	}
	if ok {
		s.d.UI.Step("deleting the box")
		if err := s.d.Cloud.DeleteBox(ctx, b); err != nil {
			return err
		}
		if err := s.closeRun(b); err != nil {
			return err
		}
		if err := s.d.Remote.Forget(b); err != nil {
			s.d.UI.Warn("could not clean the saved host key: " + err.Error())
		}
	}
	if err := s.d.Store.RemoveEnv(p.ID); err != nil {
		return err
	}
	s.d.UI.Step("deleted. Nothing of this project is left in " + s.d.Cloud.Provider())
	return nil
}

// Uninstall removes the account pieces made by Init.
func (s *Service) Uninstall(ctx context.Context) error {
	if !s.d.UI.Confirm(fmt.Sprintf("Remove protolift's one-time setup (access and firewall) from %s %s?", s.d.Cloud.Provider(), s.d.Cloud.Region())) {
		return errs.New(errs.CodeCancelled, "nothing was removed", "")
	}
	if err := s.d.Cloud.Uninstall(ctx, s.d.UI.Info); err != nil {
		return err
	}
	s.d.UI.Step("removed. protolift has nothing left in this account and region")
	return nil
}

// Config creates the settings file (outside the project) and prints its path.
func (s *Service) Config(_ context.Context, t Target) (string, error) {
	return s.d.Store.WriteSettingsTemplate(t.Dir, t.Name)
}

// Mirror re-attaches the ports of a running box until ctx ends, for callers
// that started it with Detached (the MCP server, the local screen).
func (s *Service) Mirror(ctx context.Context, t Target) error {
	p, b, err := s.requireRunning(ctx, t)
	if err != nil {
		return err
	}
	rec, ok, err := s.d.Store.LoadEnv(p.ID)
	if err != nil {
		return err
	}
	if !ok || len(rec.Ports) == 0 {
		return errs.New(errs.CodeNotFound, "no ports are known for this box yet", "run `protolift up` once")
	}
	if err := s.d.Tunnel.CheckFree(rec.Ports); err != nil {
		return err
	}
	return s.mirror(ctx, b, p, &rec, true)
}

// SSH opens a terminal on the box.
func (s *Service) SSH(ctx context.Context, t Target) error {
	_, b, err := s.requireRunning(ctx, t)
	if err != nil {
		return err
	}
	return s.d.Remote.Attach(ctx, b, "")
}

// Logs follows the logs of one folder (or one compose service).
func (s *Service) Logs(ctx context.Context, t Target, service string) error {
	p, b, err := s.requireRunning(ctx, t)
	if err != nil {
		return err
	}
	f := p.Folders[0]
	if service != "" {
		for _, x := range p.Folders {
			if strings.HasSuffix(x.RemotePath, "/"+service) {
				f, service = x, ""
				break
			}
		}
	}
	return s.d.Remote.Attach(ctx, b, s.d.Scripts.Logs(f, p.Name, service))
}

// StatusReport is what `protolift status` shows.
type StatusReport struct {
	Project    string        `json:"project"`
	Box        *domain.Box   `json:"box"`
	Size       string        `json:"size,omitempty"`
	Price      string        `json:"price,omitempty"`
	CostSoFar  string        `json:"costSoFar,omitempty"`
	Ports      []int         `json:"ports"`
	Sync       []string      `json:"sync"`
	PeakGB     float64       `json:"peakMemoryGb"`
	Advice     string        `json:"advice,omitempty"`
	SpentToday float64       `json:"spentTodayUsd"`
	SpentMonth float64       `json:"spentMonthUsd"`
	Budget     domain.Budget `json:"budget"`
}

// Status reports the box, cost, sync and a size suggestion from real use.
func (s *Service) Status(ctx context.Context, t Target) (StatusReport, error) {
	p, b, ok, err := s.loadProject(ctx, t)
	if err != nil {
		return StatusReport{}, err
	}
	r := StatusReport{Project: p.Name, Budget: p.Settings.Budget}
	if !ok {
		return r, nil
	}
	if err := s.observe(b); err != nil {
		return r, err
	}
	if l, err := s.d.Store.LoadLedger(); err == nil {
		sp := s.spent(l, p.ID)
		r.SpentToday, r.SpentMonth, r.Budget = sp.Today, sp.Month, p.Settings.Budget
	}
	r.Box = &b
	if sz, _, found := s.d.Cloud.SizeOf(b.InstanceType); found {
		r.Size = sz.String()
	}
	price := s.d.Cloud.Price(b.InstanceType)
	r.Price = price.Text()
	if b.State == domain.BoxRunning {
		if c, ok := domain.CostSoFar(price, b.StartedAt, s.d.Clock.Now()); ok {
			r.CostSoFar = fmt.Sprintf("about $%.2f since it started %s", c, b.StartedAt.Local().Format("15:04"))
		}
		if out, err := s.d.Remote.Run(ctx, b, s.d.Scripts.MemoryPeakGB(), TimeoutQuick); err == nil {
			if gb, err := strconv.ParseFloat(strings.TrimSpace(out), 64); err == nil {
				r.PeakGB = gb
				r.Advice = s.sizeAdvice(gb, b)
			}
		}
	}
	if rec, ok, err := s.d.Store.LoadEnv(p.ID); err == nil && ok {
		r.Ports = rec.Ports
	}
	if st, err := s.d.Sync.Status(ctx, p.ID); err == nil {
		for _, x := range st {
			line := fmt.Sprintf("%s: %s", x.Folder, x.Status)
			if x.Conflict > 0 || x.Problems > 0 {
				line += fmt.Sprintf(" (%d conflicts, %d problems. Run `protolift ssh` to look)", x.Conflict, x.Problems)
			}
			r.Sync = append(r.Sync, line)
		}
	}
	return r, nil
}

// sizeAdvice suggests a smaller size when peak use leaves lots of room, or
// a bigger one when the box is nearly full.
func (s *Service) sizeAdvice(peakGB float64, b domain.Box) string {
	cur, arch, ok := s.d.Cloud.SizeOf(b.InstanceType)
	if !ok {
		return ""
	}
	fit := domain.SmallestFitting(peakGB * 1.3)
	switch {
	case fit.MemoryGB < cur.MemoryGB:
		return fmt.Sprintf("peak use %.1f GB of %d GB: size %s would do (`protolift resize %s`, %s)",
			peakGB, cur.MemoryGB, fit.Name, fit.Name, s.d.Cloud.Price(s.d.Cloud.InstanceType(fit, arch)).Text())
	case peakGB > float64(cur.MemoryGB)*0.9:
		return fmt.Sprintf("peak use %.1f GB of %d GB: the box is nearly full. Consider `protolift resize` to a bigger size", peakGB, cur.MemoryGB)
	}
	return ""
}

// List shows every protolift box in the region, from any project.
func (s *Service) List(ctx context.Context) ([]domain.Box, error) {
	boxes, err := s.d.Cloud.ListBoxes(ctx, "")
	if err != nil {
		return nil, err
	}
	return boxes, s.observe(boxes...)
}
