// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package ports declares what the application needs from the outside world.
// Each interface has exactly one production adapter under adapters/, and
// tests use fakes. The application depends on these, never on AWS, SSH or
// Mutagen directly, so a provider can change without touching a use case.
package ports

import (
	"context"
	"io"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
)

// Cloud creates, finds and removes boxes and the one-time account pieces.
type Cloud interface {
	Region() string
	Account(ctx context.Context) (string, error)
	// Init is idempotent: it only creates what is missing.
	Init(ctx context.Context, log func(string)) error
	// Ready returns NOT_SET_UP when Init has not been run.
	Ready(ctx context.Context) error
	ListBoxes(ctx context.Context, envID string) ([]domain.Box, error)
	// FindBox returns ok=false when the env has no box.
	FindBox(ctx context.Context, envID string) (domain.Box, bool, error)
	CreateBox(ctx context.Context, spec BoxSpec) (domain.Box, error)
	StartBox(ctx context.Context, b domain.Box) error
	StopBox(ctx context.Context, b domain.Box) error
	DeleteBox(ctx context.Context, b domain.Box) error
	ResizeBox(ctx context.Context, b domain.Box, instanceType string) error
	// WaitReachable blocks until the box runs and the private tunnel works.
	WaitReachable(ctx context.Context, b domain.Box, log func(string)) error
	// Uninstall refuses with CONFLICT while any box exists.
	Uninstall(ctx context.Context, log func(string)) error
}

// BoxSpec is everything a new box needs.
type BoxSpec struct {
	EnvID, Name  string
	InstanceType string
	Arch         domain.Arch
	BootScript   string
}

// Remote runs commands on a box over the private tunnel.
type Remote interface {
	// Run runs a bash script on the box and returns its stdout. A non-zero
	// exit is REMOTE_FAILED with the last lines of stderr in the message.
	Run(ctx context.Context, b domain.Box, script string, timeout time.Duration) (string, error)
	// PutFile writes data to path on the box with the given mode.
	PutFile(ctx context.Context, b domain.Box, path string, data []byte, mode string) error
	// Attach runs a command with the user's terminal attached (ssh, logs).
	Attach(ctx context.Context, b domain.Box, command string) error
	// Reset closes any shared connection, so the next command logs in fresh
	// (needed after the box changes the user's groups during first boot).
	Reset(ctx context.Context, b domain.Box) error
	// Forget drops what the laptop remembers about a deleted box.
	Forget(b domain.Box) error
}

// TunnelEvent reports the state of the port mirror.
type TunnelEvent struct {
	Connected bool
	Attempt   int
	Err       error
}

// Tunnel mirrors box ports to the same laptop ports.
type Tunnel interface {
	// CheckFree returns PORT_BUSY naming the first laptop port in use.
	CheckFree(ports []int) error
	// Mirror blocks until ctx ends, reconnecting with backoff.
	Mirror(ctx context.Context, b domain.Box, ports []int, events chan<- TunnelEvent) error
}

// SyncState is one folder's sync status in people words.
type SyncState struct {
	Folder   string
	Status   string
	Problems int
	Conflict int
}

// Sync keeps laptop folders and box folders the same, both ways.
type Sync interface {
	// Ensure creates or resumes one session per folder and waits for the
	// first full sync.
	Ensure(ctx context.Context, b domain.Box, p domain.Project, log func(string)) error
	Status(ctx context.Context, envID string) ([]SyncState, error)
	Pause(ctx context.Context, envID string) error
	Remove(ctx context.Context, envID string) error
}

// Store keeps protolift's own files on the laptop.
type Store interface {
	// LoadProject reads the folder and its settings. It never writes to the
	// project folder.
	LoadProject(ctx context.Context, dir, name string) (domain.Project, error)
	// Hints reads compose and Dockerfiles to learn images, for chip and size.
	Hints(p domain.Project) (domain.StackHints, error)
	// EnvFiles returns the .env files at the top of each folder.
	EnvFiles(f domain.Folder) (map[string][]byte, error)
	// WriteSettingsTemplate creates the settings file of the project in dir
	// if missing and returns its path. It works even when the existing
	// settings have a mistake, so the user can always open the file.
	WriteSettingsTemplate(dir, name string) (string, error)
	LoadEnv(id string) (EnvRecord, bool, error)
	SaveEnv(r EnvRecord) error
	RemoveEnv(id string) error
}

// EnvRecord is what protolift remembers about an env between runs.
type EnvRecord struct {
	ID, Name, Region, Profile string
	BoxID, InstanceType       string
	Arch                      domain.Arch
	Ports                     []int
	EnvFileHash               map[string]string
	SetupHash                 map[string]string
}

// ImageInspector asks image registries which chips an image supports.
type ImageInspector interface {
	Support(ctx context.Context, image string) domain.ArchSupport
}

// UI is how use cases talk to the person at the terminal.
type UI interface {
	Step(msg string)
	Info(msg string)
	Warn(msg string)
	// Confirm asks yes/no. With --yes it returns true without asking; with
	// no terminal attached it returns false.
	Confirm(question string) bool
	Out() io.Writer
}

// Scripts writes the bash that runs on the box. The application decides
// what runs and in which order; this port decides how bash spells it.
type Scripts interface {
	// Boot is the first-start script (packages, idle stop, ready marker).
	Boot(s domain.Settings) string
	// IdleConfig is the idle-stop settings file content.
	IdleConfig(s domain.Settings) []byte
	IdleConfigPath() string
	// BootState prints "ready", "waiting", or "failed" then a log tail.
	BootState() string
	// SetupHash prints a hash of the folder's lockfiles.
	SetupHash(f domain.Folder) string
	Setup(f domain.Folder) string
	// Start starts the folder: compose up, or a system service for Run.
	Start(f domain.Folder, envName string) string
	ComposeConfig(f domain.Folder) string
	// ClosedPorts prints the ports nothing listens on yet, one per line.
	ClosedPorts(ports []int) string
	Logs(f domain.Folder, envName, service string) string
	// MemoryPeakGB prints the highest memory use seen since boot, in GB.
	MemoryPeakGB() string
}

// Clock lets tests control time.
type Clock interface{ Now() time.Time }
