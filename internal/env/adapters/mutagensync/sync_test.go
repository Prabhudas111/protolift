// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package mutagensync

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func TestIgnoresAddGitignoreButNotNegations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# comment\n/tmp\n*.log\n!keep.log\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ignores(dir)
	for _, want := range []string{"node_modules", ".env", ".env.*", "/tmp", "*.log"} {
		if !slices.Contains(got, want) {
			t.Fatalf("ignores missing %q: %v", want, got)
		}
	}
	if slices.Contains(got, "!keep.log") {
		t.Fatal("negations must not become ignores")
	}
}

func TestVerifyRejectsWrongChecksum(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := os.WriteFile(p, []byte("not mutagen"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(p, checksums["linux_amd64"]); errs.CodeOf(err) != errs.CodeInternal {
		t.Fatalf("a tampered download must be refused, got %v", err)
	}
}

func TestPlainStatus(t *testing.T) {
	if plainStatus("watching") != "in sync, watching for changes" || plainStatus("staging-beta") != "staging beta" {
		t.Fatal("status words")
	}
}

func TestEveryPlatformHasAChecksum(t *testing.T) {
	for _, p := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"} {
		if len(checksums[p]) != 64 {
			t.Fatalf("missing checksum for %s", p)
		}
	}
}
