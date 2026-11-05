// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package awscloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwt "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2t "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmt "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Box limits.
const (
	diskGB          = 60
	waitStart       = 10 * time.Minute
	waitStop        = 10 * time.Minute
	waitTerminate   = 10 * time.Minute
	waitSSM         = 10 * time.Minute
	pollSSM         = 5 * time.Second
	alarmPeriodSecs = 300
	alarmPeriods    = 12 // 12 × 5 min = one idle hour
	alarmCPUPercent = 5
)

// ListBoxes returns live boxes. envID "" means every protolift box.
func (c *Cloud) ListBoxes(ctx context.Context, envID string) ([]domain.Box, error) {
	f := []ec2t.Filter{
		{Name: aws.String("tag:" + TagManaged), Values: []string{"true"}},
		{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
	}
	if envID != "" {
		f = append(f, ec2t.Filter{Name: aws.String("tag:" + TagEnv), Values: []string{envID}})
	}
	var boxes []domain.Box
	p := ec2.NewDescribeInstancesPaginator(c.ec2, &ec2.DescribeInstancesInput{Filters: f})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return nil, translate(err, "list protolift boxes")
		}
		for _, r := range out.Reservations {
			for _, i := range r.Instances {
				boxes = append(boxes, c.toBox(i))
			}
		}
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	return boxes, nil
}

func (c *Cloud) toBox(i ec2t.Instance) domain.Box {
	b := domain.Box{
		ID: aws.ToString(i.InstanceId), State: domain.BoxState(i.State.Name), InstanceType: string(i.InstanceType),
		Arch: domain.ArchIntel, Region: c.region, StartedAt: aws.ToTime(i.LaunchTime),
	}
	if i.Architecture == ec2t.ArchitectureValuesArm64 {
		b.Arch = domain.ArchARM
	}
	b.StoppedAt = stopTime(aws.ToString(i.StateTransitionReason))
	for _, t := range i.Tags {
		switch aws.ToString(t.Key) {
		case "Name":
			b.Name = aws.ToString(t.Value)
		case TagEnv:
			b.EnvID = aws.ToString(t.Value)
		}
	}
	return b
}

// stopTime reads EC2's "User initiated (2026-11-05 10:00:00 GMT)" reason,
// which is also what an in-box shutdown reports. Zero when absent.
func stopTime(reason string) time.Time {
	open, close := strings.Index(reason, "("), strings.Index(reason, " GMT)")
	if open < 0 || close <= open {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02 15:04:05", reason[open+1:close])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// FindBox returns the env's box. Two boxes for one env is a CONFLICT the
// user must resolve, never something protolift guesses about.
func (c *Cloud) FindBox(ctx context.Context, envID string) (domain.Box, bool, error) {
	boxes, err := c.ListBoxes(ctx, envID)
	if err != nil || len(boxes) == 0 {
		return domain.Box{}, false, err
	}
	if len(boxes) > 1 {
		ids := make([]string, 0, len(boxes))
		for _, b := range boxes {
			ids = append(ids, b.ID)
		}
		return domain.Box{}, false, errs.New(errs.CodeConflict, fmt.Sprintf("found %d boxes for this project: %v", len(boxes), ids),
			"delete the extra ones in the EC2 console, or run `protolift down` and `protolift up`")
	}
	return boxes[0], true, nil
}

// CreateBox launches a box with no inbound rules, IMDSv2 only, an encrypted
// disk, and "stop" (not delete) when it shuts itself down.
func (c *Cloud) CreateBox(ctx context.Context, s ports.BoxSpec) (domain.Box, error) {
	net, err := c.network(ctx)
	if err != nil {
		return domain.Box{}, err
	}
	ami, err := c.ubuntuAMI(ctx, s.Arch)
	if err != nil {
		return domain.Box{}, err
	}
	subnet, err := c.subnetFor(ctx, net.vpc, s.InstanceType)
	if err != nil {
		return domain.Box{}, err
	}
	tags := map[string]string{"Name": "protolift-" + s.Name, TagEnv: s.EnvID, TagProject: s.Name}
	out, err := c.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String(ami), InstanceType: ec2t.InstanceType(s.InstanceType), MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		IamInstanceProfile: &ec2t.IamInstanceProfileSpecification{Name: aws.String(RoleName)},
		NetworkInterfaces: []ec2t.InstanceNetworkInterfaceSpecification{{
			DeviceIndex: aws.Int32(0), SubnetId: aws.String(subnet), Groups: []string{net.sg},
			AssociatePublicIpAddress: aws.Bool(true), DeleteOnTermination: aws.Bool(true),
		}},
		MetadataOptions: &ec2t.InstanceMetadataOptionsRequest{HttpTokens: ec2t.HttpTokensStateRequired, HttpEndpoint: ec2t.InstanceMetadataEndpointStateEnabled},
		BlockDeviceMappings: []ec2t.BlockDeviceMapping{{DeviceName: aws.String("/dev/sda1"), Ebs: &ec2t.EbsBlockDevice{
			VolumeSize: aws.Int32(diskGB), VolumeType: ec2t.VolumeTypeGp3, Encrypted: aws.Bool(true), DeleteOnTermination: aws.Bool(true),
		}}},
		InstanceInitiatedShutdownBehavior: ec2t.ShutdownBehaviorStop,
		UserData:                          aws.String(base64.StdEncoding.EncodeToString([]byte(s.BootScript))),
		TagSpecifications: []ec2t.TagSpecification{
			tagSpec(ec2t.ResourceTypeInstance, tags), tagSpec(ec2t.ResourceTypeVolume, tags), tagSpec(ec2t.ResourceTypeNetworkInterface, tags),
		},
	})
	if err != nil {
		return domain.Box{}, translate(err, "create the box")
	}
	b := c.toBox(out.Instances[0])
	b.Name, b.EnvID = tags["Name"], s.EnvID
	if err := c.putIdleAlarm(ctx, b); err != nil {
		return b, err
	}
	return b, nil
}

func (c *Cloud) ubuntuAMI(ctx context.Context, arch domain.Arch) (string, error) {
	a := "amd64"
	if arch == domain.ArchARM {
		a = "arm64"
	}
	out, err := c.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String("/aws/service/canonical/ubuntu/server/24.04/stable/current/" + a + "/hvm/ebs-gp3/ami-id"),
	})
	if err != nil {
		return "", translate(err, "find the Ubuntu 24.04 image")
	}
	return aws.ToString(out.Parameter.Value), nil
}

// subnetFor picks a subnet in a zone that offers the instance type.
func (c *Cloud) subnetFor(ctx context.Context, vpc, instanceType string) (string, error) {
	off, err := c.ec2.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: ec2t.LocationTypeAvailabilityZone,
		Filters:      []ec2t.Filter{{Name: aws.String("instance-type"), Values: []string{instanceType}}},
	})
	if err != nil {
		return "", translate(err, "check which zones offer "+instanceType)
	}
	offered := map[string]bool{}
	for _, o := range off.InstanceTypeOfferings {
		offered[aws.ToString(o.Location)] = true
	}
	subs, err := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: []ec2t.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}})
	if err != nil {
		return "", translate(err, "list subnets")
	}
	sort.Slice(subs.Subnets, func(i, j int) bool {
		return aws.ToString(subs.Subnets[i].AvailabilityZone) < aws.ToString(subs.Subnets[j].AvailabilityZone)
	})
	for _, s := range subs.Subnets {
		if offered[aws.ToString(s.AvailabilityZone)] && aws.ToInt32(s.AvailableIpAddressCount) > 0 {
			return aws.ToString(s.SubnetId), nil
		}
	}
	return "", errs.New(errs.CodeCapacity, "no zone in this network offers "+instanceType, "pick another --size or --arch")
}

func alarmName(envID string) string { return "protolift-" + envID + "-idle" }

// putIdleAlarm is the backstop that needs neither the box script nor the
// laptop: CPU under 5% for an hour stops the box.
func (c *Cloud) putIdleAlarm(ctx context.Context, b domain.Box) error {
	_, err := c.cw.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
		AlarmName:          aws.String(alarmName(b.EnvID)),
		AlarmDescription:   aws.String("protolift: stop the box after an idle hour"),
		Namespace:          aws.String("AWS/EC2"),
		MetricName:         aws.String("CPUUtilization"),
		Dimensions:         []cwt.Dimension{{Name: aws.String("InstanceId"), Value: aws.String(b.ID)}},
		Statistic:          cwt.StatisticMaximum,
		Period:             aws.Int32(alarmPeriodSecs),
		EvaluationPeriods:  aws.Int32(alarmPeriods),
		Threshold:          aws.Float64(alarmCPUPercent),
		ComparisonOperator: cwt.ComparisonOperatorLessThanThreshold,
		TreatMissingData:   aws.String("notBreaching"),
		AlarmActions:       []string{fmt.Sprintf("arn:%s:automate:%s:ec2:stop", c.partition(), c.region)},
		Tags:               []cwt.Tag{{Key: aws.String(TagManaged), Value: aws.String("true")}, {Key: aws.String(TagEnv), Value: aws.String(b.EnvID)}},
	})
	return translate(err, "create the idle-stop alarm")
}

// StartBox starts a stopped box. A running box is left alone.
func (c *Cloud) StartBox(ctx context.Context, b domain.Box) error {
	switch b.State {
	case domain.BoxRunning, domain.BoxPending:
		return nil
	case domain.BoxStopping:
		if err := ec2.NewInstanceStoppedWaiter(c.ec2).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{b.ID}}, waitStop); err != nil {
			return translate(err, "wait for the box to finish stopping")
		}
	}
	_, err := c.ec2.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{b.ID}})
	return translate(err, "start the box")
}

// StopBox stops the box, keeps its disk, and waits until it is stopped.
func (c *Cloud) StopBox(ctx context.Context, b domain.Box) error {
	if b.State == domain.BoxStopped {
		return nil
	}
	if _, err := c.ec2.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{b.ID}}); err != nil {
		return translate(err, "stop the box")
	}
	return translate(ec2.NewInstanceStoppedWaiter(c.ec2).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{b.ID}}, waitStop), "wait for the box to stop")
}

// DeleteBox terminates the box (its disk goes with it) and its alarm.
func (c *Cloud) DeleteBox(ctx context.Context, b domain.Box) error {
	if _, err := c.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{b.ID}}); err != nil && !isNotFound(err) {
		return translate(err, "delete the box")
	}
	if _, err := c.cw.DeleteAlarms(ctx, &cloudwatch.DeleteAlarmsInput{AlarmNames: []string{alarmName(b.EnvID)}}); err != nil && !isNotFound(err) {
		return translate(err, "delete the idle-stop alarm")
	}
	return translate(ec2.NewInstanceTerminatedWaiter(c.ec2).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{b.ID}}, waitTerminate), "wait for the box to be deleted")
}

// ResizeBox stops the box and changes its type. The disk is kept.
func (c *Cloud) ResizeBox(ctx context.Context, b domain.Box, instanceType string) error {
	if err := c.StopBox(ctx, b); err != nil {
		return err
	}
	_, err := c.ec2.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(b.ID), InstanceType: &ec2t.AttributeValue{Value: aws.String(instanceType)},
	})
	return translate(err, "change the box size")
}

// WaitReachable waits until the box runs and Systems Manager reaches it.
func (c *Cloud) WaitReachable(ctx context.Context, b domain.Box, log func(string)) error {
	if err := ec2.NewInstanceRunningWaiter(c.ec2).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{b.ID}}, waitStart); err != nil {
		return translate(err, "wait for the box to start")
	}
	log("box running. Waiting for the private tunnel")
	deadline := time.Now().Add(waitSSM)
	for time.Now().Before(deadline) {
		out, err := c.ssm.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
			Filters: []ssmt.InstanceInformationStringFilter{{Key: aws.String("InstanceIds"), Values: []string{b.ID}}},
		})
		if err != nil {
			return translate(err, "check the private tunnel")
		}
		if len(out.InstanceInformationList) > 0 && out.InstanceInformationList[0].PingStatus == ssmt.PingStatusOnline {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollSSM):
		}
	}
	return errs.New(errs.CodeTimeout, "the box runs, but AWS Systems Manager could not reach it within 10 minutes",
		"the box needs outbound HTTPS to AWS. Run `protolift init` again, then `protolift up`")
}
