// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Prabhudas111/protolift/internal/env"
)

func printStatus(w io.Writer, r env.StatusReport) {
	fmt.Fprintf(w, "project  %s\n", r.Project)
	if r.Box == nil {
		fmt.Fprintln(w, "box      none yet; run `protolift up`")
		return
	}
	b := r.Box
	fmt.Fprintf(w, "box      %s · %s · %s\n", b.Name, b.State, b.InstanceType)
	if r.Size != "" {
		fmt.Fprintf(w, "size     %s · %s\n", r.Size, r.Price)
	}
	if r.CostSoFar != "" {
		fmt.Fprintf(w, "cost     %s\n", r.CostSoFar)
	}
	if len(r.Ports) > 0 {
		ps := make([]string, len(r.Ports))
		for i, p := range r.Ports {
			ps[i] = "localhost:" + strconv.Itoa(p)
		}
		fmt.Fprintf(w, "ports    %s\n", strings.Join(ps, ", "))
	}
	for i, s := range r.Sync {
		label := "sync    "
		if i > 0 {
			label = "        "
		}
		fmt.Fprintf(w, "%s %s\n", label, s)
	}
	if r.PeakGB > 0 {
		fmt.Fprintf(w, "memory   peak %.1f GB\n", r.PeakGB)
	}
	if r.Advice != "" {
		fmt.Fprintf(w, "tip      %s\n", r.Advice)
	}
}

func printList(w io.Writer, boxes []env.Box) {
	if len(boxes) == 0 {
		fmt.Fprintln(w, "no protolift boxes in this region")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tTYPE\tSTARTED")
	for _, b := range boxes {
		started := "-"
		if !b.StartedAt.IsZero() {
			started = b.StartedAt.Local().Format(time.DateTime)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", b.Name, b.State, b.InstanceType, started)
	}
	tw.Flush()
}
