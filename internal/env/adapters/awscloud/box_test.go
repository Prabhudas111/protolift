// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package awscloud

import (
	"strings"
	"testing"

	"github.com/Prabhudas111/protolift/internal/env/domain"
)

func TestStopTimeFromReason(t *testing.T) {
	got := stopTime("User initiated (2026-11-05 10:30:00 GMT)")
	if got.Hour() != 10 || got.Minute() != 30 || got.Day() != 5 {
		t.Fatalf("stop time = %v", got)
	}
	if !stopTime("").IsZero() || !stopTime("Server.InternalError").IsZero() {
		t.Fatal("no timestamp must give zero")
	}
}

func TestCatalogRoundTripAndPrices(t *testing.T) {
	c := &Cloud{region: "us-east-1", self: "/usr/local/bin/protolift", profile: "dev"}
	for _, s := range domain.Sizes {
		for _, a := range []domain.Arch{domain.ArchARM, domain.ArchIntel} {
			ty := c.InstanceType(s, a)
			got, arch, ok := c.SizeOf(ty)
			if !ok || got.Name != s.Name || arch != a || !c.Price(ty).Known {
				t.Fatalf("%s/%s → %s → %v %v %v", s.Name, a, ty, got, arch, ok)
			}
		}
	}
	if p := c.Price("m7g.large"); p.USD != 0.0816 {
		t.Fatalf("price = %v", p)
	}
	if (&Cloud{region: "xx-none-1"}).Price("m7g.large").Known {
		t.Fatal("an unknown region has no price")
	}
	pc := c.ProxyCommand(domain.Box{}, "/k.pub")
	for _, want := range []string{`"/usr/local/bin/protolift" __proxy aws --region us-east-1`, `--profile "dev"`, `--pubkey "/k.pub" %h %p`} {
		if !strings.Contains(pc, want) {
			t.Fatalf("proxy %q missing %q", pc, want)
		}
	}
}
