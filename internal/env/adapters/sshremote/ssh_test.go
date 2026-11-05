// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package sshremote

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func newTest(t *testing.T) *Remote {
	t.Helper()
	r, err := New(Options{Root: t.TempDir(), SSH: "ssh", Router: fakeRouter{}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConfigHasProxyKeyAndNoOpenHostKeys(t *testing.T) {
	r := newTest(t)
	b := domain.Box{ID: "i-0123456789abcdef0", EnvID: "abc1234567"}
	if err := r.ensureConfig(b); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(r.o.Root, "ssh", "envs", "abc1234567.conf"))
	if err != nil {
		t.Fatal(err)
	}
	conf := string(raw)
	for _, want := range []string{
		"Host protolift-abc1234567", "HostName i-0123456789abcdef0", `ProxyCommand "/opt/protolift bin/protolift" __proxy test`, ".pub\" %h %p", "StrictHostKeyChecking accept-new", "HostKeyAlias i-0123456789abcdef0", "IdentitiesOnly yes",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("config missing %q:\n%s", want, conf)
		}
	}
	st, err := os.Stat(r.keyPath("abc1234567"))
	if err != nil {
		t.Fatalf("private key must exist: %v", err)
	}
	// Unix permission bits only exist on unix. Windows protects the file
	// through the user profile's ACL instead.
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key must be owner-only, got %v", st.Mode().Perm())
	}
	inc, _ := os.ReadFile(r.ConfigPath())
	if !strings.Contains(string(inc), "Include") {
		t.Fatal("main config must include the per-box files")
	}
}

func TestForgetRemovesOnlyThatBox(t *testing.T) {
	r := newTest(t)
	b := domain.Box{ID: "i-0123456789abcdef0", EnvID: "abc1234567"}
	if err := r.ensureConfig(b); err != nil {
		t.Fatal(err)
	}
	kh := filepath.Join(r.o.Root, "ssh", "known_hosts")
	if err := os.WriteFile(kh, []byte("i-0123456789abcdef0 ssh-ed25519 AAA\nother ssh-ed25519 BBB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Forget(b); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(kh)
	if strings.Contains(string(got), "i-0123456789abcdef0") || !strings.Contains(string(got), "other") {
		t.Fatalf("known_hosts after forget: %q", got)
	}
}

func TestCheckFreeFindsBusyPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	err = newTest(t).CheckFree([]int{port})
	if errs.CodeOf(err) != errs.CodePortBusy || !strings.Contains(errs.As(err).Message, strconv.Itoa(port)) {
		t.Fatalf("want PORT_BUSY naming %d, got %v", port, err)
	}
}

func TestShimName(t *testing.T) {
	for in, want := range map[string]bool{"/x/ssh": true, `C:\p\SSH.EXE`: true, "/x/scp": true, "/x/protolift": false} {
		if _, ok := ShimName(in); ok != want {
			t.Fatalf("ShimName(%q) = %v", in, ok)
		}
	}
}

// fakeRouter stands in for a cloud adapter.
type fakeRouter struct{}

func (fakeRouter) HostName(b domain.Box) string { return b.ID }
func (fakeRouter) ProxyCommand(_ domain.Box, pub string) string {
	return `"/opt/protolift bin/protolift" __proxy test --pubkey "` + pub + `" %h %p`
}
