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
	VpcId string `pulumi:"vpcId"`

	// PrivateSubnetIds are the IDs of the private subnets to attach the endpoint
	// to. AWS places one ENI per subnet. Pass all private subnet IDs — typically
	// one per AZ.
	PrivateSubnetIds []string `pulumi:"privateSubnetIds"`

	// Service is the AWS service to route privately.
	// Validated against the AwsVpcEndpointService enum in the schema.
	// The full com.amazonaws.{region}.{service} name is constructed at deploy time.
	Service string `pulumi:"service"`
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
	// endpoint. Zero rules by default. Ingress rules are added when compute
	// resources call grantEndpointAccess.
	SecurityGroupId pulumi.StringOutput `pulumi:"securityGroupId"`

	name string
}

func (v *VpcEndpoint) Annotate(a infer.Annotator) {
	a.SetToken("aws", "VpcEndpoint")
	a.Describe(&v, "An Anvil-managed AWS Interface VPC Endpoint. Creates one ENI per private subnet with private DNS enabled. Includes a dedicated security group with zero rules by default. Use grantEndpointAccess on compute resources to open the network path.")
}

// Name returns the logical name of this endpoint, used for rule naming in grants.
func (v *VpcEndpoint) Name() string {
	return v.name
}

// resolveServiceName constructs the full AWS endpoint service name from the
// service string value and the resolved AWS region.
//
// Format: com.amazonaws.{region}.{service}
// Example: com.amazonaws.ap-southeast-2.ssm
func resolveServiceName(ctx *pulumi.Context, service string) (string, error) {
	region, err := aws.GetRegion(ctx, &aws.GetRegionArgs{}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to resolve AWS region: %w", err)
	}
	return fmt.Sprintf("com.amazonaws.%s.%s", region.Region, service), nil
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

	// 1. Resolve the full AWS service name from the enum value + region.
	serviceName, err := resolveServiceName(ctx, args.Service)
	if err != nil {
		return nil, err
	}

	// 2. Dedicated security group — zero inbound, zero outbound by default.
	// Ingress rules are added per compute resource via grantEndpointAccess.
	sg, err := vpcsg.CreateSecurityGroup(ctx, name, stage, stageId, args.VpcId, v)
	if err != nil {
		return nil, err
	}

	// 3. Interface VPC Endpoint.
	// - VpcEndpointType: "Interface" — creates ENIs in each subnet
	// - PrivateDnsEnabled: true — overrides public service hostnames inside the VPC
	//   so consumers use standard AWS SDK endpoints (e.g. ssm.ap-southeast-2.amazonaws.com)
	//   and never reference vpce-xxx hostnames directly
	// - SubnetIds: one per private subnet, AWS places one ENI per AZ automatically
	// - SecurityGroupIds: dedicated SG with zero rules — nothing reachable until granted
	endpointName := provider.PhysicalName(stage, name, "vpce", stageId)

	subnetIds := make(pulumi.StringArray, len(args.PrivateSubnetIds))
	for i, id := range args.PrivateSubnetIds {
		subnetIds[i] = pulumi.String(id)
	}

	endpoint, err := ec2.NewVpcEndpoint(ctx, name+"-vpce", &ec2.VpcEndpointArgs{
		VpcId:             pulumi.String(args.VpcId),
		ServiceName:       pulumi.String(serviceName),
		VpcEndpointType:   pulumi.String("Interface"),
		PrivateDnsEnabled: pulumi.Bool(true),
		SubnetIds:         subnetIds,
		SecurityGroupIds:  pulumi.StringArray{sg.ID()},
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(endpointName),
			"ManagedBy": pulumi.String("anvil"),
			"Service":   pulumi.String(args.Service),
		},
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC endpoint for service %s: %w", args.Service, err)
	}

	// 4. Extract the first DNS entry for the dnsName output.
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

	// 5. Wire outputs.
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
