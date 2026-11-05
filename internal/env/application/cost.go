// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package application

import (
	"context"
	"fmt"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
)

// ledgerKeep is how long finished runs are kept (a full year of months).
const ledgerKeep = 13 * 30 * 24 * time.Hour

// observe records what the cloud reports about boxes in the ledger.
func (s *Service) observe(boxes ...domain.Box) error {
	l, err := s.d.Store.LoadLedger()
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	for _, b := range boxes {
		l.Observe(b, s.d.Cloud.Price(b.InstanceType), b.StoppedAt, now)
	}
	l.Prune(now.Add(-ledgerKeep))
	return s.d.Store.SaveLedger(l)
}

// closeRun ends a box's run now (we stopped or deleted it).
func (s *Service) closeRun(b domain.Box) error {
	l, err := s.d.Store.LoadLedger()
	if err != nil {
		return err
	}
	l.CloseBox(b.ID, s.d.Clock.Now())
	return s.d.Store.SaveLedger(l)
}

func (s *Service) spent(l domain.Ledger, envID string) domain.Spent {
	now := s.d.Clock.Now()
	day, month := domain.Periods(now)
	return domain.Spent{Today: l.Spend(envID, day, now, now), Month: l.Spend(envID, month, now, now)}
}

// allowance says how long a box at price may run under every budget, or
// BUDGET when one is used up. Nothing is started or created after a BUDGET.
func (s *Service) allowance(p domain.Project, price domain.Price) (domain.Allowance, error) {
	l, err := s.d.Store.LoadLedger()
	if err != nil {
		return domain.Allowance{}, err
	}
	global, err := s.d.Store.GlobalBudget()
	if err != nil {
		return domain.Allowance{}, err
	}
	a, err := domain.Allow(p.Settings.Budget, global, s.spent(l, p.ID), s.spent(l, ""), price)
	if err != nil {
		return a, err
	}
	hasBudget := p.Settings.Budget != (domain.Budget{}) || global != (domain.Budget{})
	if hasBudget && !price.Known {
		s.d.UI.Warn("the budget cannot stop this box: protolift has no price for its type here (" + price.Text() + "); idle stop and max_hours still apply")
	}
	return a, nil
}

// budgetMinutes is what the box is told: minutes since its start at which
// the budget runs out (0 = no budget stop).
func (s *Service) budgetMinutes(a domain.Allowance, b domain.Box) int {
	if a.Unlimited {
		return 0
	}
	elapsed := 0
	if !b.StartedAt.IsZero() {
		elapsed = int(s.d.Clock.Now().Sub(b.StartedAt).Minutes())
	}
	return max(elapsed+a.Minutes, 1)
}

// CostLine is one project in a cost report.
type CostLine struct {
	Name     string        `json:"name"`
	Dir      string        `json:"dir"`
	EnvName  string        `json:"envName,omitempty"`
	Provider string        `json:"cloud"`
	Region   string        `json:"region"`
	Today    float64       `json:"todayUsd"`
	Month    float64       `json:"monthUsd"`
	Budget   domain.Budget `json:"budget"`
	Idle     int           `json:"idleMinutes"`
	MaxHours int           `json:"maxHours"`
}

// CostReport is what `protolift cost` and the local screen show.
type CostReport struct {
	Lines  []CostLine    `json:"projects"`
	Today  float64       `json:"todayUsd"`
	Month  float64       `json:"monthUsd"`
	Global domain.Budget `json:"totalBudget"`
	Note   string        `json:"note"`
}

// Cost refreshes what this cloud reports, then totals the ledger for every
// project on this laptop, in every cloud.
func (s *Service) Cost(ctx context.Context) (CostReport, error) {
	boxes, err := s.d.Cloud.ListBoxes(ctx, "")
	if err != nil {
		return CostReport{}, err
	}
	if err := s.observe(boxes...); err != nil {
		return CostReport{}, err
	}
	l, err := s.d.Store.LoadLedger()
	if err != nil {
		return CostReport{}, err
	}
	envs, err := s.d.Store.ListEnvs()
	if err != nil {
		return CostReport{}, err
	}
	global, err := s.d.Store.GlobalBudget()
	if err != nil {
		return CostReport{}, err
	}
	r := CostReport{Global: global, Note: "Estimates from running time × list price; your cloud bill is the real amount."}
	for _, e := range envs {
		sp := s.spent(l, e.ID)
		line := CostLine{Name: e.Name, Dir: e.Dir, EnvName: e.EnvName, Provider: e.Provider, Region: e.Region, Today: sp.Today, Month: sp.Month}
		if e.Dir != "" {
			if p, err := s.d.Store.LoadProject(ctx, e.Dir, e.EnvName); err == nil {
				line.Budget, line.Idle, line.MaxHours = p.Settings.Budget, p.Settings.IdleMinutes, p.Settings.MaxHours
			}
		}
		r.Lines = append(r.Lines, line)
	}
	all := s.spent(l, "")
	r.Today, r.Month = all.Today, all.Month
	return r, nil
}

// SetBudget changes one project's caps.
func (s *Service) SetBudget(t Target, b domain.Budget) error {
	if err := b.Validate(); err != nil {
		return err
	}
	return s.d.Store.UpdateSettings(t.Dir, t.Name, func(st *domain.Settings) { st.Budget = b })
}

// SetGlobalBudget changes the caps across every project.
func (s *Service) SetGlobalBudget(b domain.Budget) error { return s.d.Store.SaveGlobalBudget(b) }

// SetStops changes when the box stops itself. The new values reach a running
// box on the next `up`.
func (s *Service) SetStops(t Target, idleMinutes, maxHours int) error {
	if _, err := (domain.Settings{IdleMinutes: idleMinutes, MaxHours: maxHours}).Normalize(); err != nil {
		return err
	}
	return s.d.Store.UpdateSettings(t.Dir, t.Name, func(st *domain.Settings) {
		st.IdleMinutes, st.MaxHours = idleMinutes, maxHours
	})
}

// budgetNote tells the user when the budget will stop the box.
func budgetNote(a domain.Allowance) string {
	if a.Unlimited {
		return ""
	}
	return fmt.Sprintf("budget: the box stops itself in about %d min (%s)", a.Minutes, a.Limit)
}
