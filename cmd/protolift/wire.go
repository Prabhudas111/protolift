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
	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// needsBox lists commands that connect to a box, so missing tools are
// reported before anything is created or billed.
var needsBox = map[string]bool{"up": true, "ssh": true, "logs": true}

// wire is the composition root: the only place that knows every adapter.
// ui is passed in so the terminal, the MCP server and the local screen can
// each drive the same use cases.
func wire(ctx context.Context, cmd string, o opts, ui ports.UI) (*env.Manager, error) {
	store, err := filestore.New()
	if err != nil {
		return nil, err
	}
	self, err := selfPath()
	if err != nil {
		return nil, err
	}
	l, cloudName := login{profile: o.profile, project: o.project, region: o.region, self: self}, o.cloud
	// A project remembers the cloud and login it was created with, so a later
	// command from another shell reaches the same box.
	if p, err := store.LoadProject(ctx, o.dir, o.name); err == nil {
		if cloudName == "" {
			cloudName = p.Settings.Cloud
		}
		if rec, ok, err := store.LoadEnv(p.ID); err == nil && ok {
			if cloudName == "" {
				cloudName = rec.Provider
			}
			if cloudName == "" || cloudName == rec.Provider {
				l.profile = orDefault(l.profile, rec.Profile)
				l.project = orDefault(l.project, rec.Project)
				l.region = orDefault(l.region, rec.Region)
			}
		}
	}
	prov, err := lookupProvider(orDefault(cloudName, defaultProvider))
	if err != nil {
		return nil, err
	}
	sshBin, err := sshremote.FindSSH()
	if needsBox[cmd] {
		if err != nil {
			return nil, err
		}
		if err := prov.tools(); err != nil {
			return nil, err
		}
	}
	cloud, err := prov.open(ctx, l)
	if err != nil {
		return nil, err
	}
	remote, err := sshremote.New(sshremote.Options{Root: store.Root, SSH: orDefault(sshBin, "ssh"), Router: cloud})
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
		UI:      ui,
		Clock:   clock{},
		Profile: l.profile,
		Project: projectOf(cloud, l.project),
	})
}

// projectOf records the project a GCP adapter resolved (it may come from
// gcloud's defaults), so later commands reuse it.
func projectOf(c ports.Cloud, given string) string {
	if p, ok := c.(interface{ Project() string }); ok {
		return p.Project()
	}
	return given
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

// runProxy is the hidden ProxyCommand for clouds whose tunnel protolift
// opens itself: `protolift __proxy aws --region R [--profile P] --pubkey FILE
// <instance-id> <port>`. ssh starts it once per connection; its stdin and
// stdout are the tunnel. (GCP's ProxyCommand is gcloud itself.)
func runProxy(argv []string, stderr io.Writer) int {
	if len(argv) == 0 || argv[0] != awscloud.Provider {
		fmt.Fprintln(stderr, "protolift __proxy: internal command, started by ssh")
		return 255
	}
	fs := flag.NewFlagSet("__proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	region := fs.String("region", "", "")
	profile := fs.String("profile", "", "")
	pubkey := fs.String("pubkey", "", "")
	if err := fs.Parse(argv[1:]); err != nil || fs.NArg() != 2 || *region == "" || *pubkey == "" {
		fmt.Fprintln(stderr, "protolift __proxy: internal command, started by ssh")
		return 255
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cloud, err := awscloud.New(ctx, *profile, *region, "")
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
