// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package application

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Fakes record what the use cases asked for, in order, so tests check the
// sequence and policy without any cloud.

type log struct {
	mu    sync.Mutex
	calls []string
}

func (l *log) add(s string) { l.mu.Lock(); l.calls = append(l.calls, s); l.mu.Unlock() }
func (l *log) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.calls {
		if c == s {
			return true
		}
	}
	return false
}

type fakeCloud struct {
	*log
	ready error
	box   *domain.Box
}

func (f *fakeCloud) Region() string                           { return "us-east-1" }
func (f *fakeCloud) Account(context.Context) (string, error)  { return "123456789012", nil }
func (f *fakeCloud) Init(context.Context, func(string)) error { f.add("init"); return nil }
func (f *fakeCloud) Ready(context.Context) error              { return f.ready }
func (f *fakeCloud) ListBoxes(context.Context, string) ([]domain.Box, error) {
	if f.box == nil {
		return nil, nil
	}
	return []domain.Box{*f.box}, nil
}
func (f *fakeCloud) FindBox(context.Context, string) (domain.Box, bool, error) {
	if f.box == nil {
		return domain.Box{}, false, nil
	}
	return *f.box, true, nil
}
func (f *fakeCloud) CreateBox(_ context.Context, s ports.BoxSpec) (domain.Box, error) {
	f.add("create:" + s.InstanceType)
	b := domain.Box{ID: "i-0123456789abcdef0", EnvID: s.EnvID, Name: "protolift-" + s.Name, State: domain.BoxPending, InstanceType: s.InstanceType, Arch: s.Arch, Region: "us-east-1"}
	f.box = &b
	return b, nil
}
func (f *fakeCloud) StartBox(context.Context, domain.Box) error { f.add("start"); return nil }
func (f *fakeCloud) StopBox(context.Context, domain.Box) error  { f.add("stop"); return nil }
func (f *fakeCloud) DeleteBox(context.Context, domain.Box) error {
	f.add("delete")
	f.box = nil
	return nil
}
func (f *fakeCloud) ResizeBox(_ context.Context, _ domain.Box, t string) error {
	f.add("resize:" + t)
	return nil
}
func (f *fakeCloud) WaitReachable(context.Context, domain.Box, func(string)) error { return nil }
func (f *fakeCloud) Uninstall(context.Context, func(string)) error                 { f.add("uninstall"); return nil }

type fakeRemote struct {
	*log
	compose string
	put     map[string]string
}

func (f *fakeRemote) Run(_ context.Context, _ domain.Box, script string, _ time.Duration) (string, error) {
	switch script {
	case "BOOTSTATE":
		return "ready\n", nil
	case "CONFIG":
		return f.compose, nil
	case "CLOSED":
		return "", nil
	case "HASH":
		return "h1", nil
	case "MEM":
		return "3.1", nil
	}
	f.add("run:" + script)
	return "", nil
}
func (f *fakeRemote) PutFile(_ context.Context, _ domain.Box, path string, data []byte, mode string) error {
	if f.put == nil {
		f.put = map[string]string{}
	}
	f.put[path] = mode + ":" + string(data)
	return nil
}
func (f *fakeRemote) Attach(context.Context, domain.Box, string) error { return nil }
func (f *fakeRemote) Forget(domain.Box) error                          { f.add("forget"); return nil }
func (f *fakeRemote) Reset(context.Context, domain.Box) error          { f.add("reset"); return nil }

type fakeTunnel struct {
	*log
	busy error
}

func (f *fakeTunnel) CheckFree([]int) error { return f.busy }
func (f *fakeTunnel) Mirror(ctx context.Context, _ domain.Box, ps []int, ev chan<- ports.TunnelEvent) error {
	f.add("mirror")
	ev <- ports.TunnelEvent{Connected: true}
	<-ctx.Done()
	return nil
}

type fakeSync struct{ *log }

func (f *fakeSync) Ensure(context.Context, domain.Box, domain.Project, func(string)) error {
	f.add("sync")
	return nil
}
func (f *fakeSync) Status(context.Context, string) ([]ports.SyncState, error) {
	return []ports.SyncState{{Folder: "app", Status: "in sync"}}, nil
}
func (f *fakeSync) Pause(context.Context, string) error  { f.add("pause"); return nil }
func (f *fakeSync) Remove(context.Context, string) error { f.add("unsync"); return nil }

type fakeStore struct {
	project domain.Project
	hints   domain.StackHints
	env     map[string][]byte
	rec     *ports.EnvRecord
}

func (f *fakeStore) LoadProject(context.Context, string, string) (domain.Project, error) {
	return f.project, nil
}
func (f *fakeStore) Hints(domain.Project) (domain.StackHints, error)   { return f.hints, nil }
func (f *fakeStore) EnvFiles(domain.Folder) (map[string][]byte, error) { return f.env, nil }
func (f *fakeStore) WriteSettingsTemplate(string, string) (string, error) {
	return "/tmp/settings.toml", nil
}
func (f *fakeStore) LoadEnv(string) (ports.EnvRecord, bool, error) {
	if f.rec == nil {
		return ports.EnvRecord{}, false, nil
	}
	return *f.rec, true, nil
}
func (f *fakeStore) SaveEnv(r ports.EnvRecord) error { f.rec = &r; return nil }
func (f *fakeStore) RemoveEnv(string) error          { f.rec = nil; return nil }

type fakeImages struct{ s domain.ArchSupport }

func (f fakeImages) Support(context.Context, string) domain.ArchSupport { return f.s }

// fakeScripts returns markers so fakeRemote can answer each kind of script.
type fakeScripts struct{}

func (fakeScripts) Boot(domain.Settings) string               { return "BOOT" }
func (fakeScripts) IdleConfig(domain.Settings) []byte         { return []byte("IDLE") }
func (fakeScripts) IdleConfigPath() string                    { return "/etc/protolift/idle.conf" }
func (fakeScripts) BootState() string                         { return "BOOTSTATE" }
func (fakeScripts) SetupHash(domain.Folder) string            { return "HASH" }
func (fakeScripts) Setup(f domain.Folder) string              { return "SETUP " + f.RemotePath }
func (fakeScripts) Start(f domain.Folder, _ string) string    { return "START " + f.RemotePath }
func (fakeScripts) ComposeConfig(domain.Folder) string        { return "CONFIG" }
func (fakeScripts) ClosedPorts([]int) string                  { return "CLOSED" }
func (fakeScripts) Logs(domain.Folder, string, string) string { return "LOGS" }
func (fakeScripts) MemoryPeakGB() string                      { return "MEM" }

type fakeUI struct {
	answer bool
	out    bytes.Buffer
	asked  []string
}

func (u *fakeUI) Step(m string) { u.out.WriteString("→ " + m + "\n") }
func (u *fakeUI) Info(m string) { u.out.WriteString("  " + m + "\n") }
func (u *fakeUI) Warn(m string) { u.out.WriteString("! " + m + "\n") }
func (u *fakeUI) Confirm(q string) bool {
	u.asked = append(u.asked, q)
	return u.answer
}
func (u *fakeUI) Out() io.Writer { return &u.out }

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// world is one wired Service plus handles on every fake.
type world struct {
	svc    *Service
	calls  *log
	cloud  *fakeCloud
	remote *fakeRemote
	tunnel *fakeTunnel
	store  *fakeStore
	ui     *fakeUI
}

func newWorld(answer bool) *world {
	l := &log{}
	p := domain.Project{
		ID: "abc1234567", Name: "app", Dir: "/home/dev/app",
		Folders:  []domain.Folder{{LocalPath: "/home/dev/app", RemotePath: "/work/app", Compose: "compose.yaml", Setup: "npm ci"}},
		Settings: domain.Settings{IdleMinutes: 30, MaxHours: 12},
	}
	w := &world{
		calls:  l,
		cloud:  &fakeCloud{log: l},
		remote: &fakeRemote{log: l, compose: `{"services":{"web":{"ports":[{"target":3000,"published":"3000"}]}}}`},
		tunnel: &fakeTunnel{log: l},
		store:  &fakeStore{project: p, hints: domain.StackHints{Images: []string{"postgres:16"}, Services: 2}, env: map[string][]byte{".env": []byte("SECRET=1")}},
		ui:     &fakeUI{answer: answer},
	}
	svc, err := New(Deps{
		Cloud: w.cloud, Remote: w.remote, Tunnel: w.tunnel, Sync: &fakeSync{log: l}, Store: w.store,
		Images: fakeImages{domain.ArchSupport{ARM: true, Intel: true, Known: true}}, Scripts: fakeScripts{},
		UI: w.ui, Clock: fixedClock{time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		panic(err)
	}
	w.svc = svc
	return w
}

func (w *world) output() string { return w.ui.out.String() }

// errCode is a short helper for assertions.
func errCode(err error) errs.Code { return errs.CodeOf(err) }

// portsRecord is a saved env record; setupHash "" means setup never ran.
func portsRecord(setupHash string) ports.EnvRecord {
	r := ports.EnvRecord{ID: "abc1234567"}
	if setupHash != "" {
		r.SetupHash = map[string]string{"/work/app": setupHash}
	}
	return r
}
