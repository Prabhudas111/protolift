// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package awscloud implements ports.Cloud on AWS. It is the only package
// that imports the AWS SDK for boxes. Every resource it creates is tagged
// protolift:managed=true so `down` and `uninstall` can find all of them.
package awscloud

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Names and tags shared by every resource.
const (
	TagManaged = "protolift:managed"
	TagEnv     = "protolift:env"
	TagProject = "protolift:project"
	RoleName   = "protolift-box"
	SGName     = "protolift-box"
	VPCName    = "protolift"
)

// Network limits. A single AWS call never hangs: the HTTP client gives up
// after callTimeout and the SDK retries throttling and 5xx a capped number
// of times with backoff.
const (
	callTimeout  = 30 * time.Second
	dialTimeout  = 10 * time.Second
	maxAttempts  = 6
	maxRetryWait = 20 * time.Second
)

// Cloud implements ports.Cloud.
type Cloud struct {
	region, profile string
	ec2             *ec2.Client
	ssm             *ssm.Client
	iam             *iam.Client
	cw              *cloudwatch.Client
	eic             *ec2instanceconnect.Client
	sts             *sts.Client
	cfg             aws.Config
}

var _ ports.Cloud = (*Cloud)(nil)

// New loads the user's own AWS login (env vars, profile or SSO: the AWS CLI
// chain). protolift never stores credentials.
func New(ctx context.Context, profile, region string) (*Cloud, error) {
	httpClient := &http.Client{
		Timeout: callTimeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: dialTimeout,
			MaxIdleConnsPerHost: 8,
		},
	}
	opts := []func(*config.LoadOptions) error{
		config.WithHTTPClient(httpClient),
		config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.MaxAttempts = maxAttempts
				o.MaxBackoff = maxRetryWait
			})
		}),
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, errs.Wrap(errs.CodeAuthExpired, err, "cannot load your AWS login",
			"run `aws configure sso` (or `aws configure`), or pass --profile")
	}
	if cfg.Region == "" {
		return nil, errs.New(errs.CodeInvalidInput, "no AWS region is set",
			"pass --region (for example --region us-east-1), or set AWS_REGION")
	}
	return &Cloud{
		region: cfg.Region, profile: profile, cfg: cfg,
		ec2: ec2.NewFromConfig(cfg), ssm: ssm.NewFromConfig(cfg), iam: iam.NewFromConfig(cfg),
		cw: cloudwatch.NewFromConfig(cfg), eic: ec2instanceconnect.NewFromConfig(cfg), sts: sts.NewFromConfig(cfg),
	}, nil
}

// Region is the region every call goes to.
func (c *Cloud) Region() string { return c.region }

// Config exposes the loaded AWS config to sibling adapters (the SSH proxy).
func (c *Cloud) Config() aws.Config { return c.cfg }

// Account proves the login works and returns the account id.
func (c *Cloud) Account(ctx context.Context) (string, error) {
	out, err := c.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", translate(err, "check your AWS login")
	}
	return aws.ToString(out.Account), nil
}

func (c *Cloud) partition() string {
	switch {
	case strings.HasPrefix(c.region, "cn-"):
		return "aws-cn"
	case strings.HasPrefix(c.region, "us-gov-"):
		return "aws-us-gov"
	}
	return "aws"
}

// translate maps an AWS error to protolift's error model, with a fix.
func translate(err error, doing string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		var ne net.Error
		if errors.As(err, &ne) {
			return errs.Wrap(errs.CodeNetwork, err, "could not reach AWS to "+doing, "check your internet connection and try again")
		}
		return errs.Wrap(errs.CodeInternal, err, "could not "+doing, "")
	}
	code := ae.ErrorCode()
	msg := "could not " + doing
	switch {
	case code == "ExpiredToken" || code == "ExpiredTokenException" || code == "InvalidClientTokenId" || code == "UnrecognizedClientException" || code == "AuthFailure":
		return errs.Wrap(errs.CodeAuthExpired, err, msg+": your AWS login has expired or is not valid", "run `aws sso login` (or refresh your keys) and try again")
	case (strings.Contains(code, "AccessDenied") || strings.Contains(code, "UnauthorizedOperation")) && strings.Contains(ae.ErrorMessage(), "service control policy"):
		// An AWS Organizations rule above the account, not the user's own
		// permissions: adding IAM policies cannot fix it. Often it allows only
		// some box types or regions.
		return errs.Wrap(errs.CodeNotAllowed, err, msg+": a rule set by your AWS organization blocks it (a service control policy)",
			"ask your AWS admin which box types and regions are allowed; then try another --size or --region")
	case strings.Contains(code, "AccessDenied") || strings.Contains(code, "UnauthorizedOperation") || code == "Unauthorized":
		return errs.Wrap(errs.CodeNotAllowed, err, msg+": your AWS user is not allowed to", "attach the protolift policy: https://github.com/Prabhudas111/protolift/tree/main/docs/iam")
	case code == "VcpuLimitExceeded" || code == "InstanceLimitExceeded" || code == "MaxSpotInstanceCountExceeded":
		return errs.Wrap(errs.CodeQuota, err, msg+": your AWS account has reached its CPU limit", "use a smaller --size, or raise the limit in Service Quotas → EC2")
	case code == "InsufficientInstanceCapacity" || code == "Unsupported":
		return errs.Wrap(errs.CodeCapacity, err, msg+": AWS has no capacity for this box type right now", "try again in a few minutes, or pick another --size or --arch")
	case code == "RequestLimitExceeded" || code == "Throttling" || code == "ThrottlingException":
		return errs.Wrap(errs.CodeNetwork, err, msg+": AWS is slowing requests down", "wait a minute and try again")
	case isNotFound(err):
		return errs.Wrap(errs.CodeNotFound, err, msg+": it does not exist", "")
	case code == "IncorrectInstanceState" || code == "IncorrectState" || code == "DependencyViolation" || code == "DeleteConflict":
		return errs.Wrap(errs.CodeConflict, err, msg+": AWS says it is busy or still in use", "wait a minute and try again")
	}
	return errs.Wrap(errs.CodeInternal, err, msg+" ("+code+")", ae.ErrorMessage())
}

func isNotFound(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		c := ae.ErrorCode()
		return c == "NoSuchEntity" || strings.HasSuffix(c, ".NotFound") || strings.HasSuffix(c, "NotFound") || c == "ResourceNotFound" || c == "ResourceNotFoundException"
	}
	return false
}

func isExists(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		c := ae.ErrorCode()
		return c == "EntityAlreadyExists" || c == "InvalidGroup.Duplicate" || (c == "LimitExceeded" && strings.Contains(ae.ErrorMessage(), "already"))
	}
	return false
}
