// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package gcpcloud

import (
	"errors"
	"strings"
	"testing"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func TestTranslateMapsGcloudErrors(t *testing.T) {
	cases := map[string]errs.Code{
		"ERROR: (gcloud.compute.instances.list) There was a problem refreshing your current auth tokens: Reauthentication failed.": errs.CodeAuthExpired,
		"ERROR: Compute Engine API has not been used in project example before or it is disabled.":                                 errs.CodeNotSetUp,
		"ERROR: Required 'compute.instances.create' permission for 'projects/example'":                                             errs.CodeNotAllowed,
		"ERROR: Constraint constraints/compute.vmExternalIpAccess violated":                                                        errs.CodeNotAllowed,
		"ERROR: Quota 'CPUS' exceeded. Limit: 8.0 in region us-central1.":                                                          errs.CodeQuota,
		"ERROR: The zone does not have enough resources available to fulfill the request (ZONE_RESOURCE_POOL_EXHAUSTED)":           errs.CodeCapacity,
		"ERROR: The resource 'projects/example/global/firewalls/protolift-allow-iap-ssh' was not found":                            errs.CodeNotFound,
	}
	for stderr, want := range cases {
		if got := errs.CodeOf(translate(errors.New("exit 1"), stderr, "do it")); got != want {
			t.Fatalf("%q → %s, want %s", stderr, got, want)
		}
	}
	org := errs.As(translate(errors.New("exit 1"), "Constraint constraints/compute.vmExternalIpAccess violated", "create the box"))
	if !strings.Contains(org.Message, "organization") {
		t.Fatalf("organization policy must be named, got %q", org.Message)
	}
}

func TestToBoxStatesAndArch(t *testing.T) {
	c := &Cloud{region: "us-central1", project: "example"}
	b := c.toBox(instance{
		Name: "protolift-app-abc123", Status: "TERMINATED", Zone: "https://www.googleapis.com/compute/v1/projects/example/zones/us-central1-a",
		MachineType: "https://www.googleapis.com/compute/v1/projects/example/zones/us-central1-a/machineTypes/t2a-standard-4",
		Labels:      map[string]string{LabelEnv: "abc1234567"}, LastStart: "2026-11-05T10:00:00.000-07:00",
	})
	if b.State != domain.BoxStopped || b.Location != "us-central1-a" || b.InstanceType != "t2a-standard-4" || b.Arch != domain.ArchARM || b.EnvID != "abc1234567" || b.StartedAt.IsZero() {
		t.Fatalf("box = %+v", b)
	}
	for in, want := range map[string]domain.BoxState{"RUNNING": domain.BoxRunning, "STAGING": domain.BoxPending, "STOPPING": domain.BoxStopping, "SUSPENDED": domain.BoxStopped} {
		if state(in) != want {
			t.Fatalf("state(%s) = %s", in, state(in))
		}
	}
}

func TestTypesRoundTrip(t *testing.T) {
	c := &Cloud{region: "us-central1"}
	for _, s := range domain.Sizes {
		for _, a := range []domain.Arch{domain.ArchARM, domain.ArchIntel} {
			ty := c.InstanceType(s, a)
			got, arch, ok := c.SizeOf(ty)
			if !ok || got.Name != s.Name || arch != a {
				t.Fatalf("%s/%s → %s → %v %v %v", s.Name, a, ty, got, arch, ok)
			}
		}
	}
	if p := c.Price("n2-standard-2"); p.Known && p.USD <= 0 {
		t.Fatal("a known price must be positive")
	}
}

func TestBoxNameFitsAndIsUnique(t *testing.T) {
	n := boxName(strings.Repeat("a", 60), "abc1234567")
	if len(n) > 63 || !strings.HasSuffix(n, "-abc123") || !strings.HasPrefix(n, "protolift-") {
		t.Fatalf("name = %q (%d)", n, len(n))
	}
}

func TestProxyCommandUsesIAP(t *testing.T) {
	c := &Cloud{project: "example", region: "us-central1", gcloud: "/opt/google cloud/bin/gcloud"}
	p := c.ProxyCommand(domain.Box{ID: "vm1", Location: "us-central1-b"}, "")
	for _, want := range []string{`"/opt/google cloud/bin/gcloud" compute start-iap-tunnel %h %p --listen-on-stdin`, "--zone us-central1-b", "--project example"} {
		if !strings.Contains(p, want) {
			t.Fatalf("proxy %q missing %q", p, want)
		}
	}
}

func TestToBoxStopTime(t *testing.T) {
	c := &Cloud{region: "us-central1"}
	b := c.toBox(instance{Name: "x", Status: "TERMINATED", LastStop: "2026-11-05T12:00:00.000-07:00"})
	if b.StoppedAt.IsZero() || b.StoppedAt.UTC().Hour() != 19 {
		t.Fatalf("stop time = %v", b.StoppedAt)
	}
}
