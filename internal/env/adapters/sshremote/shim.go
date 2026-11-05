// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package sshremote

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Mutagen calls `ssh` and `scp` by name and cannot be given extra flags.
// protolift puts copies of itself named ssh and scp in a private folder and
// points Mutagen there (MUTAGEN_SSH_PATH). When run under those names the
// binary adds `-F <protolift config>` and hands over to the real program.
// This keeps the user's own ~/.ssh/config untouched, on every OS.

// Environment variables the shim reads. The composition root sets them for
// the Mutagen daemon, which passes them on to the shim.
const (
	EnvConfig  = "PROTOLIFT_SSH_CONFIG"
	EnvRealSSH = "PROTOLIFT_REAL_SSH"
	EnvRealSCP = "PROTOLIFT_REAL_SCP"
)

// ShimName reports whether the binary was started as the ssh or scp shim.
// Both separators are handled so the answer does not depend on which OS
// the binary was built for.
func ShimName(argv0 string) (string, bool) {
	base := argv0[strings.LastIndexAny(argv0, `/\`)+1:]
	n := strings.TrimSuffix(strings.ToLower(base), ".exe")
	return n, n == "ssh" || n == "scp"
}

// RunShim runs the real ssh or scp with protolift's config. It returns the
// child's exit code so the shim is transparent to Mutagen.
func RunShim(name string, args []string) int {
	cfg := os.Getenv(EnvConfig)
	real := os.Getenv(EnvRealSSH)
	if name == "scp" {
		real = os.Getenv(EnvRealSCP)
	}
	if cfg == "" || real == "" {
		os.Stderr.WriteString("protolift: the " + name + " shim was started without its settings\n")
		return 255
	}
	cmd := exec.Command(real, append([]string{"-F", cfg}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		os.Stderr.WriteString("protolift: cannot run " + real + ": " + err.Error() + "\n")
		return 255
	}
	return 0
}

// EnsureShim places the ssh and scp copies of self in dir and returns the
// environment the Mutagen daemon needs.
func EnsureShim(dir, self, configPath string) ([]string, error) {
	realSSH, err := FindSSH()
	if err != nil {
		return nil, err
	}
	realSCP, err := exec.LookPath("scp")
	if err != nil {
		return nil, errs.Wrap(errs.CodeToolMissing, err, "the scp program was not found", "install the OpenSSH client (it includes scp)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "cannot create the shim folder", "")
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, n := range []string{"ssh", "scp"} {
		if err := copyIfChanged(self, filepath.Join(dir, n+ext)); err != nil {
			return nil, err
		}
	}
	return []string{
		"MUTAGEN_SSH_PATH=" + dir,
		EnvConfig + "=" + configPath,
		EnvRealSSH + "=" + realSSH,
		EnvRealSCP + "=" + realSCP,
	}, nil
}

func copyIfChanged(src, dst string) error {
	si, err := os.Stat(src)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot read the protolift binary", "")
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() && !di.ModTime().Before(si.ModTime()) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot read the protolift binary", "")
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write "+tmp, "")
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return errs.Wrap(errs.CodeInternal, err, "cannot copy the protolift binary", "")
	}
	if err := out.Close(); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write "+tmp, "")
	}
	if err := os.Rename(tmp, dst); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot place "+dst, "")
	}
	return nil
}
