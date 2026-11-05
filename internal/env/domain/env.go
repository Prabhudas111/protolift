// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package domain holds protolift's rules as plain data and pure functions:
// what an environment is, which box size and chip fit a stack, which ports
// to mirror. It imports no cloud, network or file code, so every rule here
// is tested without AWS.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Cost-safety defaults. A box never runs unattended for long.
const (
	DefaultIdleMinutes = 30
	DefaultMaxHours    = 12
	MaxIdleMinutes     = 24 * 60
	MaxMaxHours        = 72
	BoxUser            = "ubuntu"
	WorkRoot           = "/work"
)

// Folder is one project folder synced to the box and how it is started.
type Folder struct {
	LocalPath  string // absolute path on the laptop
	RemotePath string // absolute path on the box, under WorkRoot
	Compose    string // compose file name when the folder is a compose stack
	Setup      string // optional; runs on the box when a lockfile changes
	Run        string // command that starts the folder; empty for compose
	Ports      []int  // ports a Run folder listens on
}

// IsCompose reports whether Docker Compose starts this folder.
func (f Folder) IsCompose() bool { return f.Run == "" }

// Settings are the user's optional choices for one project.
type Settings struct {
	Cloud       string // provider name (aws, gcp); "" means the default
	Size        string // S, M, L, XL or "" for a suggestion
	Arch        Arch   // "" means detect
	IdleMinutes int
	MaxHours    int
	Budget      Budget // this project's caps; the global caps live apart
}

// Project is one environment: a folder on the laptop plus its settings.
type Project struct {
	ID           string // stable id: one box per project folder (+ name)
	Name         string // readable, safe for tags and paths
	Dir          string
	Folders      []Folder
	Settings     Settings
	SettingsPath string // where the user edits Settings
}

var unsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// Slug makes a name safe for AWS tags, systemd units and box paths.
func Slug(s string) string {
	out := strings.Trim(unsafe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// ProjectID is the env identity. Two git worktrees are two folders, so each
// gets its own box: isolation for parallel work comes for free.
func ProjectID(absDir, name string) string {
	sum := sha256.Sum256([]byte(absDir + "\x00" + name))
	return hex.EncodeToString(sum[:])[:10]
}

// ProjectName builds the readable name from the folder name and --name.
func ProjectName(dirBase, name string) string {
	n := Slug(dirBase)
	if n == "" {
		n = "project"
	}
	if s := Slug(name); s != "" {
		n += "-" + s
	}
	return n
}

// RemotePaths gives every folder a unique path on the box.
func RemotePaths(localPaths []string) []string {
	seen := map[string]bool{}
	out := make([]string, len(localPaths))
	for i, p := range localPaths {
		base := Slug(path.Base(strings.ReplaceAll(p, `\`, "/")))
		if base == "" {
			base = "folder"
		}
		name := base
		for n := 2; seen[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		seen[name] = true
		out[i] = WorkRoot + "/" + name
	}
	return out
}

// Normalize fills defaults and checks every setting, returning one clear
// error for the first problem.
func (s Settings) Normalize() (Settings, error) {
	if s.IdleMinutes == 0 {
		s.IdleMinutes = DefaultIdleMinutes
	}
	if s.MaxHours == 0 {
		s.MaxHours = DefaultMaxHours
	}
	if s.IdleMinutes < 5 || s.IdleMinutes > MaxIdleMinutes {
		return s, errs.New(errs.CodeInvalidInput, fmt.Sprintf("idle_minutes is %d", s.IdleMinutes),
			fmt.Sprintf("use a value from 5 to %d", MaxIdleMinutes))
	}
	if s.MaxHours < 1 || s.MaxHours > MaxMaxHours {
		return s, errs.New(errs.CodeInvalidInput, fmt.Sprintf("max_hours is %d", s.MaxHours),
			fmt.Sprintf("use a value from 1 to %d", MaxMaxHours))
	}
	if s.Size != "" {
		if _, err := SizeByName(s.Size); err != nil {
			return s, err
		}
	}
	if err := s.Budget.Validate(); err != nil {
		return s, err
	}
	if s.Arch != "" && s.Arch != ArchARM && s.Arch != ArchIntel {
		return s, errs.New(errs.CodeInvalidInput, fmt.Sprintf("arch %q is not known", s.Arch), `use "arm" or "intel"`)
	}
	return s, nil
}

// ValidateFolders checks run folders declare ports and that no two folders
// claim the same port.
func ValidateFolders(fs []Folder) error {
	if len(fs) == 0 {
		return errs.New(errs.CodeInvalidInput, "no folders to run", "add a [[folder]] in your settings (protolift config)")
	}
	for _, f := range fs {
		if f.Run == "" && f.Compose == "" {
			return errs.New(errs.CodeInvalidInput,
				fmt.Sprintf("%s has no compose file and no run command", f.LocalPath),
				"add a compose.yaml, or set run and ports for this folder with `protolift config`")
		}
		if f.Run != "" && len(f.Ports) == 0 {
			return errs.New(errs.CodeInvalidInput,
				fmt.Sprintf("%s has a run command but no ports", f.LocalPath),
				"list the ports it listens on, e.g. ports = [3000], with `protolift config`")
		}
		for _, p := range f.Ports {
			if err := ValidPort(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidPort checks a TCP port number.
func ValidPort(p int) error {
	if p < 1 || p > 65535 {
		return errs.New(errs.CodeInvalidInput, fmt.Sprintf("port %d is not a valid port", p), "use a number from 1 to 65535")
	}
	return nil
}
