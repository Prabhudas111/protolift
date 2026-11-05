// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package sshremote implements ports.Remote and ports.Tunnel with the
// system OpenSSH client. The connection goes through AWS Systems Manager
// (the ProxyCommand is protolift itself), so the box needs no open port.
// Keys are per env; the box only ever holds them for 60 seconds.
package sshremote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
	"github.com/Prabhudas111/protolift/internal/shared/privdir"
)

// stderrTail is how much of a failed command's stderr is shown.
const stderrTail = 2000

// Router is the part of a cloud the ssh adapter needs: how to name a box
// and how to reach it through that cloud's private tunnel. The composition
// root passes the active cloud adapter, so this package never knows which
// cloud it talks to.
type Router interface {
	HostName(b domain.Box) string
	ProxyCommand(b domain.Box, pubKeyPath string) string
}

// Options configure the adapter. The composition root fills them in.
type Options struct {
	Root   string // protolift home (~/.protolift)
	SSH    string // path of the real ssh client
	Router Router
}

// Remote implements ports.Remote and ports.Tunnel.
type Remote struct {
	o  Options
	mu sync.Mutex // guards config writes
}

var (
	_ ports.Remote = (*Remote)(nil)
	_ ports.Tunnel = (*Remote)(nil)
)

// FindSSH returns the system ssh client or TOOL_MISSING with an install hint.
func FindSSH() (string, error) {
	p, err := exec.LookPath("ssh")
	if err != nil {
		fix := "install OpenSSH (it ships with macOS and most Linux systems)"
		if runtime.GOOS == "windows" {
			fix = "turn on Windows' OpenSSH Client: Settings → System → Optional features → OpenSSH Client"
		}
		return "", errs.Wrap(errs.CodeToolMissing, err, "the ssh program was not found", fix)
	}
	return p, nil
}

// New checks the options and prepares protolift's ssh folder.
func New(o Options) (*Remote, error) {
	if o.Root == "" || o.SSH == "" || o.Router == nil {
		return nil, errs.New(errs.CodeInternal, "ssh adapter wired without root, ssh path or cloud router", "this is a bug; please report it")
	}
	if err := os.MkdirAll(filepath.Join(o.Root, "ssh", "envs"), 0o700); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "cannot create the ssh folder", "")
	}
	return &Remote{o: o}, nil
}

// ConfigPath is the ssh config protolift passes with -F. It includes one
// file per box; protolift never edits the user's own ~/.ssh/config.
func (r *Remote) ConfigPath() string { return filepath.Join(r.o.Root, "ssh", "config") }

// Alias is the ssh host name of a box.
func Alias(b domain.Box) string { return "protolift-" + b.EnvID }

func (r *Remote) keyPath(envID string) string { return filepath.Join(r.o.Root, "keys", envID) }

// ensureKey creates the env's ed25519 key pair once.
func (r *Remote) ensureKey(envID string) error {
	priv := r.keyPath(envID)
	if _, err := os.Stat(priv); err == nil {
		if _, err := os.Stat(priv + ".pub"); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(priv), 0o700); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot create the key folder", "")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot create an ssh key", "")
	}
	block, err := ssh.MarshalPrivateKey(key, "protolift "+envID)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the ssh key", "")
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the public key", "")
	}
	if err := os.WriteFile(priv, pem.EncodeToMemory(block), 0o600); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot save the ssh key", "")
	}
	if err := os.WriteFile(priv+".pub", ssh.MarshalAuthorizedKey(sp), 0o600); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot save the public key", "")
	}
	return nil
}

// q quotes a path for an ssh config line.
func q(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// ensureConfig writes the box's Host block and the include file.
func (r *Remote) ensureConfig(b domain.Box) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureKey(b.EnvID); err != nil {
		return err
	}
	key := r.keyPath(b.EnvID)
	proxy := r.o.Router.ProxyCommand(b, key+".pub")
	var cb strings.Builder
	fmt.Fprintf(&cb, "Host %s\n", Alias(b))
	for _, kv := range [][2]string{
		{"HostName", r.o.Router.HostName(b)}, {"User", domain.BoxUser}, {"IdentityFile", q(key)}, {"IdentitiesOnly", "yes"},
		{"HostKeyAlias", b.ID}, {"UserKnownHostsFile", q(filepath.Join(r.o.Root, "ssh", "known_hosts"))},
		{"StrictHostKeyChecking", "accept-new"}, {"ProxyCommand", proxy},
		{"ServerAliveInterval", "15"}, {"ServerAliveCountMax", "4"}, {"ConnectTimeout", "60"}, {"LogLevel", "ERROR"},
	} {
		fmt.Fprintf(&cb, "  %s %s\n", kv[0], kv[1])
	}
	// Connection sharing makes each command ~1 s faster. Windows OpenSSH
	// has no ControlMaster, so it is turned on elsewhere only, and only when
	// a short private socket folder is available.
	// The socket is this folder plus ssh's 40-character %C hash.
	if runtime.GOOS != "windows" {
		if cm, ok := privdir.Short("cm"); ok && privdir.Fits(cm+"/"+strings.Repeat("x", 40)) {
			fmt.Fprintf(&cb, "  ControlMaster auto\n  ControlPath %s\n  ControlPersist 10m\n", q(cm+"/%C"))
		}
	}
	envFile := filepath.Join(r.o.Root, "ssh", "envs", b.EnvID+".conf")
	if err := writeIfChanged(envFile, cb.String()); err != nil {
		return err
	}
	include := fmt.Sprintf("# Written by protolift. Your own ~/.ssh/config is never changed.\nInclude %s\n", q(filepath.Join(r.o.Root, "ssh", "envs", "*.conf")))
	return writeIfChanged(r.ConfigPath(), include)
}

func writeIfChanged(path, body string) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == body {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write "+path, "")
	}
	if err := os.Rename(tmp, path); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot save "+path, "")
	}
	return nil
}

func (r *Remote) base(b domain.Box, extra ...string) []string {
	return append([]string{"-F", r.ConfigPath()}, append(extra, Alias(b))...)
}

// Run runs a bash script on the box, sent on stdin so no quoting of the
// script itself is needed, under its own time limit.
func (r *Remote) Run(ctx context.Context, b domain.Box, script string, timeout time.Duration) (string, error) {
	if err := r.ensureConfig(b); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.o.SSH, append(r.base(b, "-T"), "bash", "-s")...)
	cmd.Stdin = strings.NewReader("set -o pipefail\n" + script + "\n")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if err == nil {
		return out.String(), nil
	}
	if ctx.Err() != nil {
		return "", errs.Wrap(errs.CodeTimeout, ctx.Err(), fmt.Sprintf("a command on the box took longer than %s", timeout), "")
	}
	return "", r.explain(err, stderr.String())
}

// explain separates "could not connect" (ssh exit 255) from "the command
// failed", and keeps the useful end of stderr.
func (r *Remote) explain(err error, stderr string) error {
	tail := strings.TrimSpace(stderr)
	if len(tail) > stderrTail {
		tail = "…" + tail[len(tail)-stderrTail:]
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 255 {
		return errs.Wrap(errs.CodeNetwork, err, "could not connect to the box: "+tail,
			"check your internet and AWS login (`aws sso login`); the box may still be starting, so try again in a minute")
	}
	if tail == "" {
		tail = err.Error()
	}
	return errs.Wrap(errs.CodeRemoteFailed, err, "a command on the box failed:\n"+tail, "run `protolift ssh` to look, or `protolift logs`")
}

// PutFile writes data to path atomically, with the given mode.
func (r *Remote) PutFile(ctx context.Context, b domain.Box, path string, data []byte, mode string) error {
	if err := r.ensureConfig(b); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	dir := path[:strings.LastIndex(path, "/")+1]
	script := fmt.Sprintf("mkdir -p %s && umask 077 && cat > %s.protolift-tmp && chmod %s %s.protolift-tmp && mv -f %s.protolift-tmp %s",
		shq(dir), shq(path), shq(mode), shq(path), shq(path), shq(path))
	cmd := exec.CommandContext(ctx, r.o.SSH, append(r.base(b, "-T"), script)...)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errs.Wrap(errs.CodeTimeout, ctx.Err(), "copying a file to the box took longer than a minute", "")
		}
		return r.explain(err, stderr.String())
	}
	return nil
}

// Attach runs a command (or a login shell) with the terminal attached.
func (r *Remote) Attach(ctx context.Context, b domain.Box, command string) error {
	if err := r.ensureConfig(b); err != nil {
		return err
	}
	args := r.base(b, "-t")
	if command != "" {
		args = append(args, "bash", "-lc", shq(command))
	}
	cmd := exec.CommandContext(ctx, r.o.SSH, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() != 255 {
			return nil // the remote command's own exit; the user saw its output
		}
		return r.explain(err, "")
	}
	return nil
}

// Reset closes the shared connection to the box, if one is open. "No
// shared connection" is the state Reset wants, so it is success, not an error.
func (r *Remote) Reset(ctx context.Context, b domain.Box) error {
	if err := r.ensureConfig(b); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.o.SSH, "-F", r.ConfigPath(), "-O", "exit", Alias(b))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil || noSharedConnection(stderr.String()) {
		return nil
	}
	return r.explain(err, stderr.String())
}

// noSharedConnection recognises ssh's answers when there is no master to
// close: no socket file, nothing listening, or sharing not configured.
func noSharedConnection(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no such file") || strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no controlpath") || strings.Contains(s, "control socket connect")
}

// PublicKey returns the env's public key, creating the pair once.
func (r *Remote) PublicKey(envID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureKey(envID); err != nil {
		return "", err
	}
	b, err := os.ReadFile(r.keyPath(envID) + ".pub")
	if err != nil {
		return "", errs.Wrap(errs.CodeInternal, err, "cannot read the public key", "")
	}
	return strings.TrimSpace(string(b)), nil
}

// Forget removes the box's Host block and its saved host key.
func (r *Remote) Forget(b domain.Box) error {
	var all []error
	if err := os.Remove(filepath.Join(r.o.Root, "ssh", "envs", b.EnvID+".conf")); err != nil && !errors.Is(err, os.ErrNotExist) {
		all = append(all, err)
	}
	kh := filepath.Join(r.o.Root, "ssh", "known_hosts")
	if raw, err := os.ReadFile(kh); err == nil {
		var keep []string
		for _, line := range strings.Split(string(raw), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == b.ID {
				continue
			}
			keep = append(keep, line)
		}
		if err := os.WriteFile(kh, []byte(strings.Join(keep, "\n")), 0o600); err != nil {
			all = append(all, err)
		}
	}
	return errors.Join(all...)
}

// shq single-quotes a word for the remote bash.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
