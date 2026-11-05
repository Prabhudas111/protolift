// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package archtest enforces the architecture by machine: which layer may
// import which, and that no private name ever lands in this public repo.
package archtest

import (
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const module = "github.com/Prabhudas111/protolift"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test")
		}
		dir = parent
	}
}

// rule says what a package (by path prefix inside the module) may import
// from inside the module, and whether it may import third-party code.
type rule struct {
	prefix     string
	allowed    []string
	thirdParty bool
}

// rules, most specific first. Layers: domain → application → ports →
// adapters, with the manager facade on top and cmd as composition root.
var rules = []rule{
	{"internal/shared/", []string{"internal/shared/"}, false},
	{"internal/env/domain", []string{"internal/shared/"}, false},
	{"internal/env/ports", []string{"internal/env/domain", "internal/shared/"}, false},
	{"internal/env/application", []string{"internal/env/domain", "internal/env/ports", "internal/shared/"}, false},
	{"internal/env/adapters/", []string{"internal/env/domain", "internal/env/ports", "internal/shared/"}, true},
	{"internal/env", []string{"internal/env/application", "internal/env/domain", "internal/env/ports"}, false},
	// Driving adapters: other ways in besides the command line. They use only
	// the facade (and the shared error model), never the layers below it.
	{"internal/mcpserver", []string{"internal/env", "internal/shared/"}, true},
	{"internal/webui", []string{"internal/env", "internal/shared/"}, true},
	{"internal/archtest", nil, false},
	{"cmd/", []string{"internal/"}, true},
}

// matches: a name ending in "/" covers that whole folder; any other name is
// exactly one package. So "internal/env" never covers internal/env/adapters.
func matches(pkg, pattern string) bool {
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(pkg, pattern)
	}
	return pkg == pattern
}

func ruleFor(rel string) (rule, bool) {
	for _, r := range rules {
		if matches(rel, r.prefix) {
			return r, true
		}
	}
	return rule{}, false
}

func TestLayerImports(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		relFile, _ := filepath.Rel(root, p)
		rel := filepath.ToSlash(filepath.Dir(relFile))
		r, ok := ruleFor(rel)
		if !ok {
			t.Errorf("%s is in a folder with no layer rule; add one in arch_test.go", relFile)
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		ownAdapter := ""
		if strings.HasPrefix(rel, "internal/env/adapters/") {
			ownAdapter = strings.SplitN(strings.TrimPrefix(rel, "internal/env/adapters/"), "/", 2)[0]
		}
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			switch {
			case strings.HasPrefix(path, module+"/"):
				inner := strings.TrimPrefix(path, module+"/")
				if ownAdapter != "" && strings.HasPrefix(inner, "internal/env/adapters/"+ownAdapter) {
					continue
				}
				if !allowed(inner, r.allowed) {
					t.Errorf("%s imports %s, which its layer (%s) may not use", relFile, inner, r.prefix)
				}
			case strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
				if !r.thirdParty && !strings.HasSuffix(relFile, "_test.go") {
					t.Errorf("%s imports third-party %s; only adapters and cmd may", relFile, path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func allowed(inner string, patterns []string) bool {
	for _, p := range patterns {
		if matches(inner, p) {
			return true
		}
	}
	return false
}

// banned holds SHA-256 fingerprints of private names (companies, AWS
// accounts, profiles, hosts) that must never appear in this public repo.
// Only fingerprints are stored, so this file does not reveal the names.
var banned = map[string]bool{
	"3b5baa9a2779f5b8f23cfcc0bb6fbebee7349779524442124897b78fbc8c1786": true,
	"a5af4e3d5b21186e3e80ce3fe74536816bbdc1a4c60026f820bac6f16d603643": true,
	"46073ecab34b67b9204cdcb165a823b4fdf61701b6307794773777ae1bb84e6e": true,
	"bba881ebaae654de0ab71f7b2ce886eb9d2a56e4c8cfb1d2daca713ec8207457": true,
	"bd388a77efdca664bef99f2abaf8959a0e633f21f479be81ce7e2cccff8e6d2b": true,
	"57c75695f63c8901e558d92b9678b594de12add736eab5e0130a2b52ead0a953": true,
	"3b86bbc804a242c870f869fe8158633e342292bd7b69b71c8c0a1d6433b3537c": true,
	"963fbb94ae0305c0bda9940cb3eb98d28ac619f76ccb7d937c4050b10ad60bc2": true,
	"d1490e223974ecdc9c61a9c98ef179a374f7223ac64d64de1bd54f098bf26dbf": true,
	"5793249de895d53e9f04225e53277a11c004023ce395c7f64a9c2567409ec159": true,
}

var (
	word       = regexp.MustCompile(`[a-z0-9]+(?:-[a-z0-9]+)*`)
	accountID  = regexp.MustCompile(`\b\d{12}\b`)
	instanceID = regexp.MustCompile(`\bi-[0-9a-f]{17}\b`)
)

// Documented example ids, the only ones allowed in the repo.
const (
	placeholderAccount  = "123456789012"
	placeholderInstance = "i-0123456789abcdef0"
)

func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestNoPrivateNames(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil || isBinary(raw) {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		text := strings.ToLower(string(raw))
		for _, w := range word.FindAllString(text, -1) {
			parts := append([]string{w}, strings.Split(w, "-")...)
			for _, part := range parts {
				if banned[fingerprint(part)] {
					t.Errorf("%s contains a private name (fingerprint %s…); remove it", rel, fingerprint(part)[:8])
				}
			}
		}
		for _, m := range accountID.FindAllString(text, -1) {
			if m != placeholderAccount && !strings.HasSuffix(rel, "go.sum") {
				t.Errorf("%s contains what looks like an AWS account id (%s…); use %s in examples", rel, m[:4], placeholderAccount)
			}
		}
		for _, m := range instanceID.FindAllString(text, -1) {
			if m != placeholderInstance {
				t.Errorf("%s contains a real-looking instance id; use %s in examples", rel, placeholderInstance)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isBinary(b []byte) bool {
	n := min(len(b), 8000)
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}
