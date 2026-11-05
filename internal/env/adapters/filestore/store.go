// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package filestore implements ports.Store with files under ~/.protolift.
// It reads the user's project but never writes into it.
package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// composeFiles are the names Docker Compose itself looks for, in its order.
var composeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// maxEnvFile stops a huge file named .env from being read into memory.
const maxEnvFile = 1 << 20

// Store keeps protolift's files under Root.
type Store struct{ Root string }

var _ ports.Store = (*Store)(nil)

// New uses $PROTOLIFT_HOME, or ~/.protolift.
func New() (*Store, error) {
	if v := os.Getenv("PROTOLIFT_HOME"); v != "" {
		return &Store{Root: v}, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "cannot find your home folder", "set PROTOLIFT_HOME to a folder protolift may use")
	}
	return &Store{Root: filepath.Join(h, ".protolift")}, nil
}

// Dir returns a sub folder of Root, created owner-only.
func (s *Store) Dir(parts ...string) (string, error) {
	p := filepath.Join(append([]string{s.Root}, parts...)...)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", errs.Wrap(errs.CodeInternal, err, "cannot create "+p, "check the permissions of "+s.Root)
	}
	return p, nil
}

// settingsFile is the TOML shape of a project's settings.
type settingsFile struct {
	Cloud       string        `toml:"cloud"`
	Size        string        `toml:"size"`
	Arch        string        `toml:"arch"`
	IdleMinutes int           `toml:"idle_minutes"`
	MaxHours    int           `toml:"max_hours"`
	DailyUSD    float64       `toml:"daily_budget_usd"`
	MonthlyUSD  float64       `toml:"monthly_budget_usd"`
	Folders     []folderEntry `toml:"folder"`
}

type folderEntry struct {
	Path  string `toml:"path"`
	Setup string `toml:"setup"`
	Run   string `toml:"run"`
	Ports []int  `toml:"ports"`
}

func (s *Store) identity(dir, name string) (abs, id, pname, settings string, err error) {
	abs, err = filepath.Abs(dir)
	if err != nil {
		return "", "", "", "", errs.Wrap(errs.CodeInvalidInput, err, "cannot resolve the folder "+dir, "")
	}
	if st, e := os.Stat(abs); e != nil || !st.IsDir() {
		return "", "", "", "", errs.New(errs.CodeInvalidInput, abs+" is not a folder", "run protolift inside your project folder")
	}
	id = domain.ProjectID(abs, name)
	pd, err := s.Dir("projects")
	if err != nil {
		return "", "", "", "", err
	}
	return abs, id, domain.ProjectName(filepath.Base(abs), name), filepath.Join(pd, id+".toml"), nil
}

// LoadProject reads the folder and its optional settings file.
func (s *Store) LoadProject(_ context.Context, dir, name string) (domain.Project, error) {
	abs, id, pname, settingsPath, err := s.identity(dir, name)
	if err != nil {
		return domain.Project{}, err
	}
	var sf settingsFile
	if _, err := toml.DecodeFile(settingsPath, &sf); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.Project{}, errs.Wrap(errs.CodeInvalidInput, err, "your settings file has a mistake",
			"fix "+settingsPath+" (run `protolift config` to see its path)")
	}
	settings, err := domain.Settings{Cloud: strings.ToLower(sf.Cloud), Size: sf.Size, Arch: domain.Arch(strings.ToLower(sf.Arch)), IdleMinutes: sf.IdleMinutes, MaxHours: sf.MaxHours,
		Budget: domain.Budget{DailyUSD: sf.DailyUSD, MonthlyUSD: sf.MonthlyUSD}}.Normalize()
	if err != nil {
		return domain.Project{}, err
	}
	p := domain.Project{ID: id, Name: pname, Dir: abs, Settings: settings, SettingsPath: settingsPath}
	if len(sf.Folders) == 0 {
		sf.Folders = []folderEntry{{Path: "."}}
	}
	locals := make([]string, len(sf.Folders))
	for i, f := range sf.Folders {
		lp := f.Path
		if lp == "" {
			lp = "."
		}
		if !filepath.IsAbs(lp) {
			lp = filepath.Join(abs, lp)
		}
		lp = filepath.Clean(lp)
		if st, err := os.Stat(lp); err != nil || !st.IsDir() {
			return domain.Project{}, errs.New(errs.CodeInvalidInput, fmt.Sprintf("folder %q in your settings does not exist", f.Path), "fix "+settingsPath)
		}
		locals[i] = lp
	}
	remotes := domain.RemotePaths(locals)
	for i, f := range sf.Folders {
		folder := domain.Folder{LocalPath: locals[i], RemotePath: remotes[i], Setup: f.Setup, Run: f.Run, Ports: f.Ports}
		if f.Run == "" {
			folder.Compose = findCompose(locals[i])
		}
		p.Folders = append(p.Folders, folder)
	}
	return p, nil
}

func findCompose(dir string) string {
	for _, c := range composeFiles {
		if st, err := os.Stat(filepath.Join(dir, c)); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// EnvFiles returns .env, .env.local, .env.development and similar files at
// the top of the folder. Example files are left out: they hold no secrets
// and are already in git.
func (s *Store) EnvFiles(f domain.Folder) (map[string][]byte, error) {
	entries, err := os.ReadDir(f.LocalPath)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "cannot read "+f.LocalPath, "")
	}
	out := map[string][]byte{}
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || (n != ".env" && !strings.HasPrefix(n, ".env.")) {
			continue
		}
		if strings.HasSuffix(n, ".example") || strings.HasSuffix(n, ".sample") || strings.HasSuffix(n, ".template") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, errs.Wrap(errs.CodeInternal, err, "cannot read "+n, "")
		}
		if info.Size() > maxEnvFile {
			return nil, errs.New(errs.CodeInvalidInput, n+" is larger than 1 MB", "env files should hold settings only. Use --no-env to skip them")
		}
		b, err := os.ReadFile(filepath.Join(f.LocalPath, n))
		if err != nil {
			return nil, errs.Wrap(errs.CodeInternal, err, "cannot read "+n, "")
		}
		out[n] = b
	}
	return out, nil
}

// WriteSettingsTemplate creates the settings file if it is missing.
func (s *Store) WriteSettingsTemplate(dir, name string) (string, error) {
	abs, _, _, path, err := s.identity(dir, name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(template(abs)), 0o600); err != nil {
		return "", errs.Wrap(errs.CodeInternal, err, "cannot write "+path, "")
	}
	return path, nil
}

func envPath(s *Store, id string) (string, error) {
	d, err := s.Dir("envs")
	if err != nil {
		return "", err
	}
	return filepath.Join(d, id+".json"), nil
}

// LoadEnv returns the saved record, or ok=false when there is none.
func (s *Store) LoadEnv(id string) (ports.EnvRecord, bool, error) {
	p, err := envPath(s, id)
	if err != nil {
		return ports.EnvRecord{}, false, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return ports.EnvRecord{}, false, nil
	}
	if err != nil {
		return ports.EnvRecord{}, false, errs.Wrap(errs.CodeInternal, err, "cannot read "+p, "")
	}
	var r ports.EnvRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return ports.EnvRecord{}, false, errs.Wrap(errs.CodeInternal, err, p+" is damaged", "delete it. `protolift up` rebuilds it from AWS")
	}
	return r, true, nil
}

// SaveEnv writes atomically, so a crash never leaves half a file.
func (s *Store) SaveEnv(r ports.EnvRecord) error {
	p, err := envPath(s, r.ID)
	if err != nil {
		return err
	}
	sort.Ints(r.Ports)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode state", "")
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write "+tmp, "")
	}
	if err := os.Rename(tmp, p); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot save "+p, "")
	}
	return nil
}

// RemoveEnv deletes the record and the env's key files.
func (s *Store) RemoveEnv(id string) error {
	p, err := envPath(s, id)
	if err != nil {
		return err
	}
	kd, err := s.Dir("keys")
	if err != nil {
		return err
	}
	var all []error
	for _, f := range []string{p, filepath.Join(kd, id), filepath.Join(kd, id+".pub")} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			all = append(all, err)
		}
	}
	if err := errors.Join(all...); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot remove local files", "delete them by hand in "+s.Root)
	}
	return nil
}

func template(dir string) string {
	return fmt.Sprintf(`# protolift settings for %s
# This file lives outside your project, so your repo stays unchanged.

# Cloud: "aws" or "gcp". Empty = aws (or --cloud).
cloud = ""
# Box size: S (2 CPU, 8 GB), M (4, 16), L (8, 32), XL (16, 32). Empty = protolift suggests one.
size = ""
# Chip: "arm", "intel" or "" to detect from your images.
arch = ""
# The box stops itself after this many idle minutes, and always after max_hours.
idle_minutes = %d
max_hours = %d
# Spending caps for this project, in US dollars (0 = no cap). protolift
# refuses to start over budget, and the box stops itself when the budget runs out.
daily_budget_usd = 0
monthly_budget_usd = 0

# One [[folder]] per folder to sync. A folder with a compose file needs only "path".
# A folder started by a command needs "run" and the "ports" it listens on.
[[folder]]
path = "."
# setup = "npm ci"          # runs on the box when the lockfile changes
# run = "npm run dev"        # leave out to use the compose file
# ports = [3000]

# A second folder, for example a backend next to this one:
# [[folder]]
# path = "../backend"
# setup = "go mod download"
# run = "make dev"
# ports = [8080]
`, dir, domain.DefaultIdleMinutes, domain.DefaultMaxHours)
}
