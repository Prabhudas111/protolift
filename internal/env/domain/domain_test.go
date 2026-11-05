// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"testing"
	"time"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func TestProjectIDSeparatesFoldersAndNames(t *testing.T) {
	a := ProjectID("/home/dev/app", "")
	if a != ProjectID("/home/dev/app", "") {
		t.Fatal("same folder must give the same id")
	}
	if a == ProjectID("/home/dev/app-2", "") || a == ProjectID("/home/dev/app", "agent-1") {
		t.Fatal("different folders or names must give different ids")
	}
	if len(a) != 10 {
		t.Fatalf("id length = %d, want 10", len(a))
	}
}

func TestRemotePathsAreUniqueAndSafe(t *testing.T) {
	got := RemotePaths([]string{"/a/My App", "/b/my-app", `C:\work\web`})
	want := []string{"/work/my-app", "/work/my-app-2", "/work/web"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RemotePaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSettingsNormalize(t *testing.T) {
	s, err := Settings{}.Normalize()
	if err != nil || s.IdleMinutes != DefaultIdleMinutes || s.MaxHours != DefaultMaxHours {
		t.Fatalf("defaults not applied: %+v %v", s, err)
	}
	for _, bad := range []Settings{{IdleMinutes: 1}, {MaxHours: 500}, {Size: "XXL"}, {Arch: "sparc"}} {
		if _, err := bad.Normalize(); errs.CodeOf(err) != errs.CodeInvalidInput {
			t.Fatalf("%+v: want INVALID_INPUT, got %v", bad, err)
		}
	}
}

func TestValidateFolders(t *testing.T) {
	if err := ValidateFolders([]Folder{{LocalPath: "/x", Run: "npm run dev"}}); errs.CodeOf(err) != errs.CodeInvalidInput {
		t.Fatalf("run folder without ports must fail, got %v", err)
	}
	if err := ValidateFolders([]Folder{{LocalPath: "/x", Compose: "compose.yaml"}}); err != nil {
		t.Fatalf("compose folder must pass: %v", err)
	}
}

func TestChooseArch(t *testing.T) {
	h := StackHints{Images: []string{"postgres:16"}, BaseImages: []string{"node:22"}}
	all := map[string]ArchSupport{"postgres:16": {ARM: true, Intel: true, Known: true}, "node:22": {ARM: true, Intel: true, Known: true}}
	if c := ChooseArch(h, all); c.Arch != ArchARM {
		t.Fatalf("all ARM images must pick ARM, got %+v", c)
	}
	intelOnly := map[string]ArchSupport{"postgres:16": {ARM: true, Known: true}, "node:22": {Intel: true, Known: true}}
	if c := ChooseArch(h, intelOnly); c.Arch != ArchIntel {
		t.Fatalf("an Intel-only image must pick Intel, got %+v", c)
	}
	if c := ChooseArch(h, map[string]ArchSupport{}); c.Arch != ArchIntel {
		t.Fatalf("unknown images must pick Intel, got %+v", c)
	}
	if c := ChooseArch(StackHints{RunCommands: []string{"make dev"}}, nil); c.Arch != ArchARM {
		t.Fatalf("run commands only must pick ARM, got %+v", c)
	}
}

func TestEstimateAndSize(t *testing.T) {
	h := StackHints{Images: []string{"postgres:16", "localstack/localstack:4"}, Services: 3, RunCommands: []string{"npm run dev"}}
	gb := EstimateMemoryGB(h) // 2 + 1 + 1.5 + 1.5 (one built service) + 2
	if gb != 8 {
		t.Fatalf("estimate = %v, want 8", gb)
	}
	if s := SmallestFitting(gb); s.Name != "S" {
		t.Fatalf("8 GB must fit S, got %s", s.Name)
	}
	if s := SmallestFitting(9); s.Name != "M" {
		t.Fatalf("9 GB must pick M, got %s", s.Name)
	}
	if s := SmallestFitting(99); s.Name != "XL" {
		t.Fatalf("too big must cap at XL, got %s", s.Name)
	}
}

func TestComposePorts(t *testing.T) {
	cfg := []byte(`{"services":{
	  "web":{"ports":[{"target":3000,"published":"3000","protocol":"tcp"}]},
	  "db":{"ports":[{"target":5432,"published":"5433-5434"}]},
	  "dns":{"ports":[{"target":53,"published":"53","protocol":"udp"}]},
	  "rand":{"ports":[{"target":9000}]},
	  "host":{"network_mode":"host"}}}`)
	ports, warns, err := ComposePorts(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 3 || ports[0].Port != 5433 || ports[1].Port != 5434 || ports[2].Port != 3000 {
		t.Fatalf("ports = %+v", ports)
	}
	if len(warns) != 3 {
		t.Fatalf("want 3 warnings (udp, random, host), got %+v", warns)
	}
	if _, _, err := ComposePorts([]byte("not json")); errs.CodeOf(err) != errs.CodeRemoteFailed {
		t.Fatalf("bad json must be REMOTE_FAILED, got %v", err)
	}
}

func TestMergePortsRefusesDoubleOwner(t *testing.T) {
	_, err := MergePorts([]PortSpec{{3000, "web"}}, []PortSpec{{3000, "api"}})
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("want CONFLICT, got %v", err)
	}
	got, err := MergePorts([]PortSpec{{8080, "api"}, {3000, "web"}}, []PortSpec{{3000, "web"}})
	if err != nil || len(got) != 2 || got[0].Port != 3000 {
		t.Fatalf("merge = %+v, %v", got, err)
	}
}

func TestPricesAndCost(t *testing.T) {
	if _, ok := HourlyUSD("ap-south-1", "m7g.xlarge"); !ok {
		t.Fatal("known price missing")
	}
	if PriceText("xx-none-1", "m7g.xlarge") == "" {
		t.Fatal("unknown region must still give text")
	}
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	c, ok := CostSoFar("us-east-1", "m7g.large", start, start.Add(2*time.Hour))
	if !ok || c < 0.163 || c > 0.164 {
		t.Fatalf("cost = %v %v", c, ok)
	}
	for _, s := range Sizes {
		if _, a, ok := SizeOfType(s.ARM); !ok || a != ArchARM {
			t.Fatalf("SizeOfType(%s) failed", s.ARM)
		}
	}
}
