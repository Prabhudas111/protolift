// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package mutagensync implements ports.Sync with Mutagen, run as a private
// copy: its own data folder and daemon, so it never touches a Mutagen the
// user already has. Sessions are labelled with the env id; every command
// selects by that label, never by guessing names.
package mutagensync

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
	"github.com/Prabhudas111/protolift/internal/shared/privdir"
)

// Sync limits.
const (
	cmdTimeout = time.Minute
	firstSync  = 30 * time.Minute // first full copy of a large repo
	labelKey   = "protolift-env"
	// configLabel versions the session settings below. A session made with
	// other settings is recreated, so a protolift upgrade never leaves an
	// old, wrongly configured session running.
	configLabel  = "protolift-sync=v2"
	maxGitIgnore = 500 // lines read from .gitignore
)

// Always skipped: they are rebuilt on the box, or hold secrets that travel
// separately (.env, laptop → box only).
var defaultIgnores = []string{
	"node_modules", ".next", ".nuxt", ".svelte-kit", ".turbo", ".parcel-cache", ".cache",
	"dist", "build", "target", "coverage", "__pycache__", ".venv", "venv", ".pytest_cache",
	".DS_Store", "Thumbs.db", ".idea", ".vscode", ".env", ".env.*",
}

// Options configure the adapter.
type Options struct {
	Root string   // protolift home
	Env  []string // shim settings for the daemon (from sshremote.EnsureShim)
}

// Sync implements ports.Sync.
type Sync struct {
	o    Options
	bin  string
	data string // Mutagen's data folder; its daemon socket lives inside
}

var _ ports.Sync = (*Sync)(nil)

// New downloads (once) the pinned Mutagen and returns the adapter.
func New(ctx context.Context, o Options) (*Sync, error) {
	bin, err := ensureTool(ctx, o.Root)
	if err != nil {
		return nil, err
	}
	data, err := dataDir(o.Root)
	if err != nil {
		return nil, err
	}
	return &Sync{o: o, bin: bin, data: data}, nil
}

// dataDir keeps Mutagen's state under protolift's home unless the daemon
// socket path would pass the OS limit; then it uses the short private
// folder. Sessions there are recreated by Ensure after a reboot clears /tmp.
func dataDir(root string) (string, error) {
	home := filepath.Join(root, "mutagen")
	if privdir.Fits(filepath.Join(home, "daemon", "daemon.sock")) {
		return home, nil
	}
	if short, ok := privdir.Short("mutagen"); ok {
		return short, nil
	}
	return "", errs.New(errs.CodeInternal, "protolift's home folder path is too long for the file-sync daemon",
		"set PROTOLIFT_HOME to a shorter folder, for example ~/.protolift")
}

func (s *Sync) run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = append(os.Environ(), append([]string{"MUTAGEN_DATA_DIRECTORY=" + s.data}, s.o.Env...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errs.Wrap(errs.CodeTimeout, ctx.Err(), fmt.Sprintf("file sync (%s) took longer than %s", args[0]+" "+args[1], timeout), "")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errs.Wrap(errs.CodeRemoteFailed, err, "file sync failed: "+msg, "run `protolift status`; if it repeats, `protolift down` and `protolift up` start fresh")
	}
	return out.String(), nil
}

func selector(envID string) string { return labelKey + "=" + envID }

func sessionName(envID string, i int) string { return fmt.Sprintf("protolift-%s-%d", envID, i) }

// listed is the subset of Mutagen's JSON listing protolift reads.
type listed struct {
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	Conflicts []json.RawMessage `json:"conflicts"`
	Alpha     endpoint          `json:"alpha"`
	Beta      endpoint          `json:"beta"`
}

type endpoint struct {
	Path               string            `json:"path"`
	Host               string            `json:"host"`
	ScanProblems       []json.RawMessage `json:"scanProblems"`
	TransitionProblems []json.RawMessage `json:"transitionProblems"`
}

func (s *Sync) list(ctx context.Context, envID string) ([]listed, error) {
	out, err := s.run(ctx, cmdTimeout, "sync", "list", "--label-selector", selector(envID), "--template", "{{json .}}")
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil, nil
	}
	var ls []listed
	if err := json.Unmarshal([]byte(out), &ls); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "cannot read the file-sync status", "")
	}
	return ls, nil
}

// Ensure creates missing sessions, resumes paused ones, removes sessions of
// folders no longer listed, then waits for a full sync.
func (s *Sync) Ensure(ctx context.Context, b domain.Box, p domain.Project, log func(string)) error {
	existing, err := s.list(ctx, p.ID)
	if err != nil {
		return err
	}
	have := map[string]listed{}
	for _, l := range existing {
		have[l.Name] = l
	}
	want := map[string]bool{}
	for i, f := range p.Folders {
		name := sessionName(p.ID, i)
		want[name] = true
		beta := "protolift-" + p.ID + ":" + f.RemotePath
		if cur, ok := have[name]; ok && cur.Alpha.Path == f.LocalPath && cur.Beta.Path == f.RemotePath && cur.Labels["protolift-sync"] == "v2" {
			continue
		} else if ok {
			if _, err := s.run(ctx, cmdTimeout, "sync", "terminate", name); err != nil {
				return err
			}
		}
		// Files land on the box readable by every process (0644/0755), like a
		// normal dev folder: services in containers run as other users, and
		// Mutagen's owner-only default would hide the files from them.
		args := []string{"sync", "create", "--name", name, "--label", selector(p.ID), "--label", configLabel,
			"--mode", "two-way-resolved", "--ignore-vcs", "--symlink-mode", "posix-raw",
			"--default-file-mode-beta", "0644", "--default-directory-mode-beta", "0755"}
		for _, ig := range ignores(f.LocalPath) {
			args = append(args, "--ignore", ig)
		}
		args = append(args, f.LocalPath, beta)
		log("sync " + path.Base(f.RemotePath) + ": " + f.LocalPath + " ↔ box:" + f.RemotePath)
		if _, err := s.run(ctx, 5*time.Minute, args...); err != nil {
			return err
		}
	}
	for name := range have {
		if !want[name] {
			if _, err := s.run(ctx, cmdTimeout, "sync", "terminate", name); err != nil {
				return err
			}
		}
	}
	if _, err := s.run(ctx, cmdTimeout, "sync", "resume", "--label-selector", selector(p.ID)); err != nil {
		return err
	}
	_, err = s.run(ctx, firstSync, "sync", "flush", "--label-selector", selector(p.ID))
	return err
}

// ignores is the default list plus the top-level .gitignore, so build
// output and caches the project already ignores never cross the network.
func ignores(dir string) []string {
	out := append([]string{}, defaultIgnores...)
	f, err := os.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < maxGitIgnore; n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// Status reports each session in plain words.
func (s *Sync) Status(ctx context.Context, envID string) ([]ports.SyncState, error) {
	ls, err := s.list(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := make([]ports.SyncState, 0, len(ls))
	for _, l := range ls {
		out = append(out, ports.SyncState{
			Folder:   path.Base(l.Beta.Path),
			Status:   plainStatus(l.Status),
			Problems: len(l.Alpha.ScanProblems) + len(l.Alpha.TransitionProblems) + len(l.Beta.ScanProblems) + len(l.Beta.TransitionProblems),
			Conflict: len(l.Conflicts),
		})
	}
	return out, nil
}

func plainStatus(s string) string {
	switch s {
	case "watching":
		return "in sync, watching for changes"
	case "disconnected", "halted-on-root-emptied", "halted-on-root-deletion", "halted-on-root-type-change":
		return "stopped (" + s + ")"
	case "":
		return "unknown"
	}
	return strings.ReplaceAll(s, "-", " ")
}

// Pause stops syncing (the box is stopping) without losing state.
func (s *Sync) Pause(ctx context.Context, envID string) error {
	_, err := s.run(ctx, cmdTimeout, "sync", "pause", "--label-selector", selector(envID))
	return ignoreNoSessions(err)
}

// Remove ends every session of the env.
func (s *Sync) Remove(ctx context.Context, envID string) error {
	_, err := s.run(ctx, cmdTimeout, "sync", "terminate", "--label-selector", selector(envID))
	return ignoreNoSessions(err)
}

// ignoreNoSessions treats "nothing matched" as success: pausing or removing
// an env with no sessions has nothing to do.
func ignoreNoSessions(err error) error {
	var e *errs.Error
	if errors.As(err, &e) && strings.Contains(strings.ToLower(e.Message), "no matching") {
		return nil
	}
	return err
}
