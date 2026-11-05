// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package gcpcloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// apis protolift needs: Compute Engine for boxes, IAP for the private tunnel.
var apis = []string{"compute.googleapis.com", "iap.googleapis.com"}

type firewall struct {
	Name    string `json:"name"`
	Network string `json:"network"`
}

// Init turns on the two APIs, picks a network (the project's default one,
// or a protolift network if there is none) and adds the only firewall rule:
// ssh from Google's IAP range to boxes tagged protolift. Safe to repeat.
func (c *Cloud) Init(ctx context.Context, log func(string)) error {
	if err := c.run(ctx, createTimeout, nil, append([]string{"services", "enable"}, apis...)...); err != nil {
		return err
	}
	log("Compute Engine and IAP APIs: on")
	if _, err := c.firewall(ctx); err == nil {
		log("firewall rule " + FirewallName + ": already there")
		return nil
	} else if !isCode(err, errs.CodeNotSetUp) {
		return err
	}
	network := "default"
	if err := c.run(ctx, callTimeout, nil, "compute", "networks", "describe", "default"); isCode(err, errs.CodeNotFound) {
		log("no default network: creating a small one")
		if err := c.run(ctx, createTimeout, nil, "compute", "networks", "create", OwnNetwork, "--subnet-mode=auto",
			"--description=protolift boxes"); err != nil {
			return err
		}
		network = OwnNetwork
	} else if err != nil {
		return err
	}
	log("network " + network + ": ready")
	if err := c.run(ctx, createTimeout, nil, "compute", "firewall-rules", "create", FirewallName,
		"--network", network, "--direction=INGRESS", "--action=ALLOW", "--rules=tcp:22",
		"--source-ranges="+iapRange, "--target-tags="+NetworkTag,
		"--description=protolift: ssh only through Google's private tunnel (IAP), nothing else"); err != nil {
		return err
	}
	log("firewall rule " + FirewallName + ": ssh from Google's IAP range only")
	return nil
}

// firewall returns protolift's rule, or NOT_SET_UP when it is missing.
func (c *Cloud) firewall(ctx context.Context) (firewall, error) {
	var fw firewall
	err := c.run(ctx, callTimeout, &fw, "compute", "firewall-rules", "describe", FirewallName)
	if isCode(err, errs.CodeNotFound) {
		return fw, errs.New(errs.CodeNotSetUp, "this Google Cloud project is not set up for protolift yet", "run `protolift init --cloud gcp` once")
	}
	return fw, err
}

// Ready returns NOT_SET_UP when Init has not been run.
func (c *Cloud) Ready(ctx context.Context) error {
	_, err := c.firewall(ctx)
	return err
}

// Uninstall removes what Init made. It refuses while any box exists. The
// APIs stay on: other things in the project may use them.
func (c *Cloud) Uninstall(ctx context.Context, log func(string)) error {
	boxes, err := c.ListBoxes(ctx, "")
	if err != nil {
		return err
	}
	if len(boxes) > 0 {
		names := make([]string, 0, len(boxes))
		for _, b := range boxes {
			names = append(names, b.Name)
		}
		return errs.New(errs.CodeConflict, fmt.Sprintf("%d box(es) still exist: %s", len(boxes), strings.Join(names, ", ")),
			"run `protolift down` in each project first (`protolift list` shows them)")
	}
	fw, err := c.firewall(ctx)
	if isCode(err, errs.CodeNotSetUp) {
		log("nothing to remove")
		return nil
	}
	if err != nil {
		return err
	}
	if err := c.run(ctx, createTimeout, nil, "compute", "firewall-rules", "delete", FirewallName); err != nil && !isCode(err, errs.CodeNotFound) {
		return err
	}
	log("firewall rule " + FirewallName + ": deleted")
	if lastPart(fw.Network) == OwnNetwork {
		if err := c.run(ctx, createTimeout, nil, "compute", "networks", "delete", OwnNetwork); err != nil && !isCode(err, errs.CodeNotFound) {
			return err
		}
		log("network " + OwnNetwork + ": deleted")
	}
	return nil
}
