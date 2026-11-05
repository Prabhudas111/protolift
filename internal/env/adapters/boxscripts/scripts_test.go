// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package boxscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Prabhudas111/protolift/internal/env/domain"
)

// bashPath finds bash. Every supported dev and CI machine has it (macOS,
// Linux, and Git Bash on Windows runners), so a missing bash is a failure.
func bashPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is required to test the box scripts: %v", err)
	}
	return p
}

func TestBootRendersWithSettings(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	out := s.Boot(domain.Settings{IdleMinutes: 45, MaxHours: 8})
	if got := string(s.IdleConfig(domain.Settings{IdleMinutes: 30, MaxHours: 12}, 95)); !strings.Contains(got, "BUDGET_MINUTES=95") {
		t.Fatalf("idle config = %q", got)
	}
	for _, want := range []string{"IDLE_MINUTES=45", "MAX_HOURS=8", "BUDGET_MINUTES=0", "protolift-idle.timer", "touch \"$STATE/ready\"", "/work"} {
		if !strings.Contains(out, want) {
			t.Fatalf("boot script missing %q", want)
		}
	}
	if strings.Contains(out, "{{") {
		t.Fatal("boot script has unrendered template markers")
	}
}

func TestQuoteSurvivesShell(t *testing.T) {
	nasty := `it's "quoted" $(rm -rf /) ; echo hi`
	out, err := exec.Command(bashPath(t), "-c", "printf '%s' "+quote(nasty)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != nasty {
		t.Fatalf("quote round trip: got %q", out)
	}
}

func TestSystemdQuoteEscapesExpansion(t *testing.T) {
	got := systemdQuote(`npm run dev -- --port $PORT "x" 100%`)
	want := `"npm run dev -- --port $$PORT \"x\" 100%%"`
	if got != want {
		t.Fatalf("systemdQuote = %s, want %s", got, want)
	}
}

func TestStartComposeAndService(t *testing.T) {
	s, _ := New()
	c := s.Start(domain.Folder{RemotePath: "/work/my app", Compose: "compose.yaml"}, "app")
	if !strings.Contains(c, "cd '/work/my app' && docker compose -f 'compose.yaml' up -d") {
		t.Fatalf("compose start = %s", c)
	}
	r := s.Start(domain.Folder{RemotePath: "/work/api", Run: "make dev", Ports: []int{8080}}, "My App")
	for _, want := range []string{"protolift-my-app-api.service", "systemctl restart", "cmp -s"} {
		if !strings.Contains(r, want) {
			t.Fatalf("service start missing %q:\n%s", want, r)
		}
	}
}

func TestScriptsAreValidBash(t *testing.T) {
	bash := bashPath(t)
	s, _ := New()
	scripts := map[string]string{
		"idle.sh":  idleScript,
		"boot.sh":  s.Boot(domain.Settings{IdleMinutes: 30, MaxHours: 12}),
		"start.sh": s.Start(domain.Folder{RemotePath: "/work/api", Run: "make dev"}, "app"),
		"state.sh": s.BootState(),
		"hash.sh":  s.SetupHash(domain.Folder{RemotePath: "/work/api", Setup: "npm ci"}),
		"ports.sh": s.ClosedPorts([]int{3000, 8080}),
		"mem.sh":   s.MemoryPeakGB(),
	}
	for name, body := range scripts {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bash, "-n", p).CombinedOutput(); err != nil {
			t.Fatalf("%s syntax: %v\n%s", name, err, out)
		}
	}
}
