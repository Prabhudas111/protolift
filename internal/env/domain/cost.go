// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Budget caps estimated spend. Zero means no cap for that period.
type Budget struct {
	DailyUSD   float64 `json:"dailyUsd"`
	MonthlyUSD float64 `json:"monthlyUsd"`
}

// Validate refuses negative or absurd caps.
func (b Budget) Validate() error {
	for _, v := range []float64{b.DailyUSD, b.MonthlyUSD} {
		if v < 0 || v > 100000 || math.IsNaN(v) {
			return errs.New(errs.CodeInvalidInput, fmt.Sprintf("budget %.2f is not valid", v), "use a dollar amount from 0 (no cap) to 100000")
		}
	}
	return nil
}

// Run is one stretch of time a box was running, at its hourly price.
type Run struct {
	EnvID     string    `json:"envId"`
	BoxID     string    `json:"boxId"`
	Type      string    `json:"type"`
	HourlyUSD float64   `json:"hourlyUsd"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end,omitempty"` // zero while the box runs
}

// Ledger is the record of box running time, kept on the laptop. Each run is
// learned from the cloud's own start and stop times, so a box that stopped
// itself while the laptop was off is still counted correctly.
type Ledger struct {
	Runs []Run `json:"runs"`
}

// Observe updates the ledger from what the cloud reports now. A running box
// opens (or keeps) the run that began at its last start. A stopped box
// closes its open run at stoppedAt, or at now when the cloud does not say
// (which can only over-count, the safe side for a budget).
func (l *Ledger) Observe(b Box, price Price, stoppedAt, now time.Time) {
	open := -1
	for i := range l.Runs {
		r := &l.Runs[i]
		if r.BoxID == b.ID && r.End.IsZero() {
			open = i
		}
	}
	switch b.State {
	case BoxRunning, BoxPending:
		if b.StartedAt.IsZero() {
			return
		}
		if open >= 0 && l.Runs[open].Start.Equal(b.StartedAt) {
			return
		}
		if open >= 0 { // a newer start: the earlier run ended before it
			l.Runs[open].End = minTime(b.StartedAt, now)
		}
		if price.Known {
			l.Runs = append(l.Runs, Run{EnvID: b.EnvID, BoxID: b.ID, Type: b.InstanceType, HourlyUSD: price.USD, Start: b.StartedAt})
		}
	default:
		if open < 0 {
			return
		}
		end := now
		if !stoppedAt.IsZero() && stoppedAt.After(l.Runs[open].Start) && stoppedAt.Before(now) {
			end = stoppedAt
		}
		l.Runs[open].End = end
	}
}

// CloseBox ends a box's open run (the box was deleted or stopped by us).
func (l *Ledger) CloseBox(boxID string, at time.Time) {
	for i := range l.Runs {
		if l.Runs[i].BoxID == boxID && l.Runs[i].End.IsZero() {
			l.Runs[i].End = at
		}
	}
}

// Spend sums the cost of runs inside [from, to). envID "" means every env.
func (l Ledger) Spend(envID string, from, to, now time.Time) float64 {
	total := 0.0
	for _, r := range l.Runs {
		if envID != "" && r.EnvID != envID {
			continue
		}
		end := r.End
		if end.IsZero() {
			end = now
		}
		s, e := maxTime(r.Start, from), minTime(end, to)
		if e.After(s) {
			total += r.HourlyUSD * e.Sub(s).Hours()
		}
	}
	return total
}

// Prune drops runs that ended before cutoff, so the file stays small.
func (l *Ledger) Prune(cutoff time.Time) {
	keep := l.Runs[:0]
	for _, r := range l.Runs {
		if r.End.IsZero() || r.End.After(cutoff) {
			keep = append(keep, r)
		}
	}
	l.Runs = keep
	sort.Slice(l.Runs, func(i, j int) bool { return l.Runs[i].Start.Before(l.Runs[j].Start) })
}

// Periods returns the start of today and of this month in the local zone.
func Periods(now time.Time) (day, month time.Time) {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
}

// Spent is what has been spent against one budget.
type Spent struct{ Today, Month float64 }

// Allowance is how long a box may run before a budget runs out.
type Allowance struct {
	Unlimited bool
	Minutes   int    // minutes left at the box's price, when not unlimited
	Limit     string // which budget is the tightest, for messages
}

// Allow combines the project and global budgets: the tightest wins.
// It returns BUDGET when either is already used up.
func Allow(project, global Budget, projSpent, allSpent Spent, price Price) (Allowance, error) {
	type cap struct {
		name       string
		limit, use float64
	}
	caps := []cap{
		{"this project's daily budget", project.DailyUSD, projSpent.Today},
		{"this project's monthly budget", project.MonthlyUSD, projSpent.Month},
		{"your total daily budget", global.DailyUSD, allSpent.Today},
		{"your total monthly budget", global.MonthlyUSD, allSpent.Month},
	}
	a := Allowance{Unlimited: true}
	for _, c := range caps {
		if c.limit <= 0 {
			continue
		}
		left := c.limit - c.use
		if left <= 0 {
			return Allowance{}, errs.New(errs.CodeBudget,
				fmt.Sprintf("%s ($%.2f) is used up: $%.2f spent", c.name, c.limit, c.use),
				"raise it with `protolift ui` or `protolift config`, or wait for the next day or month")
		}
		if !price.Known || price.USD <= 0 {
			continue
		}
		mins := int(left / price.USD * 60)
		if a.Unlimited || mins < a.Minutes {
			a = Allowance{Minutes: mins, Limit: c.name}
		}
	}
	return a, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
