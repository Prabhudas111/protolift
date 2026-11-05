// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package awscloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2t "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamt "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

const trustPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

// iamSettle is how long a new instance profile needs before EC2 can use it
// (IAM is eventually consistent).
const iamSettle = 10 * time.Second

type network struct{ vpc, sg string }

func tagSpec(rt ec2t.ResourceType, extra map[string]string) ec2t.TagSpecification {
	tags := []ec2t.Tag{{Key: aws.String(TagManaged), Value: aws.String("true")}}
	for k, v := range extra {
		tags = append(tags, ec2t.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return ec2t.TagSpecification{ResourceType: rt, Tags: tags}
}

func (c *Cloud) ssmPolicyARN() string {
	return fmt.Sprintf("arn:%s:iam::aws:policy/AmazonSSMManagedInstanceCore", c.partition())
}

// Init creates what is missing: access role, instance profile, network (only
// when the region has no default one) and a firewall group with no inbound
// rules. Every step checks first, so running it twice changes nothing.
func (c *Cloud) Init(ctx context.Context, log func(string)) error {
	if err := c.ensureRole(ctx, log); err != nil {
		return err
	}
	_, err := c.ensureNetwork(ctx, log)
	return err
}

// Ready returns NOT_SET_UP when Init has not been run in this region.
func (c *Cloud) Ready(ctx context.Context) error {
	if _, err := c.iam.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(RoleName)}); err != nil {
		if isNotFound(err) {
			return errs.New(errs.CodeNotSetUp, "this AWS account is not set up for protolift yet", "run `protolift init` once")
		}
		return translate(err, "check the protolift access role")
	}
	_, err := c.network(ctx)
	return err
}

func (c *Cloud) ensureRole(ctx context.Context, log func(string)) error {
	_, err := c.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(RoleName)})
	switch {
	case err == nil:
		log("access role " + RoleName + ": already there")
	case isNotFound(err):
		_, err = c.iam.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName: aws.String(RoleName), AssumeRolePolicyDocument: aws.String(trustPolicy),
			Description: aws.String("protolift box: lets AWS Systems Manager reach the box. No other access."),
			Tags:        []iamt.Tag{{Key: aws.String(TagManaged), Value: aws.String("true")}},
		})
		if err != nil && !isExists(err) {
			return translate(err, "create the access role")
		}
		log("access role " + RoleName + ": created")
	default:
		return translate(err, "check the access role")
	}
	if _, err := c.iam.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{RoleName: aws.String(RoleName), PolicyArn: aws.String(c.ssmPolicyARN())}); err != nil {
		return translate(err, "attach the Systems Manager policy")
	}
	ip, err := c.iam.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(RoleName)})
	if isNotFound(err) {
		if _, err := c.iam.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: aws.String(RoleName), Tags: []iamt.Tag{{Key: aws.String(TagManaged), Value: aws.String("true")}},
		}); err != nil && !isExists(err) {
			return translate(err, "create the instance profile")
		}
		ip, err = c.iam.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(RoleName)})
	}
	if err != nil {
		return translate(err, "read the instance profile")
	}
	for _, r := range ip.InstanceProfile.Roles {
		if aws.ToString(r.RoleName) == RoleName {
			log("instance profile " + RoleName + ": ready")
			return nil
		}
	}
	if _, err := c.iam.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{InstanceProfileName: aws.String(RoleName), RoleName: aws.String(RoleName)}); err != nil && !isExists(err) {
		return translate(err, "add the role to the instance profile")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(iamSettle):
	}
	log("instance profile " + RoleName + ": ready")
	return nil
}

// network finds the network Init chose, without creating anything.
func (c *Cloud) network(ctx context.Context) (network, error) {
	vpc, _, err := c.findVPC(ctx)
	if err != nil {
		return network{}, err
	}
	if vpc == "" {
		return network{}, errs.New(errs.CodeNotSetUp, "protolift has no network in "+c.region, "run `protolift init` once")
	}
	sg, err := c.findSG(ctx, vpc)
	if err != nil {
		return network{}, err
	}
	if sg == "" {
		return network{}, errs.New(errs.CodeNotSetUp, "the protolift firewall group is missing", "run `protolift init` again (it is safe to repeat)")
	}
	return network{vpc: vpc, sg: sg}, nil
}

func (c *Cloud) findVPC(ctx context.Context) (vpc string, managed bool, err error) {
	out, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: []ec2t.Filter{{Name: aws.String("tag:" + TagManaged), Values: []string{"true"}}}})
	if err != nil {
		return "", false, translate(err, "look for the protolift network")
	}
	if len(out.Vpcs) > 0 {
		return aws.ToString(out.Vpcs[0].VpcId), true, nil
	}
	def, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: []ec2t.Filter{{Name: aws.String("is-default"), Values: []string{"true"}}}})
	if err != nil {
		return "", false, translate(err, "look for the default network")
	}
	if len(def.Vpcs) > 0 {
		return aws.ToString(def.Vpcs[0].VpcId), false, nil
	}
	return "", false, nil
}

func (c *Cloud) findSG(ctx context.Context, vpc string) (string, error) {
	out, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: []ec2t.Filter{
		{Name: aws.String("vpc-id"), Values: []string{vpc}}, {Name: aws.String("group-name"), Values: []string{SGName}},
	}})
	if err != nil {
		return "", translate(err, "look for the protolift firewall group")
	}
	if len(out.SecurityGroups) > 0 {
		return aws.ToString(out.SecurityGroups[0].GroupId), nil
	}
	return "", nil
}

func (c *Cloud) ensureNetwork(ctx context.Context, log func(string)) (network, error) {
	vpc, _, err := c.findVPC(ctx)
	if err != nil {
		return network{}, err
	}
	if vpc == "" {
		log("no default network in " + c.region + ": creating a small one (outbound internet only)")
		if vpc, err = c.createVPC(ctx); err != nil {
			return network{}, err
		}
	}
	log("network " + vpc + ": ready")
	sg, err := c.findSG(ctx, vpc)
	if err != nil {
		return network{}, err
	}
	if sg == "" {
		out, err := c.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName: aws.String(SGName), VpcId: aws.String(vpc),
			Description:       aws.String("protolift box: no inbound rules, all access goes through AWS Systems Manager"),
			TagSpecifications: []ec2t.TagSpecification{tagSpec(ec2t.ResourceTypeSecurityGroup, nil)},
		})
		if err != nil {
			return network{}, translate(err, "create the firewall group")
		}
		sg = aws.ToString(out.GroupId)
	}
	log("firewall group " + sg + ": ready, no inbound rules")
	return network{vpc: vpc, sg: sg}, nil
}

func (c *Cloud) createVPC(ctx context.Context) (string, error) {
	v, err := c.ec2.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock:         aws.String("10.77.0.0/16"),
		TagSpecifications: []ec2t.TagSpecification{tagSpec(ec2t.ResourceTypeVpc, map[string]string{"Name": VPCName})},
	})
	if err != nil {
		return "", translate(err, "create the network")
	}
	vpc := aws.ToString(v.Vpc.VpcId)
	for _, in := range []*ec2.ModifyVpcAttributeInput{
		{VpcId: &vpc, EnableDnsSupport: &ec2t.AttributeBooleanValue{Value: aws.Bool(true)}},
		{VpcId: &vpc, EnableDnsHostnames: &ec2t.AttributeBooleanValue{Value: aws.Bool(true)}},
	} {
		if _, err := c.ec2.ModifyVpcAttribute(ctx, in); err != nil {
			return "", translate(err, "turn on DNS for the network")
		}
	}
	igw, err := c.ec2.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
		TagSpecifications: []ec2t.TagSpecification{tagSpec(ec2t.ResourceTypeInternetGateway, map[string]string{"Name": VPCName})},
	})
	if err != nil {
		return "", translate(err, "create the internet gateway")
	}
	igwID := aws.ToString(igw.InternetGateway.InternetGatewayId)
	if _, err := c.ec2.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{InternetGatewayId: &igwID, VpcId: &vpc}); err != nil {
		return "", translate(err, "attach the internet gateway")
	}
	rts, err := c.ec2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{Filters: []ec2t.Filter{
		{Name: aws.String("vpc-id"), Values: []string{vpc}}, {Name: aws.String("association.main"), Values: []string{"true"}},
	}})
	if err != nil {
		return "", translate(err, "find the network's route table")
	}
	if len(rts.RouteTables) == 0 {
		return "", errs.New(errs.CodeInternal, "the new network has no route table", "run `protolift uninstall` and `protolift init` again")
	}
	if _, err := c.ec2.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId: rts.RouteTables[0].RouteTableId, DestinationCidrBlock: aws.String("0.0.0.0/0"), GatewayId: &igwID,
	}); err != nil {
		return "", translate(err, "add the outbound internet route")
	}
	azs, err := c.ec2.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{Filters: []ec2t.Filter{
		{Name: aws.String("state"), Values: []string{"available"}}, {Name: aws.String("zone-type"), Values: []string{"availability-zone"}},
	}})
	if err != nil {
		return "", translate(err, "list availability zones")
	}
	for i, az := range azs.AvailabilityZones {
		if i == 3 {
			break
		}
		if _, err := c.ec2.CreateSubnet(ctx, &ec2.CreateSubnetInput{
			VpcId: &vpc, AvailabilityZone: az.ZoneName, CidrBlock: aws.String(fmt.Sprintf("10.77.%d.0/24", i)),
			TagSpecifications: []ec2t.TagSpecification{tagSpec(ec2t.ResourceTypeSubnet, map[string]string{"Name": VPCName})},
		}); err != nil {
			return "", translate(err, "create a subnet")
		}
	}
	return vpc, nil
}

// Uninstall removes what Init created. It refuses while any box exists, so
// it can never strand a billing box without its access role.
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
	vpc, managed, err := c.findVPC(ctx)
	if err != nil {
		return err
	}
	if vpc != "" {
		sg, err := c.findSG(ctx, vpc)
		if err != nil {
			return err
		}
		if sg != "" {
			if _, err := c.ec2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: &sg}); err != nil && !isNotFound(err) {
				return translate(err, "delete the firewall group")
			}
			log("firewall group " + sg + ": deleted")
		}
		if managed {
			if err := c.deleteVPC(ctx, vpc); err != nil {
				return err
			}
			log("network " + vpc + ": deleted")
		}
	}
	steps := []struct {
		doing string
		call  func() error
	}{
		{"detach the role from the instance profile", func() error {
			_, err := c.iam.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{InstanceProfileName: aws.String(RoleName), RoleName: aws.String(RoleName)})
			return err
		}},
		{"delete the instance profile", func() error {
			_, err := c.iam.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{InstanceProfileName: aws.String(RoleName)})
			return err
		}},
		{"detach the Systems Manager policy", func() error {
			_, err := c.iam.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{RoleName: aws.String(RoleName), PolicyArn: aws.String(c.ssmPolicyARN())})
			return err
		}},
		{"delete the access role", func() error {
			_, err := c.iam.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(RoleName)})
			return err
		}},
	}
	for _, s := range steps {
		if err := s.call(); err != nil && !isNotFound(err) {
			return translate(err, s.doing)
		}
	}
	log("access role " + RoleName + ": deleted")
	return nil
}

func (c *Cloud) deleteVPC(ctx context.Context, vpc string) error {
	f := []ec2t.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}
	subs, err := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: f})
	if err != nil {
		return translate(err, "list the network's subnets")
	}
	var all []error
	for _, s := range subs.Subnets {
		if _, err := c.ec2.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: s.SubnetId}); err != nil && !isNotFound(err) {
			all = append(all, err)
		}
	}
	if err := errors.Join(all...); err != nil {
		return translate(err, "delete the network's subnets")
	}
	igws, err := c.ec2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{Filters: []ec2t.Filter{{Name: aws.String("attachment.vpc-id"), Values: []string{vpc}}}})
	if err != nil {
		return translate(err, "list the internet gateway")
	}
	for _, g := range igws.InternetGateways {
		if _, err := c.ec2.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{InternetGatewayId: g.InternetGatewayId, VpcId: &vpc}); err != nil && !isNotFound(err) {
			return translate(err, "detach the internet gateway")
		}
		if _, err := c.ec2.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{InternetGatewayId: g.InternetGatewayId}); err != nil && !isNotFound(err) {
			return translate(err, "delete the internet gateway")
		}
	}
	if _, err := c.ec2.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: &vpc}); err != nil && !isNotFound(err) {
		return translate(err, "delete the network")
	}
	return nil
}
