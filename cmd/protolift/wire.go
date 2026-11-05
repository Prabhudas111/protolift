// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/env/adapters/awscloud"
	"github.com/Prabhudas111/protolift/internal/env/adapters/boxscripts"
	"github.com/Prabhudas111/protolift/internal/env/adapters/filestore"
	"github.com/Prabhudas111/protolift/internal/env/adapters/mutagensync"
	"github.com/Prabhudas111/protolift/internal/env/adapters/registry"
	"github.com/Prabhudas111/protolift/internal/env/adapters/sshremote"
	"github.com/Prabhudas111/protolift/internal/env/adapters/terminal"
	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// needsBox lists commands that connect to a box, so missing tools are
// reported before anything is created or billed.
var needsBox = map[string]bool{"up": true, "ssh": true, "logs": true}

// wire is the composition root: the only place that knows every adapter.
func wire(ctx context.Context, cmd string, o opts) (*env.Manager, error) {
	store, err := filestore.New()
	if err != nil {
		return nil, err
	}
	profile, region := o.profile, o.region
	// A project remembers the account it was created in, so a later command
	// from another shell reaches the same box.
	if p, err := store.LoadProject(ctx, o.dir, o.name); err == nil {
		if rec, ok, err := store.LoadEnv(p.ID); err == nil && ok {
			if profile == "" {
				profile = rec.Profile
			}
			if region == "" {
				region = rec.Region
			}
		}
	}
	cloud, err := awscloud.New(ctx, profile, region)
	if err != nil {
		return nil, err
	}
	self, err := selfPath()
	if err != nil {
		return nil, err
	}
	sshBin, err := sshremote.FindSSH()
	if err != nil && needsBox[cmd] {
		return nil, err
	}
	if needsBox[cmd] {
		if _, err := awscloud.FindPlugin(); err != nil {
			return nil, err
		}
	}
	remote, err := sshremote.New(sshremote.Options{Root: store.Root, Self: self, Region: cloud.Region(), Profile: profile, SSH: orDefault(sshBin, "ssh")})
	if err != nil {
		return nil, err
	}
	scripts, err := boxscripts.New()
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "the box scripts are broken", "this is a bug; please report it")
	}
	return env.NewManager(env.Deps{
		Cloud: cloud, Remote: remote, Tunnel: remote, Store: store,
		Sync:    &lazySync{root: store.Root, self: self, config: remote.ConfigPath()},
		Images:  registry.New(),
		Scripts: scripts,
		UI:      terminal.New(o.yes, o.quiet),
		Clock:   clock{},
		Profile: profile,
	})
}

func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", errs.Wrap(errs.CodeInternal, err, "cannot find the protolift program file", "")
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

// lazySync builds the Mutagen adapter on first use, so commands that never
// sync (init, list, config) never download it.
type lazySync struct {
	root, self, config string
	once               sync.Once
	s                  *mutagensync.Sync
	err                error
}

var _ ports.Sync = (*lazySync)(nil)

func (l *lazySync) get(ctx context.Context) (*mutagensync.Sync, error) {
	l.once.Do(func() {
		shimEnv, err := sshremote.EnsureShim(filepath.Join(l.root, "bin", "shim"), l.self, l.config)
		if err != nil {
			l.err = err
			return
		}
		l.s, l.err = mutagensync.New(ctx, mutagensync.Options{Root: l.root, Env: shimEnv})
	})
	return l.s, l.err
}

func (l *lazySync) Ensure(ctx context.Context, b domain.Box, p domain.Project, log func(string)) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.Ensure(ctx, b, p, log)
}

func (l *lazySync) Status(ctx context.Context, envID string) ([]ports.SyncState, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Status(ctx, envID)
}

func (l *lazySync) Pause(ctx context.Context, envID string) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.Pause(ctx, envID)
}

func (l *lazySync) Remove(ctx context.Context, envID string) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.Remove(ctx, envID)
}

// runProxy is the hidden ProxyCommand: `protolift __proxy --region R
// [--profile P] --pubkey FILE <instance-id> <port>`. ssh starts it once per
// connection; its stdin/stdout are the tunnel.
func runProxy(argv []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("__proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	region := fs.String("region", "", "")
	profile := fs.String("profile", "", "")
	pubkey := fs.String("pubkey", "", "")
	if err := fs.Parse(argv); err != nil || fs.NArg() != 2 || *region == "" || *pubkey == "" {
		fmt.Fprintln(stderr, "protolift __proxy: internal command, started by ssh")
		return 255
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cloud, err := awscloud.New(ctx, *profile, *region)
	if err == nil {
		err = cloud.Proxy(ctx, fs.Arg(0), fs.Arg(1), *pubkey)
	}
	if err != nil {
		e := errs.As(err)
		fmt.Fprintf(stderr, "protolift: %s", e.Message)
		if e.Fix != "" {
			fmt.Fprintf(stderr, " (fix: %s)", e.Fix)
		}
		fmt.Fprintln(stderr)
		return 255
	}
	return 0
}
