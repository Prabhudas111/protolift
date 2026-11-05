// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// writeAtomic writes through a temp file and rename, owner-only.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write "+tmp, "")
	}
	if err := os.Rename(tmp, path); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot save "+path, "")
	}
	return nil
}

func (s *Store) ledgerPath() string { return filepath.Join(s.Root, "ledger.json") }

// LoadLedger reads the running-time record; none yet is an empty ledger.
func (s *Store) LoadLedger() (domain.Ledger, error) {
	var l domain.Ledger
	b, err := os.ReadFile(s.ledgerPath())
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, errs.Wrap(errs.CodeInternal, err, "cannot read the cost ledger", "")
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, errs.Wrap(errs.CodeInternal, err, "the cost ledger is damaged", "delete "+s.ledgerPath()+"; protolift starts a new one")
	}
	return l, nil
}

// SaveLedger writes the ledger atomically.
func (s *Store) SaveLedger(l domain.Ledger) error {
	if _, err := s.Dir(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the cost ledger", "")
	}
	return writeAtomic(s.ledgerPath(), b)
}

type globalFile struct {
	DailyUSD   float64 `toml:"daily_budget_usd"`
	MonthlyUSD float64 `toml:"monthly_budget_usd"`
}

func (s *Store) globalPath() string { return filepath.Join(s.Root, "config.toml") }

// GlobalBudget reads the caps that apply across every project.
func (s *Store) GlobalBudget() (domain.Budget, error) {
	var g globalFile
	if _, err := toml.DecodeFile(s.globalPath(), &g); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.Budget{}, errs.Wrap(errs.CodeInvalidInput, err, s.globalPath()+" has a mistake", "fix it, or set the budget again in `protolift ui`")
	}
	b := domain.Budget{DailyUSD: g.DailyUSD, MonthlyUSD: g.MonthlyUSD}
	return b, b.Validate()
}

// SaveGlobalBudget writes the total caps.
func (s *Store) SaveGlobalBudget(b domain.Budget) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if _, err := s.Dir(); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("# protolift: spending caps across every project on this laptop, in US dollars (0 = no cap).\n")
	if err := toml.NewEncoder(&buf).Encode(globalFile{DailyUSD: b.DailyUSD, MonthlyUSD: b.MonthlyUSD}); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the budget", "")
	}
	return writeAtomic(s.globalPath(), buf.Bytes())
}

// ListEnvs returns every saved env record, by name.
func (s *Store) ListEnvs() ([]ports.EnvRecord, error) {
	d, err := s.Dir("envs")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, err, "cannot list "+d, "")
	}
	var out []ports.EnvRecord
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, ok, err := s.LoadEnv(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// UpdateSettings changes a project's settings and keeps its folders.
func (s *Store) UpdateSettings(dir, name string, change func(*domain.Settings)) error {
	path, err := s.WriteSettingsTemplate(dir, name)
	if err != nil {
		return err
	}
	var sf settingsFile
	if _, err := toml.DecodeFile(path, &sf); err != nil {
		return errs.Wrap(errs.CodeInvalidInput, err, "your settings file has a mistake", "fix "+path)
	}
	st := domain.Settings{Cloud: sf.Cloud, Size: sf.Size, Arch: domain.Arch(sf.Arch), IdleMinutes: sf.IdleMinutes, MaxHours: sf.MaxHours,
		Budget: domain.Budget{DailyUSD: sf.DailyUSD, MonthlyUSD: sf.MonthlyUSD}}
	change(&st)
	if _, err := st.Normalize(); err != nil {
		return err
	}
	sf.Cloud, sf.Size, sf.Arch, sf.IdleMinutes, sf.MaxHours = st.Cloud, st.Size, string(st.Arch), st.IdleMinutes, st.MaxHours
	sf.DailyUSD, sf.MonthlyUSD = st.Budget.DailyUSD, st.Budget.MonthlyUSD
	if len(sf.Folders) == 0 {
		sf.Folders = []folderEntry{{Path: "."}}
	}
	var buf bytes.Buffer
	buf.WriteString("# protolift settings for " + dir + "\n# Kept outside your project. `protolift config` shows this path.\n")
	if err := toml.NewEncoder(&buf).Encode(sf); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the settings", "")
	}
	return writeAtomic(path, buf.Bytes())
}
