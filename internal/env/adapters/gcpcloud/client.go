// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package gcpcloud implements ports.Cloud on Google Cloud. It drives the
// official gcloud CLI, so the user has one Google login, the same one IAP
// (Google's private tunnel) needs. The box has no service account and no
// open port except ssh from Google's IAP range (35.235.240.0/20).
package gcpcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Names shared by every resource. GCP labels allow only lowercase letters,
// digits, - and _, so the keys differ from the AWS tags.
const (
	Provider     = "gcp"
	LabelManaged = "protolift-managed"
	LabelEnv     = "protolift-env"
	LabelProject = "protolift-project"
	NetworkTag   = "protolift"
	FirewallName = "protolift-allow-iap-ssh"
	OwnNetwork   = "protolift"
	iapRange     = "35.235.240.0/20" // Google's documented IAP TCP forwarding range
)

// Limits for gcloud calls.
const (
	callTimeout   = 2 * time.Minute
	createTimeout = 10 * time.Minute
	pollEvery     = 5 * time.Second
	waitRunning   = 10 * time.Minute
)

// Cloud implements ports.Cloud with gcloud.
type Cloud struct {
	project, region string
	gcloud          string
	zones           map[string][]string // instance type → zones offering it, cached
}

var _ ports.Cloud = (*Cloud)(nil)

// FindGcloud returns the gcloud CLI or TOOL_MISSING with the install link.
func FindGcloud() (string, error) {
	p, err := exec.LookPath("gcloud")
	if err != nil {
		fix := "install the Google Cloud CLI: https://cloud.google.com/sdk/docs/install"
		if runtime.GOOS == "darwin" {
			fix = "run `brew install --cask google-cloud-sdk`, or see https://cloud.google.com/sdk/docs/install"
		}
		return "", errs.Wrap(errs.CodeToolMissing, err, "the gcloud CLI is not installed", fix)
	}
	return p, nil
}

// New uses the user's own gcloud login. project and region fall back to the
// gcloud defaults (`gcloud config set project / compute/region`).
func New(ctx context.Context, project, region string) (*Cloud, error) {
	bin, err := FindGcloud()
	if err != nil {
		return nil, err
	}
	c := &Cloud{project: project, region: region, gcloud: bin, zones: map[string][]string{}}
	if c.project == "" {
		if c.project, err = c.configValue(ctx, "core/project"); err != nil {
			return nil, err
		}
	}
	if c.region == "" {
		if c.region, err = c.configValue(ctx, "compute/region"); err != nil {
			return nil, err
		}
	}
	if c.project == "" {
		return nil, errs.New(errs.CodeInvalidInput, "no Google Cloud project is set", "pass --project my-project, or run `gcloud config set project my-project`")
	}
	if c.region == "" {
		return nil, errs.New(errs.CodeInvalidInput, "no Google Cloud region is set", "pass --region (for example --region us-central1), or run `gcloud config set compute/region us-central1`")
	}
	return c, nil
}

func (c *Cloud) configValue(ctx context.Context, key string) (string, error) {
	out, err := c.raw(ctx, callTimeout, "config", "get-value", key)
	return strings.TrimSpace(out), err
}

// Provider is the --cloud name.
func (c *Cloud) Provider() string { return Provider }

// Region is the region every box goes to.
func (c *Cloud) Region() string { return c.region }

// Project is the Google Cloud project every call uses.
func (c *Cloud) Project() string { return c.project }

// Account names the login and project.
func (c *Cloud) Account(ctx context.Context) (string, error) {
	acct, err := c.configValue(ctx, "core/account")
	if err != nil {
		return "", err
	}
	if acct == "" {
		return "", errs.New(errs.CodeAuthExpired, "gcloud is not logged in", "run `gcloud auth login`")
	}
	return "project " + c.project + " (as " + acct + ")", nil
}

// raw runs gcloud with no project or format flags (config commands).
func (c *Cloud) raw(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.gcloud, append(args, "--quiet", "--verbosity=error")...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errs.Wrap(errs.CodeTimeout, ctx.Err(), "gcloud "+strings.Join(args[:min(3, len(args))], " ")+" took longer than "+timeout.String(), "check your internet connection")
		}
		return "", translate(err, stderr.String(), strings.Join(args[:min(3, len(args))], " "))
	}
	return out.String(), nil
}

// run runs a gcloud command in the project and decodes its JSON output.
func (c *Cloud) run(ctx context.Context, timeout time.Duration, into any, args ...string) error {
	args = append(args, "--project", c.project, "--format=json")
	out, err := c.raw(ctx, timeout, args...)
	if err != nil || into == nil {
		return err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = "null"
	}
	if err := json.Unmarshal([]byte(out), into); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot read gcloud's answer", "update the Google Cloud CLI (`gcloud components update`)")
	}
	return nil
}

// translate maps gcloud's error text to protolift's error model.
func translate(err error, stderr, doing string) error {
	msg := strings.TrimSpace(stderr)
	l := strings.ToLower(msg)
	what := "gcloud could not " + doing
	switch {
	case strings.Contains(l, "reauthentication") || strings.Contains(l, "gcloud auth login") || strings.Contains(l, "invalid_grant") || strings.Contains(l, "no credentialed accounts"):
		return errs.Wrap(errs.CodeAuthExpired, err, "your Google Cloud login has expired", "run `gcloud auth login` and try again")
	case strings.Contains(l, "api has not been used") || strings.Contains(l, "service_disabled") || strings.Contains(l, "is not enabled"):
		return errs.Wrap(errs.CodeNotSetUp, err, "a Google Cloud API protolift needs is turned off in this project", "run `protolift init --cloud gcp` (it turns on Compute Engine and IAP)")
	case strings.Contains(l, "constraints/") || strings.Contains(l, "organization policy"):
		return errs.Wrap(errs.CodeNotAllowed, err, what+": a rule set by your Google Cloud organization blocks it (an organization policy)",
			"ask your Google Cloud admin which machine types and regions are allowed; then try another --size or --region")
	case strings.Contains(l, "permission") || strings.Contains(l, "forbidden") || strings.Contains(l, "403"):
		return errs.Wrap(errs.CodeNotAllowed, err, what+": your Google account is not allowed to",
			"give it the roles in https://github.com/Prabhudas111/protolift/tree/main/docs/iam (gcp.md)")
	case strings.Contains(l, "quota"):
		return errs.Wrap(errs.CodeQuota, err, what+": your project has reached a quota", "use a smaller --size, or raise the quota in IAM & Admin → Quotas")
	case strings.Contains(l, "resource_pool_exhausted") || strings.Contains(l, "does not have enough resources") || strings.Contains(l, "stockout"):
		return errs.Wrap(errs.CodeCapacity, err, what+": Google has no capacity for this machine type in this zone right now", "try again later, or another --size or --region")
	case strings.Contains(l, "was not found") || strings.Contains(l, "notfound") || strings.Contains(l, "not found"):
		return errs.Wrap(errs.CodeNotFound, err, what+": it does not exist", "")
	case strings.Contains(l, "already exists"):
		return errs.Wrap(errs.CodeConflict, err, what+": it already exists", "")
	case strings.Contains(l, "unable to connect") || strings.Contains(l, "connection") || strings.Contains(l, "timed out"):
		return errs.Wrap(errs.CodeNetwork, err, what+": could not reach Google Cloud", "check your internet connection and try again")
	}
	if msg == "" {
		msg = err.Error()
	}
	return errs.Wrap(errs.CodeInternal, err, what+": "+msg, "run again with --debug")
}

func isCode(err error, c errs.Code) bool { return errs.CodeOf(err) == c }
