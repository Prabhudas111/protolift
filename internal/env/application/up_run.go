// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
)

// envWatchEvery is how often `up` checks .env files for changes while it
// mirrors ports. Cheap: it reads a few small files.
const envWatchEvery = 3 * time.Second

// pushEnvFiles copies changed .env files laptop → box only, owner-only.
// Contents are never printed; only file names are.
func (s *Service) pushEnvFiles(ctx context.Context, b domain.Box, p domain.Project, rec *ports.EnvRecord, announce bool) error {
	if rec.EnvFileHash == nil {
		rec.EnvFileHash = map[string]string{}
	}
	var pushed []string
	for _, f := range p.Folders {
		files, err := s.d.Store.EnvFiles(f)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sum := sha256.Sum256(files[n])
			key := f.RemotePath + "/" + n
			h := hex.EncodeToString(sum[:])
			if rec.EnvFileHash[key] == h {
				continue
			}
			if err := s.d.Remote.PutFile(ctx, b, path.Join(f.RemotePath, n), files[n], "0600"); err != nil {
				return err
			}
			rec.EnvFileHash[key] = h
			pushed = append(pushed, key)
		}
	}
	if len(pushed) > 0 && announce {
		s.d.UI.Info(fmt.Sprintf("copied %d env file(s) to the box (one way, owner-only): %s", len(pushed), strings.Join(pushed, ", ")))
	}
	return nil
}

// startFolders runs setup when lockfiles changed, starts every folder and
// collects the ports to mirror.
func (s *Service) startFolders(ctx context.Context, b domain.Box, p domain.Project, rec *ports.EnvRecord) ([]domain.PortSpec, error) {
	if rec.SetupHash == nil {
		rec.SetupHash = map[string]string{}
	}
	var lists [][]domain.PortSpec
	for _, f := range p.Folders {
		if err := s.setupFolder(ctx, b, f, rec); err != nil {
			return nil, err
		}
		s.d.UI.Step("starting " + path.Base(f.RemotePath))
		if err := step(ctx, TimeoutStart, "starting "+path.Base(f.RemotePath), func(ctx context.Context) error {
			_, err := s.d.Remote.Run(ctx, b, s.d.Scripts.Start(f, p.Name), TimeoutStart)
			return err
		}); err != nil {
			return nil, err
		}
		ps, err := s.folderPorts(ctx, b, f)
		if err != nil {
			return nil, err
		}
		lists = append(lists, ps)
	}
	merged, err := domain.MergePorts(lists...)
	if err != nil {
		return nil, err
	}
	rec.Ports = rec.Ports[:0]
	for _, p := range merged {
		rec.Ports = append(rec.Ports, p.Port)
	}
	s.reportClosedPorts(ctx, b, rec.Ports)
	return merged, nil
}

func (s *Service) setupFolder(ctx context.Context, b domain.Box, f domain.Folder, rec *ports.EnvRecord) error {
	if f.Setup == "" {
		return nil
	}
	h, err := s.d.Remote.Run(ctx, b, s.d.Scripts.SetupHash(f), TimeoutQuick)
	if err != nil {
		return err
	}
	h = strings.TrimSpace(h)
	if rec.SetupHash[f.RemotePath] == h {
		return nil
	}
	s.d.UI.Step(fmt.Sprintf("running setup in %s: %s", path.Base(f.RemotePath), f.Setup))
	if err := step(ctx, TimeoutSetup, "setup in "+path.Base(f.RemotePath), func(ctx context.Context) error {
		_, err := s.d.Remote.Run(ctx, b, s.d.Scripts.Setup(f), TimeoutSetup)
		return err
	}); err != nil {
		return err
	}
	rec.SetupHash[f.RemotePath] = h
	return nil
}

func (s *Service) folderPorts(ctx context.Context, b domain.Box, f domain.Folder) ([]domain.PortSpec, error) {
	if !f.IsCompose() {
		out := make([]domain.PortSpec, 0, len(f.Ports))
		for _, p := range f.Ports {
			out = append(out, domain.PortSpec{Port: p, Source: path.Base(f.RemotePath)})
		}
		return out, nil
	}
	cfg, err := s.d.Remote.Run(ctx, b, s.d.Scripts.ComposeConfig(f), TimeoutQuick)
	if err != nil {
		return nil, err
	}
	ps, warns, err := domain.ComposePorts([]byte(cfg))
	if err != nil {
		return nil, err
	}
	for _, w := range warns {
		s.d.UI.Warn(fmt.Sprintf("%s: %s", w.Service, w.Reason))
	}
	extra := make([]domain.PortSpec, 0, len(f.Ports))
	for _, p := range f.Ports {
		extra = append(extra, domain.PortSpec{Port: p, Source: path.Base(f.RemotePath)})
	}
	return append(ps, extra...), nil
}

// reportClosedPorts waits a little for services to listen, then names the
// ones still closed. It warns instead of failing: a service may be slow.
func (s *Service) reportClosedPorts(ctx context.Context, b domain.Box, ps []int) {
	if len(ps) == 0 {
		return
	}
	deadline := time.Now().Add(TimeoutPorts)
	var closed string
	for time.Now().Before(deadline) {
		out, err := s.d.Remote.Run(ctx, b, s.d.Scripts.ClosedPorts(ps), TimeoutQuick)
		if err != nil {
			s.d.UI.Warn("could not check which ports are open: " + err.Error())
			return
		}
		closed = strings.TrimSpace(out)
		if closed == "" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
	s.d.UI.Warn("nothing is listening yet on port(s) " + strings.ReplaceAll(closed, "\n", ", ") + "; check `protolift logs`")
}

func (s *Service) summary(p domain.Project, b domain.Box, ps []domain.PortSpec) {
	s.d.UI.Step("ready")
	for _, x := range ps {
		s.d.UI.Info(fmt.Sprintf("%-14s http://localhost:%d", x.Source, x.Port))
	}
	s.d.UI.Info(fmt.Sprintf("box %s · %s · stops itself after %d min idle (max %d h)",
		b.InstanceType, domain.PriceText(s.d.Cloud.Region(), b.InstanceType), p.Settings.IdleMinutes, p.Settings.MaxHours))
}

// mirror keeps the port tunnel up and pushes .env changes until Ctrl-C.
func (s *Service) mirror(ctx context.Context, b domain.Box, p domain.Project, rec *ports.EnvRecord, noEnv bool) error {
	events := make(chan ports.TunnelEvent, 8)
	done := make(chan error, 1)
	go func() { done <- s.d.Tunnel.Mirror(ctx, b, rec.Ports, events) }()
	tick := time.NewTicker(envWatchEvery)
	defer tick.Stop()
	for {
		select {
		case e := <-events:
			switch {
			case e.Connected:
				s.d.UI.Info("ports mirrored: " + joinPorts(rec.Ports) + " (Ctrl-C to stop mirroring; the box keeps running)")
			case e.Err != nil:
				s.d.UI.Warn(fmt.Sprintf("tunnel dropped (%v); reconnecting, try %d", e.Err, e.Attempt))
			}
		case <-tick.C:
			if noEnv {
				continue
			}
			if err := s.pushEnvFiles(ctx, b, p, rec, true); err != nil {
				s.d.UI.Warn("could not copy a changed .env file: " + err.Error())
				continue
			}
			if err := s.d.Store.SaveEnv(*rec); err != nil {
				s.d.UI.Warn("could not save state: " + err.Error())
			}
		case err := <-done:
			if ctx.Err() != nil {
				s.d.UI.Info(fmt.Sprintf("stopped mirroring. The box keeps running and stops itself after %d idle minutes; `protolift stop` stops it now.", p.Settings.IdleMinutes))
				return nil
			}
			return err
		}
	}
}

func joinPorts(ps []int) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = strconv.Itoa(p)
	}
	return strings.Join(out, ", ")
}
