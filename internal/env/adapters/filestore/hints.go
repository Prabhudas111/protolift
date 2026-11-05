// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// composeFile is the part of a compose file protolift reads on the laptop,
// only to choose a chip and a size. Ports are read later on the box, from
// Compose's own resolved config.
type composeFile struct {
	Services map[string]struct {
		Image string `yaml:"image"`
		Build any    `yaml:"build"` // a string (context) or a map
	} `yaml:"services"`
}

// Hints reads compose files and Dockerfiles in every folder.
func (s *Store) Hints(p domain.Project) (domain.StackHints, error) {
	var h domain.StackHints
	images, bases := map[string]bool{}, map[string]bool{}
	for _, f := range p.Folders {
		if !f.IsCompose() {
			h.RunCommands = append(h.RunCommands, f.Run)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(f.LocalPath, f.Compose))
		if err != nil {
			return h, errs.Wrap(errs.CodeInvalidInput, err, "cannot read "+f.Compose, "")
		}
		var c composeFile
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return h, errs.Wrap(errs.CodeInvalidInput, err, f.Compose+" is not valid YAML", "run `docker compose config` to find the mistake")
		}
		for _, svc := range c.Services {
			h.Services++
			if svc.Build != nil {
				ctxDir, dockerfile := buildPaths(svc.Build)
				for _, b := range fromLines(filepath.Join(f.LocalPath, ctxDir, dockerfile)) {
					bases[b] = true
				}
				continue
			}
			if svc.Image != "" {
				images[svc.Image] = true
			}
		}
	}
	h.Images, h.BaseImages = sorted(images), sorted(bases)
	return h, nil
}

func buildPaths(build any) (ctxDir, dockerfile string) {
	ctxDir, dockerfile = ".", "Dockerfile"
	switch b := build.(type) {
	case string:
		ctxDir = b
	case map[string]any:
		if c, ok := b["context"].(string); ok && c != "" {
			ctxDir = c
		}
		if d, ok := b["dockerfile"].(string); ok && d != "" {
			dockerfile = d
		}
	}
	return ctxDir, dockerfile
}

// fromLines returns the external base images of a Dockerfile. Stage names
// (FROM builder) and images built from variables are skipped: they are not
// registry images protolift can check.
func fromLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	stages := map[string]bool{}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		args := fields[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		img := args[0]
		if len(args) >= 3 && strings.EqualFold(args[1], "AS") {
			stages[strings.ToLower(args[2])] = true
		}
		if stages[strings.ToLower(img)] || strings.Contains(img, "$") || img == "scratch" {
			continue
		}
		out = append(out, img)
	}
	return out
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
