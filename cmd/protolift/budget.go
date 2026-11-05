// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// setBudget is `protolift budget --daily N --monthly N [--all]`. A flag left
// out keeps its current value. 0 removes that cap.
func setBudget(ctx context.Context, m *env.Manager, t env.Target, o opts, w io.Writer) error {
	if o.daily < 0 && o.monthly < 0 {
		return errs.New(errs.CodeInvalidInput, "budget needs --daily and/or --monthly", "for example: protolift budget --daily 5 --monthly 60 (add --all for every project together)")
	}
	r, err := m.Cost(ctx)
	if err != nil {
		return err
	}
	b := r.Global
	if !o.all {
		cur, err := m.Status(ctx, t)
		if err != nil {
			return err
		}
		b = cur.Budget
	}
	if o.daily >= 0 {
		b.DailyUSD = o.daily
	}
	if o.monthly >= 0 {
		b.MonthlyUSD = o.monthly
	}
	if o.all {
		err = m.SetGlobalBudget(b)
	} else {
		err = m.SetBudget(t, b)
	}
	if err != nil {
		return err
	}
	scope := "this project"
	if o.all {
		scope = "all projects together"
	}
	fmt.Fprintf(w, "budget for %s: daily %s, monthly %s. It applies from the next `protolift up`.\n", scope, capText(b.DailyUSD), capText(b.MonthlyUSD))
	return nil
}
