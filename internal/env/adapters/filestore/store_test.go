// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectComposeAndHints(t *testing.T) {
	proj := t.TempDir()
	write(t, filepath.Join(proj, "compose.yaml"), `
services:
  db: {image: "postgres:16"}
  web:
    build: {context: ./web, dockerfile: Dockerfile.dev}
`)
	write(t, filepath.Join(proj, "web", "Dockerfile.dev"), "FROM --platform=$BUILDPLATFORM node:22 AS deps\nFROM deps\nFROM nginx:1.27\n")
	s := &Store{Root: t.TempDir()}
	p, err := s.LoadProject(context.Background(), proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Folders) != 1 || p.Folders[0].Compose != "compose.yaml" || p.Folders[0].RemotePath == "" {
		t.Fatalf("folders = %+v", p.Folders)
	}
	h, err := s.Hints(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Images) != 1 || h.Images[0] != "postgres:16" || h.Services != 2 {
		t.Fatalf("hints = %+v", h)
	}
	if len(h.BaseImages) != 2 || h.BaseImages[0] != "nginx:1.27" || h.BaseImages[1] != "node:22" {
		t.Fatalf("base images = %+v (stage name must be skipped)", h.BaseImages)
	}
}

func TestSettingsMultiFolderAndMistakes(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	api := filepath.Join(root, "api")
	write(t, filepath.Join(app, "package.json"), "{}")
	write(t, filepath.Join(api, "go.mod"), "module x")
	s := &Store{Root: t.TempDir()}
	path, err := s.WriteSettingsTemplate(app, "")
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "idle_minutes = 45\n[[folder]]\npath = \".\"\nrun = \"npm run dev\"\nports = [3000]\n[[folder]]\npath = \"../api\"\nrun = \"make dev\"\nports = [8080]\n")
	p, err := s.LoadProject(context.Background(), app, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Folders) != 2 || p.Folders[1].RemotePath != "/work/api" || p.Settings.IdleMinutes != 45 {
		t.Fatalf("project = %+v", p)
	}
	write(t, path, "size = \"HUGE\"\n")
	if _, err := s.LoadProject(context.Background(), app, ""); errs.CodeOf(err) != errs.CodeInvalidInput {
		t.Fatalf("bad size must be INVALID_INPUT, got %v", err)
	}
	write(t, path, "[[folder]]\npath = \"../missing\"\n")
	if _, err := s.LoadProject(context.Background(), app, ""); errs.CodeOf(err) != errs.CodeInvalidInput {
		t.Fatalf("missing folder must be INVALID_INPUT, got %v", err)
	}
}

func TestEnvFilesSkipExamples(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".env"), "A=1")
	write(t, filepath.Join(dir, ".env.local"), "B=2")
	write(t, filepath.Join(dir, ".env.example"), "A=")
	write(t, filepath.Join(dir, "sub", ".env"), "C=3")
	s := &Store{Root: t.TempDir()}
	files, err := s.EnvFiles(domainFolder(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || string(files[".env"]) != "A=1" {
		t.Fatalf("files = %v", files)
	}
}

func TestEnvRecordRoundTrip(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if _, ok, err := s.LoadEnv("abc"); ok || err != nil {
		t.Fatalf("missing record: ok=%v err=%v", ok, err)
	}
	r := ports.EnvRecord{ID: "abc", Name: "app", Ports: []int{8080, 3000}}
	if err := s.SaveEnv(r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LoadEnv("abc")
	if err != nil || !ok || got.Ports[0] != 3000 {
		t.Fatalf("got %+v %v %v", got, ok, err)
	}
	if err := s.RemoveEnv("abc"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LoadEnv("abc"); ok {
		t.Fatal("record must be gone")
	}
}

func domainFolder(dir string) domain.Folder { return domain.Folder{LocalPath: dir} }
