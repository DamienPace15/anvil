package vpcendpoint

import (
	"fmt"

	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/vpcsg"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// VpcEndpointArgs defines the inputs for an Anvil-managed Interface VPC Endpoint.
//
// Accepts both Anvil-managed VPC fields and imported VPC fields — pass vpcId
// and privateSubnetIds directly, consistent with VpcPlacementArgs.
//
// Cost: ~$7-9/month per endpoint per AZ. A 3-AZ deployment costs ~$21-27/month
// per endpoint service. Create endpoints deliberately — only for services with
// meaningful traffic volume or strict private routing requirements.
type VpcEndpointArgs struct {
	// VpcId is the ID of the VPC to create the endpoint in.
	VpcId pulumi.StringInput `pulumi:"vpcId"`

	// PrivateSubnetIds are the IDs of the private subnets to attach the endpoint
	// to. AWS places one ENI per subnet. Pass all private subnet IDs — typically
	// one per AZ.
	PrivateSubnetIds pulumi.StringArrayInput `pulumi:"privateSubnetIds"`

	// Service is the AWS service to route privately.
	// Validated against the AwsVpcEndpointService enum in the schema.
	// The full com.amazonaws.{region}.{service} name is constructed at deploy time.
	Service pulumi.StringInput `pulumi:"service"`
}

// VpcEndpoint is the Anvil-managed Interface VPC Endpoint component resource.
type VpcEndpoint struct {
	pulumi.ResourceState

	// EndpointId is the AWS VPC endpoint ID, e.g. vpce-0abc1234567890abc.
	EndpointId pulumi.StringOutput `pulumi:"endpointId"`

	// DnsName is the first DNS name assigned to the endpoint.
	// With private DNS enabled, normal consumers use the standard AWS SDK
	// hostname — this is exposed for debugging and multi-VPC architectures only.
	DnsName pulumi.StringOutput `pulumi:"dnsName"`

	// SecurityGroupId is the ID of the dedicated security group attached to this
	// endpoint. Inbound port 443 is allowed from the VPC CIDR — any resource in
	// the VPC can reach the endpoint at the network layer. Access is controlled by
	// the compute resource's egress rules (only resources with an explicit egress
	// rule pointing at this SG can initiate a connection) and by IAM (only roles
	// with the appropriate service permissions can make API calls).
	SecurityGroupId pulumi.StringOutput `pulumi:"securityGroupId"`

	name string
}

func (v *VpcEndpoint) Annotate(a infer.Annotator) {
	a.SetToken("aws", "VpcEndpoint")
	a.Describe(&v, "An Anvil-managed AWS Interface VPC Endpoint. Creates one ENI per private subnet with private DNS enabled. The endpoint security group allows inbound port 443 from the VPC CIDR. Access is controlled at two layers: compute egress rules (only resources with an explicit egress rule to this SG can initiate connections) and IAM (only roles with the appropriate service permissions can make API calls). This scales to any number of compute resources with a single inbound rule.")
}

// Name returns the logical name of this endpoint, used for rule naming in grants.
func (v *VpcEndpoint) Name() string {
	return v.name
}

// NewVpcEndpoint creates a new Anvil-managed Interface VPC Endpoint component.
func NewVpcEndpoint(ctx *pulumi.Context, name string, args VpcEndpointArgs, opts ...pulumi.ResourceOption) (*VpcEndpoint, error) {
	v := &VpcEndpoint{name: name}

	provider.NewContext(ctx)

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")

	opts = provider.WithDefault(opts, true)

	if err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, v, opts...); err != nil {
		return nil, err
	}

	// 1. Dedicated security group — accepts pulumi.StringInput directly.
	sg, err := vpcsg.CreateSecurityGroup(ctx, name, stage, stageId, args.VpcId, v)
	if err != nil {
		return nil, err
	}

	// 2. Look up the VPC CIDR from the VPC ID so we can wire the ingress rule.
	//    This works for both Anvil-managed VPCs and imported VPCs — the user
	//    only needs to pass vpcId and Anvil resolves the CIDR automatically.
	//    The lookup happens inside ApplyT since vpcId is an Output<string>.
	vpcCidr := args.VpcId.ToStringOutput().ApplyT(func(vpcId string) (string, error) {
		vpc, err := ec2.LookupVpc(ctx, &ec2.LookupVpcArgs{
			Id: &vpcId,
		}, nil)
		if err != nil {
			return "", fmt.Errorf("failed to look up VPC CIDR for %s: %w", vpcId, err)
		}
		return vpc.CidrBlock, nil
	}).(pulumi.StringOutput)

	// 3. CIDR ingress rule on port 443.
	//
	// Allows inbound 443 from the VPC CIDR — any resource in the VPC can reach
	// the endpoint ENI at the network layer. This scales to any number of compute
	// resources with a single rule (no per-compute ingress rules needed).
	//
	// Access is controlled at two additional layers:
	//   - Compute egress rules: only resources with an explicit egress rule
	//     pointing at this SG can initiate a TCP connection to the endpoint.
	//   - IAM: only roles with the appropriate service permissions can make
	//     API calls through the endpoint regardless of network access.
	if err := vpcsg.AddIngressCIDRRule(
		ctx,
		fmt.Sprintf("%s-vpc-ingress", name),
		sg.ID().ToStringOutput(),
		vpcCidr,
		443, 443,
		v,
	); err != nil {
		return nil, fmt.Errorf("failed to wire VPC CIDR ingress on endpoint SG: %w", err)
	}

	// 4. Resolve the AWS region synchronously — safe to call outside Apply.
	region, err := aws.GetRegion(ctx, &aws.GetRegionArgs{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve AWS region: %w", err)
	}
	awsRegion := region.Region

	endpointName := provider.PhysicalName(stage, name, "vpce", stageId)

	// 5. Lift inputs to Outputs so they can be passed directly to ec2.NewVpcEndpoint.
	vpcIdOutput := args.VpcId.ToStringOutput()
	serviceOutput := args.Service.ToStringOutput()
	subnetIdsOutput := args.PrivateSubnetIds.ToStringArrayOutput()

	// Build the full service name as an Output by applying over the service string.
	serviceNameOutput := serviceOutput.ApplyT(func(svc string) string {
		return fmt.Sprintf("com.amazonaws.%s.%s", awsRegion, svc)
	}).(pulumi.StringOutput)

	// 6. Interface VPC Endpoint.
	// - VpcEndpointType: "Interface" — creates ENIs in each subnet
	// - PrivateDnsEnabled: true — overrides public service hostnames inside the VPC
	//   so consumers use standard AWS SDK endpoints (e.g. sqs.ap-southeast-2.amazonaws.com)
	//   and never reference vpce-xxx hostnames directly
	// - SubnetIds: one per private subnet, AWS places one ENI per AZ automatically
	// - SecurityGroupIds: dedicated SG with VPC CIDR ingress on 443
	endpoint, err := ec2.NewVpcEndpoint(ctx, name+"-vpce", &ec2.VpcEndpointArgs{
		VpcId:             vpcIdOutput,
		ServiceName:       serviceNameOutput,
		VpcEndpointType:   pulumi.String("Interface"),
		PrivateDnsEnabled: pulumi.Bool(true),
		SubnetIds:         subnetIdsOutput,
		SecurityGroupIds:  pulumi.StringArray{sg.ID()},
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(endpointName),
			"ManagedBy": pulumi.String("anvil"),
			"Service":   serviceOutput,
		},
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC endpoint: %w", err)
	}

	// 7. Extract the first DNS entry for the dnsName output.
	// With private DNS enabled, consumers never need this — it's exposed for
	// debugging and multi-VPC architectures where the private DNS override
	// does not propagate to peered VPCs.
	dnsName := endpoint.DnsEntries.ApplyT(func(entries []ec2.VpcEndpointDnsEntry) string {
		if len(entries) == 0 {
			return ""
		}
		if entries[0].DnsName == nil {
			return ""
		}
		return *entries[0].DnsName
	}).(pulumi.StringOutput)

	// 8. Wire outputs.
	v.EndpointId = endpoint.ID().ToStringOutput()
	v.DnsName = dnsName
	v.SecurityGroupId = sg.ID().ToStringOutput()

	ctx.RegisterResourceOutputs(v, pulumi.Map{
		"endpointId":      endpoint.ID(),
		"dnsName":         dnsName,
		"securityGroupId": sg.ID(),
	})

	return v, nil
}
