// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Prabhudas111/protolift/internal/env/adapters/awscloud"
	"github.com/Prabhudas111/protolift/internal/env/adapters/gcpcloud"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// login is who and where a cloud acts: AWS uses a profile, GCP a project.
type login struct {
	profile, project, region, self string
}

// provider is one pluggable cloud. To add a cloud, write its adapter under
// internal/env/adapters and add one entry here. Nothing else changes.
type provider struct {
	// open builds the adapter with the user's own login.
	open func(ctx context.Context, l login) (ports.Cloud, error)
	// tools checks the laptop has what this cloud's tunnel needs, before
	// anything is created or billed.
	tools func() error
	// hint is shown in help and errors.
	hint string
}

var providers = map[string]provider{
	awscloud.Provider: {
		open: func(ctx context.Context, l login) (ports.Cloud, error) {
			return awscloud.New(ctx, l.profile, l.region, l.self)
		},
		tools: func() error { _, err := awscloud.FindPlugin(); return err },
		hint:  "Amazon Web Services (login: aws configure sso)",
	},
	gcpcloud.Provider: {
		open: func(ctx context.Context, l login) (ports.Cloud, error) {
			return gcpcloud.New(ctx, l.project, l.region)
		},
		tools: func() error { _, err := gcpcloud.FindGcloud(); return err },
		hint:  "Google Cloud (login: gcloud auth login)",
	},
}

// defaultProvider is used when neither --cloud, the settings nor a saved
// env name one.
const defaultProvider = awscloud.Provider

func providerNames() string {
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func lookupProvider(name string) (provider, error) {
	p, ok := providers[strings.ToLower(name)]
	if !ok {
		return provider{}, errs.New(errs.CodeInvalidInput, fmt.Sprintf("cloud %q is not supported", name),
			"use --cloud with one of: "+providerNames())
	}
	return p, nil
}
