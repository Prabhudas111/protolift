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

// Box is one cloud box, in words every cloud shares.
type Box struct {
	ID           string    `json:"id"` // the cloud's id for the box (instance id, VM name)
	EnvID        string    `json:"envId"`
	Name         string    `json:"name"`
	State        BoxState  `json:"state"`
	InstanceType string    `json:"type"` // the cloud's own machine type name
	Arch         Arch      `json:"arch"`
	Region       string    `json:"region"`
	Location     string    `json:"zone,omitempty"` // zone, when the cloud places boxes in zones
	StartedAt    time.Time `json:"startedAt"`
	StoppedAt    time.Time `json:"stoppedAt"` // last stop, when the cloud reports it
}

// Size is a cloud-neutral box size: CPU and memory only. Each cloud adapter
// maps a Size to its own machine types, so adding a cloud never changes
// these rules.
type Size struct {
	Name     string
	CPU      int
	MemoryGB int
}

// Sizes, smallest first.
var Sizes = []Size{
	{"S", 2, 8},
	{"M", 4, 16},
	{"L", 8, 32},
	{"XL", 16, 32},
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

// Price is an hourly estimate from a cloud's own price table.
type Price struct {
	USD   float64
	Known bool   // false when the cloud has no price for this type and region
	Where string // the cloud's pricing page, shown when the price is unknown
}

// Text formats the price for people.
func (p Price) Text() string {
	if p.Known {
		return fmt.Sprintf("about $%.2f/hour", p.USD)
	}
	return "see " + p.Where + " for the price"
}

// CostSoFar estimates spend since the last start.
func CostSoFar(p Price, since, now time.Time) (float64, bool) {
	if !p.Known || since.IsZero() || now.Before(since) {
		return 0, false
	}
	return p.USD * now.Sub(since).Hours(), true
}
