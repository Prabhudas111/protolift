// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// StackHints is what protolift can learn about a stack on the laptop,
// before any box exists: which images it pulls and which base images it
// builds from. Used only to pick a chip and a size.
type StackHints struct {
	Images      []string // compose `image:` values, variables left as written
	BaseImages  []string // FROM lines of the Dockerfiles compose builds
	RunCommands []string // run commands of non-compose folders
	Services    int      // compose services, counted across folders
}

// ArchSupport is what one image's registry says about chips.
type ArchSupport struct {
	ARM, Intel bool
	Known      bool // false when the registry could not be read
}

// ArchChoice is the chosen chip and the sentence that explains it.
type ArchChoice struct {
	Arch   Arch
	Reason string
}

// ChooseArch picks ARM (cheaper) only when every image is known to support
// it. One Intel-only or unknown image means Intel, which always runs.
func ChooseArch(h StackHints, support map[string]ArchSupport) ArchChoice {
	images := append(append([]string{}, h.Images...), h.BaseImages...)
	sort.Strings(images)
	var unknown []string
	for _, img := range images {
		s := support[img]
		switch {
		case !s.Known:
			unknown = append(unknown, img)
		case !s.ARM:
			return ArchChoice{ArchIntel, fmt.Sprintf("Intel, because %s has no ARM build", img)}
		}
	}
	if len(unknown) > 0 {
		return ArchChoice{ArchIntel, fmt.Sprintf("Intel, because the chips of %s could not be checked (Intel always works)", strings.Join(unknown, ", "))}
	}
	if len(images) == 0 {
		return ArchChoice{ArchARM, "ARM: run commands only, and Go, Node, Python and Java run natively on ARM"}
	}
	return ArchChoice{ArchARM, "ARM, because every image supports it (about 20-45% cheaper than Intel)"}
}

// memory guesses in GB per kind of service. Deliberately generous: a box
// that is too small is worse than one size too big, and `status` shows the
// real peak so the user can size down.
var memoryByImage = []struct {
	match string
	gb    float64
}{
	{"elasticsearch", 3}, {"opensearch", 3}, {"clickhouse", 2}, {"kafka", 2}, {"localstack", 1.5},
	{"postgres", 1}, {"mysql", 1}, {"mariadb", 1}, {"mongo", 1}, {"rabbitmq", 1},
	{"redis", 0.5}, {"valkey", 0.5}, {"minio", 0.5}, {"nginx", 0.25}, {"caddy", 0.25},
}

// EstimateMemoryGB guesses how much memory the stack needs, including room
// for the system and Docker.
func EstimateMemoryGB(h StackHints) float64 {
	const system = 2.0
	total := system
	matched := 0
	for _, img := range h.Images {
		l := strings.ToLower(img)
		gb := 0.5
		for _, m := range memoryByImage {
			if strings.Contains(l, m.match) {
				gb = m.gb
				break
			}
		}
		total += gb
		matched++
	}
	// Built services (no image) are usually the app itself: a dev server.
	total += 1.5 * float64(max(h.Services-matched, 0))
	for _, cmd := range h.RunCommands {
		l := strings.ToLower(cmd)
		switch {
		case strings.Contains(l, "next") || strings.Contains(l, "npm") || strings.Contains(l, "pnpm") || strings.Contains(l, "yarn") || strings.Contains(l, "vite"):
			total += 2
		default:
			total += 1
		}
	}
	return total
}

// PortSpec is one port to mirror and where it comes from.
type PortSpec struct {
	Port   int
	Source string // service or folder name, for messages
}

// PortWarning is a port protolift cannot mirror, with the reason.
type PortWarning struct {
	Service string
	Reason  string
}

// composeConfig is the subset of `docker compose config --format json`
// that protolift reads.
type composeConfig struct {
	Services map[string]struct {
		NetworkMode string `json:"network_mode"`
		Ports       []struct {
			Target    int    `json:"target"`
			Published string `json:"published"`
			Protocol  string `json:"protocol"`
		} `json:"ports"`
	} `json:"services"`
}

// ComposePorts reads the published host ports from the box's resolved
// compose config. Variables, override files and profiles are already
// applied by Compose itself, so protolift never re-implements them.
func ComposePorts(configJSON []byte) ([]PortSpec, []PortWarning, error) {
	var c composeConfig
	if err := json.Unmarshal(configJSON, &c); err != nil {
		return nil, nil, errs.Wrap(errs.CodeRemoteFailed, err, "could not read the compose config from the box",
			"run `docker compose config` in your project to check the file")
	}
	names := make([]string, 0, len(c.Services))
	for n := range c.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var ports []PortSpec
	var warns []PortWarning
	for _, n := range names {
		s := c.Services[n]
		if s.NetworkMode == "host" {
			warns = append(warns, PortWarning{n, "uses network_mode: host, so its ports are not listed. Add them with `protolift config`"})
		}
		for _, p := range s.Ports {
			switch {
			case strings.EqualFold(p.Protocol, "udp"):
				warns = append(warns, PortWarning{n, fmt.Sprintf("port %d is UDP, which the tunnel cannot carry", p.Target)})
			case p.Published == "":
				warns = append(warns, PortWarning{n, fmt.Sprintf("port %d has no fixed host port (Docker picks one at random). Set one like \"%d:%d\"", p.Target, p.Target, p.Target)})
			default:
				lo, hi, err := portRange(p.Published)
				if err != nil {
					warns = append(warns, PortWarning{n, fmt.Sprintf("host port %q is not understood", p.Published)})
					continue
				}
				for port := lo; port <= hi; port++ {
					ports = append(ports, PortSpec{Port: port, Source: n})
				}
			}
		}
	}
	return ports, warns, nil
}

func portRange(s string) (int, int, error) {
	lo, hi, found := strings.Cut(s, "-")
	a, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, err
	}
	b := a
	if found {
		if b, err = strconv.Atoi(hi); err != nil {
			return 0, 0, err
		}
	}
	if ValidPort(a) != nil || ValidPort(b) != nil || b < a || b-a > 100 {
		return 0, 0, fmt.Errorf("bad range %s", s)
	}
	return a, b, nil
}

// MergePorts joins port lists and refuses a port claimed twice, because two
// services cannot both own one laptop port.
func MergePorts(lists ...[]PortSpec) ([]PortSpec, error) {
	owner := map[int]string{}
	var out []PortSpec
	for _, l := range lists {
		for _, p := range l {
			if prev, dup := owner[p.Port]; dup {
				if prev == p.Source {
					continue
				}
				return nil, errs.New(errs.CodeConflict, fmt.Sprintf("port %d is used by both %s and %s", p.Port, prev, p.Source),
					"change one of them so each port has one owner")
			}
			owner[p.Port] = p.Source
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, nil
}
