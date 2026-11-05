// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Arch is the box chip family.
type Arch string

// Chip families.
const (
	ArchARM   Arch = "arm"
	ArchIntel Arch = "intel"
)

// BoxState mirrors the cloud's lifecycle in protolift words.
type BoxState string

// Box states.
const (
	BoxPending  BoxState = "pending"
	BoxRunning  BoxState = "running"
	BoxStopping BoxState = "stopping"
	BoxStopped  BoxState = "stopped"
)

// Box is one cloud box.
type Box struct {
	ID           string
	EnvID        string
	Name         string
	State        BoxState
	InstanceType string
	Arch         Arch
	Region       string
	StartedAt    time.Time // time of the last start
}

// Size is one box size protolift offers. General purpose up to L, compute
// for XL. No burstable types: their CPU credits run out during builds.
type Size struct {
	Name     string
	CPU      int
	MemoryGB int
	ARM      string
	Intel    string
}

// Sizes, smallest first.
var Sizes = []Size{
	{"S", 2, 8, "m7g.large", "m7i.large"},
	{"M", 4, 16, "m7g.xlarge", "m7i.xlarge"},
	{"L", 8, 32, "m7g.2xlarge", "m7i.2xlarge"},
	{"XL", 16, 32, "c7g.4xlarge", "c7i.4xlarge"},
}

// SizeByName finds S/M/L/XL in any case.
func SizeByName(n string) (Size, error) {
	for _, s := range Sizes {
		if strings.EqualFold(s.Name, n) {
			return s, nil
		}
	}
	return Size{}, errs.New(errs.CodeInvalidInput, fmt.Sprintf("size %q is not known", n), "use S, M, L or XL")
}

// SizeOfType finds the size behind an instance type.
func SizeOfType(t string) (Size, Arch, bool) {
	for _, s := range Sizes {
		switch t {
		case s.ARM:
			return s, ArchARM, true
		case s.Intel:
			return s, ArchIntel, true
		}
	}
	return Size{}, "", false
}

// InstanceType picks the type of a size for a chip.
func (s Size) InstanceType(a Arch) string {
	if a == ArchIntel {
		return s.Intel
	}
	return s.ARM
}

// String is the short form shown to users, e.g. "M · 4 CPU · 16 GB".
func (s Size) String() string {
	return fmt.Sprintf("%s · %d CPU · %d GB", s.Name, s.CPU, s.MemoryGB)
}

// SmallestFitting returns the smallest size with at least memGB memory.
func SmallestFitting(memGB float64) Size {
	for _, s := range Sizes {
		if float64(s.MemoryGB) >= memGB {
			return s
		}
	}
	return Sizes[len(Sizes)-1]
}

// pricesUSD: On-Demand Linux, per hour, from the AWS Price List API on
// 2026-11-05. Shown as estimates; AWS bills the real amount.
var pricesUSD = map[string]map[string]float64{
	"ap-south-1":     {"m7g.large": 0.0583, "m7g.xlarge": 0.1166, "m7g.2xlarge": 0.2333, "c7g.4xlarge": 0.3926, "m7i.large": 0.10605, "m7i.xlarge": 0.2121, "m7i.2xlarge": 0.4242, "c7i.4xlarge": 0.714},
	"us-east-1":      {"m7g.large": 0.0816, "m7g.xlarge": 0.1632, "m7g.2xlarge": 0.3264, "c7g.4xlarge": 0.58, "m7i.large": 0.1008, "m7i.xlarge": 0.2016, "m7i.2xlarge": 0.4032, "c7i.4xlarge": 0.714},
	"us-west-2":      {"m7g.large": 0.0816, "m7g.xlarge": 0.1632, "m7g.2xlarge": 0.3264, "c7g.4xlarge": 0.58, "m7i.large": 0.1008, "m7i.xlarge": 0.2016, "m7i.2xlarge": 0.4032, "c7i.4xlarge": 0.714},
	"eu-west-1":      {"m7g.large": 0.091, "m7g.xlarge": 0.1819, "m7g.2xlarge": 0.3638, "c7g.4xlarge": 0.6202, "m7i.large": 0.11235, "m7i.xlarge": 0.2247, "m7i.2xlarge": 0.4494, "c7i.4xlarge": 0.76608},
	"eu-central-1":   {"m7g.large": 0.0978, "m7g.xlarge": 0.1955, "m7g.2xlarge": 0.391, "c7g.4xlarge": 0.6596, "m7i.large": 0.12075, "m7i.xlarge": 0.2415, "m7i.2xlarge": 0.483, "c7i.4xlarge": 0.8148},
	"ap-southeast-1": {"m7g.large": 0.102, "m7g.xlarge": 0.204, "m7g.2xlarge": 0.408, "c7g.4xlarge": 0.6664, "m7i.large": 0.126, "m7i.xlarge": 0.252, "m7i.2xlarge": 0.504, "c7i.4xlarge": 0.8232},
}

// HourlyUSD returns the price per hour; ok=false when the region or type is
// not in the table, so callers say "see AWS pricing" instead of guessing.
func HourlyUSD(region, instanceType string) (float64, bool) {
	p, ok := pricesUSD[region][instanceType]
	return p, ok
}

// PriceText formats the hourly price for people.
func PriceText(region, instanceType string) string {
	if p, ok := HourlyUSD(region, instanceType); ok {
		return fmt.Sprintf("about $%.2f/hour", p)
	}
	return "see aws.amazon.com/ec2/pricing for the price"
}

// CostSoFar estimates spend since the last start.
func CostSoFar(region, instanceType string, since, now time.Time) (float64, bool) {
	p, ok := HourlyUSD(region, instanceType)
	if !ok || since.IsZero() || now.Before(since) {
		return 0, false
	}
	return p * now.Sub(since).Hours(), true
}
