// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package awscloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2t "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/Prabhudas111/protolift/internal/env/domain"
)

// Provider is the --cloud name of this adapter.
const Provider = "aws"

// types maps each cloud-neutral size to EC2 types. General purpose up to L,
// compute for XL. No burstable (t) types: their CPU credits run out in builds.
var types = map[string]struct{ arm, intel string }{
	"S":  {"m7g.large", "m7i.large"},
	"M":  {"m7g.xlarge", "m7i.xlarge"},
	"L":  {"m7g.2xlarge", "m7i.2xlarge"},
	"XL": {"c7g.4xlarge", "c7i.4xlarge"},
}

// pricesUSD: On-Demand Linux per hour, read from the AWS Price List API on
// 2026-11-05. Shown as estimates; AWS bills the real amount.
var pricesUSD = map[string]map[string]float64{
	"ap-south-1":     {"m7g.large": 0.0583, "m7g.xlarge": 0.1166, "m7g.2xlarge": 0.2333, "c7g.4xlarge": 0.3926, "m7i.large": 0.10605, "m7i.xlarge": 0.2121, "m7i.2xlarge": 0.4242, "c7i.4xlarge": 0.714},
	"us-east-1":      {"m7g.large": 0.0816, "m7g.xlarge": 0.1632, "m7g.2xlarge": 0.3264, "c7g.4xlarge": 0.58, "m7i.large": 0.1008, "m7i.xlarge": 0.2016, "m7i.2xlarge": 0.4032, "c7i.4xlarge": 0.714},
	"us-west-2":      {"m7g.large": 0.0816, "m7g.xlarge": 0.1632, "m7g.2xlarge": 0.3264, "c7g.4xlarge": 0.58, "m7i.large": 0.1008, "m7i.xlarge": 0.2016, "m7i.2xlarge": 0.4032, "c7i.4xlarge": 0.714},
	"eu-west-1":      {"m7g.large": 0.091, "m7g.xlarge": 0.1819, "m7g.2xlarge": 0.3638, "c7g.4xlarge": 0.6202, "m7i.large": 0.11235, "m7i.xlarge": 0.2247, "m7i.2xlarge": 0.4494, "c7i.4xlarge": 0.76608},
	"eu-central-1":   {"m7g.large": 0.0978, "m7g.xlarge": 0.1955, "m7g.2xlarge": 0.391, "c7g.4xlarge": 0.6596, "m7i.large": 0.12075, "m7i.xlarge": 0.2415, "m7i.2xlarge": 0.483, "c7i.4xlarge": 0.8148},
	"ap-southeast-1": {"m7g.large": 0.102, "m7g.xlarge": 0.204, "m7g.2xlarge": 0.408, "c7g.4xlarge": 0.6664, "m7i.large": 0.126, "m7i.xlarge": 0.252, "m7i.2xlarge": 0.504, "c7i.4xlarge": 0.8232},
}

const pricingPage = "aws.amazon.com/ec2/pricing"

// Provider is the --cloud name.
func (c *Cloud) Provider() string { return Provider }

// InstanceType maps a size and chip to an EC2 type.
func (c *Cloud) InstanceType(s domain.Size, a domain.Arch) string {
	t := types[s.Name]
	if a == domain.ArchIntel {
		return t.intel
	}
	return t.arm
}

// SizeOf maps an EC2 type back to its size and chip.
func (c *Cloud) SizeOf(instanceType string) (domain.Size, domain.Arch, bool) {
	for _, s := range domain.Sizes {
		switch instanceType {
		case types[s.Name].arm:
			return s, domain.ArchARM, true
		case types[s.Name].intel:
			return s, domain.ArchIntel, true
		}
	}
	return domain.Size{}, "", false
}

// Price is the hourly estimate in this region.
func (c *Cloud) Price(instanceType string) domain.Price {
	p, ok := pricesUSD[c.region][instanceType]
	return domain.Price{USD: p, Known: ok, Where: pricingPage}
}

// Supports reports whether any zone in the region offers the type.
func (c *Cloud) Supports(ctx context.Context, instanceType string) (bool, error) {
	out, err := c.ec2.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: ec2t.LocationTypeAvailabilityZone,
		Filters:      []ec2t.Filter{{Name: aws.String("instance-type"), Values: []string{instanceType}}},
	})
	if err != nil {
		return false, translate(err, "check which box types "+c.region+" offers")
	}
	return len(out.InstanceTypeOfferings) > 0, nil
}

// HostName: ssh's HostName is the instance id; the proxy resolves it.
func (c *Cloud) HostName(b domain.Box) string { return b.ID }

// ProxyCommand runs protolift's own SSM proxy (see proxy.go).
func (c *Cloud) ProxyCommand(_ domain.Box, pubKeyPath string) string {
	cmd := fmt.Sprintf("%s __proxy aws --region %s", quote(c.self), c.region)
	if c.profile != "" {
		cmd += " --profile " + quote(c.profile)
	}
	return cmd + " --pubkey " + quote(pubKeyPath) + " %h %p"
}

// quote double-quotes one ProxyCommand word.
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
