// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package gcpcloud

import (
	"context"
	"sort"
	"strings"

	"github.com/Prabhudas111/protolift/internal/env/domain"
)

// types maps each size to Compute Engine types: Tau T2A (Arm) and N2
// (Intel), both general purpose. XL is 16 CPU / 64 GB on GCP, the nearest
// shape to the 16 CPU / 32 GB minimum.
var types = map[string]struct{ arm, intel string }{
	"S":  {"t2a-standard-2", "n2-standard-2"},
	"M":  {"t2a-standard-4", "n2-standard-4"},
	"L":  {"t2a-standard-8", "n2-standard-8"},
	"XL": {"t2a-standard-16", "n2-standard-16"},
}

// pricesUSD: on-demand per hour (vCPU + memory) by region, from the Cloud
// Billing Catalog. Regions not listed show the pricing page instead.
var pricesUSD = map[string]map[string]float64{}

const pricingPage = "cloud.google.com/compute/vm-instance-pricing"

// InstanceType maps a size and chip to a machine type.
func (c *Cloud) InstanceType(s domain.Size, a domain.Arch) string {
	t := types[s.Name]
	if a == domain.ArchIntel {
		return t.intel
	}
	return t.arm
}

// SizeOf maps a machine type back to its size and chip.
func (c *Cloud) SizeOf(instanceType string) (domain.Size, domain.Arch, bool) {
	for _, s := range domain.Sizes {
		switch instanceType {
		case types[s.Name].arm:
			return s, domain.ArchARM, true
		case types[s.Name].intel:
			return s, domain.ArchIntel, true
		}
	}
	return domain.Size{}, "", false
}

// Price is the hourly estimate in this region, when known.
func (c *Cloud) Price(instanceType string) domain.Price {
	p, ok := pricesUSD[c.region][instanceType]
	return domain.Price{USD: p, Known: ok, Where: pricingPage}
}

// Supports reports whether a zone in the region offers the type; the zones
// are kept for CreateBox.
func (c *Cloud) Supports(ctx context.Context, instanceType string) (bool, error) {
	zones, err := c.zonesFor(ctx, instanceType)
	return len(zones) > 0, err
}

func (c *Cloud) zonesFor(ctx context.Context, instanceType string) ([]string, error) {
	if z, ok := c.zones[instanceType]; ok {
		return z, nil
	}
	var list []struct {
		Zone string `json:"zone"`
	}
	if err := c.run(ctx, callTimeout, &list, "compute", "machine-types", "list",
		"--filter", "name="+instanceType+" AND zone~^"+c.region+"-"); err != nil {
		return nil, err
	}
	zones := make([]string, 0, len(list))
	for _, x := range list {
		zones = append(zones, lastPart(x.Zone))
	}
	sort.Strings(zones)
	c.zones[instanceType] = zones
	return zones, nil
}

// lastPart returns the name at the end of a Compute Engine URL.
func lastPart(s string) string { return s[strings.LastIndex(s, "/")+1:] }
