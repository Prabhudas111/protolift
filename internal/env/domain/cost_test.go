// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"math"
	"testing"
	"time"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestLedgerObservesRunsFromCloudTimes(t *testing.T) {
	var l Ledger
	price := Price{USD: 1, Known: true}
	box := Box{ID: "b1", EnvID: "e1", State: BoxRunning, StartedAt: t0, InstanceType: "x"}
	l.Observe(box, price, time.Time{}, t0.Add(time.Hour))
	l.Observe(box, price, time.Time{}, t0.Add(2*time.Hour)) // same run, no duplicate
	if len(l.Runs) != 1 {
		t.Fatalf("runs = %+v", l.Runs)
	}
	// The box stopped itself at 11:00 while the laptop was off; seen at 15:00.
	box.State = BoxStopped
	l.Observe(box, price, t0.Add(2*time.Hour), t0.Add(6*time.Hour))
	if !l.Runs[0].End.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("run must end at the cloud's stop time, got %v", l.Runs[0].End)
	}
	if got := l.Spend("e1", t0, t0.Add(24*time.Hour), t0.Add(6*time.Hour)); !near(got, 2) {
		t.Fatalf("spend = %v, want 2", got)
	}
	// Restarted later: a new run opens.
	box.State, box.StartedAt = BoxRunning, t0.Add(8*time.Hour)
	l.Observe(box, price, time.Time{}, t0.Add(9*time.Hour))
	if len(l.Runs) != 2 || !l.Runs[1].End.IsZero() {
		t.Fatalf("runs = %+v", l.Runs)
	}
}

func TestLedgerUnknownStopTimeOverCounts(t *testing.T) {
	l := Ledger{Runs: []Run{{EnvID: "e1", BoxID: "b1", HourlyUSD: 1, Start: t0}}}
	l.Observe(Box{ID: "b1", State: BoxStopped}, Price{}, time.Time{}, t0.Add(3*time.Hour))
	if !l.Runs[0].End.Equal(t0.Add(3 * time.Hour)) {
		t.Fatal("with no stop time, the run ends when the stop was seen (safe side)")
	}
}

func TestSpendClipsToPeriodAndEnv(t *testing.T) {
	l := Ledger{Runs: []Run{
		{EnvID: "e1", BoxID: "b1", HourlyUSD: 2, Start: t0.Add(-2 * time.Hour), End: t0.Add(time.Hour)}, // crosses 09:00
		{EnvID: "e2", BoxID: "b2", HourlyUSD: 1, Start: t0, End: t0.Add(time.Hour)},
	}}
	if got := l.Spend("e1", t0, t0.Add(time.Hour*24), t0.Add(5*time.Hour)); !near(got, 2) {
		t.Fatalf("clipped spend = %v, want 2", got)
	}
	if got := l.Spend("", t0, t0.Add(time.Hour*24), t0.Add(5*time.Hour)); !near(got, 3) {
		t.Fatalf("all-env spend = %v, want 3", got)
	}
}

func TestAllowTightestBudgetWins(t *testing.T) {
	price := Price{USD: 0.5, Known: true}
	a, err := Allow(Budget{DailyUSD: 2}, Budget{MonthlyUSD: 10}, Spent{Today: 1}, Spent{Month: 9.75}, price)
	if err != nil {
		t.Fatal(err)
	}
	// project: $1 left = 120 min; global: $0.25 left = 30 min.
	if a.Unlimited || a.Minutes != 30 || a.Limit != "your total monthly budget" {
		t.Fatalf("allowance = %+v", a)
	}
	if _, err := Allow(Budget{DailyUSD: 1}, Budget{}, Spent{Today: 1.01}, Spent{}, price); errs.CodeOf(err) != errs.CodeBudget {
		t.Fatalf("a used-up budget must refuse with BUDGET, got %v", err)
	}
	if a, _ := Allow(Budget{}, Budget{}, Spent{}, Spent{}, price); !a.Unlimited {
		t.Fatal("no budgets means no limit")
	}
	if err := (Budget{DailyUSD: -1}).Validate(); errs.CodeOf(err) != errs.CodeInvalidInput {
		t.Fatal("negative budget must be refused")
	}
}

func TestPeriodsAndPrune(t *testing.T) {
	day, month := Periods(time.Date(2026, 11, 5, 15, 4, 0, 0, time.UTC))
	if day.Day() != 5 || day.Hour() != 0 || month.Day() != 1 {
		t.Fatalf("day=%v month=%v", day, month)
	}
	l := Ledger{Runs: []Run{{BoxID: "old", Start: t0.AddDate(0, -3, 0), End: t0.AddDate(0, -3, 0).Add(time.Hour)}, {BoxID: "open", Start: t0}}}
	l.Prune(t0.AddDate(0, -2, 0))
	if len(l.Runs) != 1 || l.Runs[0].BoxID != "open" {
		t.Fatalf("prune = %+v", l.Runs)
	}
}
