// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package awscloud

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// sshDocument is the AWS-owned SSM document that opens a TCP stream to the
// box's own sshd. Nothing on the box listens on the network.
const sshDocument = "AWS-StartSSHSession"

// FindPlugin returns AWS's session-manager-plugin or TOOL_MISSING with the
// install link for this OS.
func FindPlugin() (string, error) {
	p, err := exec.LookPath("session-manager-plugin")
	if err == nil {
		return p, nil
	}
	fix := "install the AWS Session Manager plugin: https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"
	if runtime.GOOS == "darwin" {
		fix = "run `brew install --cask session-manager-plugin`, or see " + strings.TrimPrefix(fix, "install the AWS Session Manager plugin: ")
	}
	return "", errs.Wrap(errs.CodeToolMissing, err, "the AWS session-manager-plugin is not installed", fix)
}

// Proxy is the ssh ProxyCommand: it gives the box a 60-second public key,
// opens an SSM session to the box's sshd, and hands stdin/stdout to AWS's
// session-manager-plugin. ssh runs it once per connection.
func (c *Cloud) Proxy(ctx context.Context, instanceID, port, pubKeyPath string) error {
	plugin, err := FindPlugin()
	if err != nil {
		return err
	}
	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot read the ssh public key", "run `protolift up` again to recreate it")
	}
	if _, err := c.eic.SendSSHPublicKey(ctx, &ec2instanceconnect.SendSSHPublicKeyInput{
		InstanceId: aws.String(instanceID), InstanceOSUser: aws.String(domain.BoxUser), SSHPublicKey: aws.String(strings.TrimSpace(string(pub))),
	}); err != nil {
		return translate(err, "send the one-time key to the box")
	}
	in := &ssm.StartSessionInput{
		Target: aws.String(instanceID), DocumentName: aws.String(sshDocument),
		Parameters: map[string][]string{"portNumber": {port}},
	}
	out, err := c.ssm.StartSession(ctx, in)
	if err != nil {
		return translate(err, "open the private tunnel")
	}
	// The plugin's arguments are its documented contract with the AWS CLI:
	// session JSON, region, operation, profile, request JSON, endpoint.
	session, err := json.Marshal(map[string]string{
		"SessionId": aws.ToString(out.SessionId), "TokenValue": aws.ToString(out.TokenValue), "StreamUrl": aws.ToString(out.StreamUrl),
	})
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the session", "")
	}
	request, err := json.Marshal(map[string]any{"Target": instanceID, "DocumentName": sshDocument, "Parameters": in.Parameters})
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot encode the session request", "")
	}
	endpoint := "https://ssm." + c.region + ".amazonaws.com"
	if c.partition() == "aws-cn" {
		endpoint += ".cn"
	}
	cmd := exec.CommandContext(ctx, plugin, string(session), c.region, "StartSession", c.profile, string(request), endpoint)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return errs.Wrap(errs.CodeNetwork, err, "the private tunnel closed", "")
		}
		return errs.Wrap(errs.CodeToolMissing, err, "cannot run session-manager-plugin", "reinstall the AWS Session Manager plugin")
	}
	return nil
}
