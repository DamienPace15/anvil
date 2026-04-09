package vpc

import (
	"fmt"

	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// NatArgs configures outbound internet access for private subnets.
type NatArgs struct {
	// NatType is the type of NAT to provision.
	// "gateway" — one AWS managed NAT Gateway per AZ (~$32/month per AZ + $0.045/GB).
	// "fck-nat" — single fck-nat EC2 instance shared across all AZs (~$4-6/month).
	NatType string `pulumi:"natType"`

	// InstanceType is the EC2 instance type for the fck-nat instance.
	// Only applies when NatType is "fck-nat". Default: "t4g.small".
	InstanceType string `pulumi:"instanceType,optional"`
}

// VpcArgs defines the inputs for an Anvil-managed VPC.
//
// The following are always enforced at no cost:
//   - DNS hostnames and DNS resolution enabled (required for RDS, ECS service
//     discovery, and PrivateLink)
//   - Public and private subnets across the specified AZ count
//   - Internet Gateway attached to public subnet route tables
//   - Deterministic CIDR carving so Pulumi plans never show unexpected churn
//
// Gateway endpoints (S3, DynamoDB) are explicitly opt-in via the VpcEndpoint
// component, consistent with Anvil's explicit grant philosophy.
type VpcArgs struct {
	// Cidr is the IPv4 CIDR block for the VPC.
	// Default: "10.0.0.0/16"
	Cidr string `pulumi:"cidr,optional"`

	// AvailabilityZones controls how many AZs to deploy subnets into.
	// Valid values: 1, 2, 3. Default: 1.
	// "high" availability in App.defaults maps to 3, "low" maps to 1.
	AvailabilityZones int `pulumi:"availabilityZones,optional"`

	// Nat configures outbound internet access for private subnets.
	// Omit for a fully private VPC with no outbound internet access.
	Nat *NatArgs `pulumi:"nat,optional"`
}

// Vpc is the Anvil-managed VPC component resource.
type Vpc struct {
	pulumi.ResourceState

	VpcId                  pulumi.StringOutput      `pulumi:"vpcId"`
	PrivateSubnetIds       pulumi.StringArrayOutput `pulumi:"privateSubnetIds"`
	PublicSubnetIds        pulumi.StringArrayOutput `pulumi:"publicSubnetIds"`
	AvailabilityZones      pulumi.StringArrayOutput `pulumi:"availabilityZones"`
	DefaultSecurityGroupId pulumi.StringOutput      `pulumi:"defaultSecurityGroupId"`

	name string
}

func (v *Vpc) Annotate(a infer.Annotator) {
	a.SetToken("aws", "Vpc")
	a.Describe(&v, "An Anvil-managed VPC. Provides public and private subnets across one to three Availability Zones with an Internet Gateway and correct route tables. Optional NAT Gateway or fck-nat EC2 instance for outbound internet access. DNS hostnames and resolution are enabled by default, required for RDS, ECS service discovery, and PrivateLink.")
}

func resolveAZCount(args VpcArgs, cfg *c.Config) int {
	if args.AvailabilityZones > 0 {
		if args.AvailabilityZones > 3 {
			return 3
		}
		return args.AvailabilityZones
	}
	if cfg.Get("availability") == "high" {
		return 3
	}
	return 1
}

func resolveCIDR(args VpcArgs) string {
	if args.Cidr != "" {
		return args.Cidr
	}
	return "10.0.0.0/16"
}

func resolveNatInstanceType(nat *NatArgs) string {
	if nat.InstanceType != "" {
		return nat.InstanceType
	}
	return "t4g.small"
}

// publicSubnetCIDR returns the CIDR for public subnet at index i.
// Offset 0: 10.0.0.0/24, 10.0.1.0/24, 10.0.2.0/24
func publicSubnetCIDR(vpcCIDR string, i int) string {
	return fmt.Sprintf("%s.%d.0/24", vpcBase(vpcCIDR), i)
}

// privateSubnetCIDR returns the CIDR for private subnet at index i.
// Offset 10: 10.0.10.0/24, 10.0.11.0/24, 10.0.12.0/24
// Gap from public (0-2) leaves room for future subnet tiers without renumbering.
func privateSubnetCIDR(vpcCIDR string, i int) string {
	return fmt.Sprintf("%s.%d.0/24", vpcBase(vpcCIDR), 10+i)
}

// vpcBase extracts the first two octets: "10.0.0.0/16" -> "10.0"
func vpcBase(cidr string) string {
	dots := 0
	for i, ch := range cidr {
		if ch == '.' {
			dots++
			if dots == 2 {
				return cidr[:i]
			}
		}
	}
	return "10.0"
}

func stringInputsToArrayOutput(inputs []pulumi.StringInput) pulumi.StringArrayOutput {
	arr := make(pulumi.StringArray, len(inputs))
	for i, input := range inputs {
		arr[i] = input
	}
	return arr.ToStringArrayOutput()
}

func stringInputsToArray(inputs []pulumi.StringInput) pulumi.StringArray {
	arr := make(pulumi.StringArray, len(inputs))
	for i, input := range inputs {
		arr[i] = input
	}
	return arr
}

// NewVpc creates a new Anvil-managed VPC component.
func NewVpc(ctx *pulumi.Context, name string, args VpcArgs, opts ...pulumi.ResourceOption) (*Vpc, error) {
	v := &Vpc{name: name}

	provider.NewContext(ctx)

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")

	azCount := resolveAZCount(args, cfg)
	vpcCIDR := resolveCIDR(args)

	opts = provider.WithDefault(opts, true)

	if err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, v, opts...); err != nil {
		return nil, err
	}

	// 1. Validate nat config early so errors are clear before any AWS calls.
	if args.Nat != nil {
		switch args.Nat.NatType {
		case "gateway", "fck-nat":
			// valid
		default:
			return nil, fmt.Errorf("invalid nat.natType %q: must be \"gateway\" or \"fck-nat\"", args.Nat.NatType)
		}
	}

	// 2. Resolve AZ names via aws.GetAvailabilityZones.
	// Never hardcode AZ suffixes -- regions vary in count and naming convention.
	azResult, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
		State: pulumi.StringRef("available"),
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve availability zones: %w", err)
	}

	resolvedAZs := make([]string, azCount)
	for i := 0; i < azCount; i++ {
		if i >= len(azResult.Names) {
			return nil, fmt.Errorf("region does not have %d availability zones (found %d)", azCount, len(azResult.Names))
		}
		resolvedAZs[i] = azResult.Names[i]
	}

	// 3. VPC.
	// enableDnsHostnames and enableDnsSupport required for RDS, ECS service
	// discovery, and PrivateLink interface endpoints.
	vpcName := provider.PhysicalName(stage, name, "vpc", stageId)

	vpcResource, err := ec2.NewVpc(ctx, name, &ec2.VpcArgs{
		CidrBlock:          pulumi.String(vpcCIDR),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(vpcName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC: %w", err)
	}

	// 4. Internet Gateway.
	// One per VPC. Attached to public subnet route tables.
	igwName := provider.PhysicalName(stage, name, "igw", stageId)

	igw, err := ec2.NewInternetGateway(ctx, name+"-igw", &ec2.InternetGatewayArgs{
		VpcId: vpcResource.ID(),
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(igwName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to create Internet Gateway: %w", err)
	}

	// 5. Public subnets.
	// One shared route table for all public subnets -- they all route the same way.
	publicSubnetIds := make([]pulumi.StringInput, azCount)
	publicSubnets := make([]*ec2.Subnet, azCount)

	pubRTName := provider.PhysicalName(stage, name, "public-rt", stageId)
	publicRT, err := ec2.NewRouteTable(ctx, name+"-public-rt", &ec2.RouteTableArgs{
		VpcId: vpcResource.ID(),
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(pubRTName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to create public route table: %w", err)
	}

	if _, err = ec2.NewRoute(ctx, name+"-public-default-route", &ec2.RouteArgs{
		RouteTableId:         publicRT.ID(),
		DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
		GatewayId:            igw.ID(),
	}, pulumi.Parent(v)); err != nil {
		return nil, fmt.Errorf("failed to create public default route: %w", err)
	}

	for i, az := range resolvedAZs {
		subnetName := provider.PhysicalName(stage, fmt.Sprintf("%s-public-%d", name, i+1), "subnet", stageId)

		subnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-public-subnet-%d", name, i+1), &ec2.SubnetArgs{
			VpcId:               vpcResource.ID(),
			CidrBlock:           pulumi.String(publicSubnetCIDR(vpcCIDR, i)),
			AvailabilityZone:    pulumi.String(az),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			Tags: pulumi.StringMap{
				"Name":      pulumi.String(subnetName),
				"ManagedBy": pulumi.String("anvil"),
				"Tier":      pulumi.String("public"),
			},
		}, pulumi.Parent(v))
		if err != nil {
			return nil, fmt.Errorf("failed to create public subnet %d: %w", i+1, err)
		}

		if _, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-public-rta-%d", name, i+1), &ec2.RouteTableAssociationArgs{
			SubnetId:     subnet.ID(),
			RouteTableId: publicRT.ID(),
		}, pulumi.Parent(v)); err != nil {
			return nil, fmt.Errorf("failed to associate public subnet %d with route table: %w", i+1, err)
		}

		publicSubnetIds[i] = subnet.ID()
		publicSubnets[i] = subnet
	}

	// 6. Private subnets.
	// Each gets its own route table so NAT routes can be scoped per-AZ.
	privateSubnetIds := make([]pulumi.StringInput, azCount)
	privateRouteTables := make([]*ec2.RouteTable, azCount)

	for i, az := range resolvedAZs {
		subnetName := provider.PhysicalName(stage, fmt.Sprintf("%s-private-%d", name, i+1), "subnet", stageId)
		rtName := provider.PhysicalName(stage, fmt.Sprintf("%s-private-%d", name, i+1), "rt", stageId)

		rt, err := ec2.NewRouteTable(ctx, fmt.Sprintf("%s-private-rt-%d", name, i+1), &ec2.RouteTableArgs{
			VpcId: vpcResource.ID(),
			Tags: pulumi.StringMap{
				"Name":      pulumi.String(rtName),
				"ManagedBy": pulumi.String("anvil"),
				"Tier":      pulumi.String("private"),
			},
		}, pulumi.Parent(v))
		if err != nil {
			return nil, fmt.Errorf("failed to create private route table %d: %w", i+1, err)
		}
		privateRouteTables[i] = rt

		subnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-private-subnet-%d", name, i+1), &ec2.SubnetArgs{
			VpcId:            vpcResource.ID(),
			CidrBlock:        pulumi.String(privateSubnetCIDR(vpcCIDR, i)),
			AvailabilityZone: pulumi.String(az),
			Tags: pulumi.StringMap{
				"Name":      pulumi.String(subnetName),
				"ManagedBy": pulumi.String("anvil"),
				"Tier":      pulumi.String("private"),
			},
		}, pulumi.Parent(v))
		if err != nil {
			return nil, fmt.Errorf("failed to create private subnet %d: %w", i+1, err)
		}

		if _, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-private-rta-%d", name, i+1), &ec2.RouteTableAssociationArgs{
			SubnetId:     subnet.ID(),
			RouteTableId: rt.ID(),
		}, pulumi.Parent(v)); err != nil {
			return nil, fmt.Errorf("failed to associate private subnet %d with route table: %w", i+1, err)
		}

		privateSubnetIds[i] = subnet.ID()
	}

	// 7. NAT (optional).
	// Wires outbound internet routes onto the private subnet route tables.
	if args.Nat != nil {
		switch args.Nat.NatType {
		case "gateway":
			if err := createNatGateways(ctx, name, stage, stageId, resolvedAZs, publicSubnets, privateRouteTables, v); err != nil {
				return nil, err
			}
		case "fck-nat":
			instanceType := resolveNatInstanceType(args.Nat)
			if err := createNatInstance(ctx, name, stage, stageId, instanceType, vpcResource, publicSubnets[0], privateRouteTables, v); err != nil {
				return nil, err
			}
		}
	}

	// 8. Lock down the VPC default security group.
	// AWS creates a default SG with allow-all inbound from itself and allow-all
	// outbound. We adopt it and remove all rules. Anvil components never use
	// the default SG -- each gets a dedicated SG (ANVIL-N006).
	defaultSG, err := ec2.NewDefaultSecurityGroup(ctx, name+"-default-sg", &ec2.DefaultSecurityGroupArgs{
		VpcId: vpcResource.ID(),
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(fmt.Sprintf("%s-default-sg-do-not-use", vpcName)),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(v))
	if err != nil {
		return nil, fmt.Errorf("failed to lock down default security group: %w", err)
	}

	// 9. Wire outputs.
	v.VpcId = vpcResource.ID().ToStringOutput()
	v.DefaultSecurityGroupId = defaultSG.ID().ToStringOutput()
	v.PrivateSubnetIds = stringInputsToArrayOutput(privateSubnetIds)
	v.PublicSubnetIds = stringInputsToArrayOutput(publicSubnetIds)

	azStringInputs := make([]pulumi.StringInput, azCount)
	for i, az := range resolvedAZs {
		azStringInputs[i] = pulumi.String(az)
	}
	v.AvailabilityZones = stringInputsToArrayOutput(azStringInputs)

	ctx.RegisterResourceOutputs(v, pulumi.Map{
		"vpcId":                  vpcResource.ID(),
		"privateSubnetIds":       stringInputsToArray(privateSubnetIds),
		"publicSubnetIds":        stringInputsToArray(publicSubnetIds),
		"availabilityZones":      pulumi.ToStringArray(resolvedAZs),
		"defaultSecurityGroupId": defaultSG.ID(),
	})

	return v, nil
}

// createNatGateways provisions one AWS managed NAT Gateway per AZ.
// Each gateway sits in the public subnet of its AZ with its own Elastic IP.
// The private route table in the same AZ routes 0.0.0.0/0 to the local NAT
// Gateway for true HA -- a private subnet in AZ-a never traverses AZ-b.
func createNatGateways(
	ctx *pulumi.Context,
	name, stage, stageId string,
	resolvedAZs []string,
	publicSubnets []*ec2.Subnet,
	privateRouteTables []*ec2.RouteTable,
	parent pulumi.Resource,
) error {
	for i := range resolvedAZs {
		eipName := provider.PhysicalName(stage, fmt.Sprintf("%s-nat-%d", name, i+1), "eip", stageId)
		eip, err := ec2.NewEip(ctx, fmt.Sprintf("%s-nat-eip-%d", name, i+1), &ec2.EipArgs{
			Domain: pulumi.String("vpc"),
			Tags: pulumi.StringMap{
				"Name":      pulumi.String(eipName),
				"ManagedBy": pulumi.String("anvil"),
			},
		}, pulumi.Parent(parent))
		if err != nil {
			return fmt.Errorf("failed to create EIP for NAT Gateway %d: %w", i+1, err)
		}

		natName := provider.PhysicalName(stage, fmt.Sprintf("%s-%d", name, i+1), "nat-gw", stageId)
		natGW, err := ec2.NewNatGateway(ctx, fmt.Sprintf("%s-nat-gw-%d", name, i+1), &ec2.NatGatewayArgs{
			SubnetId:     publicSubnets[i].ID(),
			AllocationId: eip.ID(),
			Tags: pulumi.StringMap{
				"Name":      pulumi.String(natName),
				"ManagedBy": pulumi.String("anvil"),
			},
		}, pulumi.Parent(parent))
		if err != nil {
			return fmt.Errorf("failed to create NAT Gateway %d: %w", i+1, err)
		}

		if _, err = ec2.NewRoute(ctx, fmt.Sprintf("%s-private-nat-route-%d", name, i+1), &ec2.RouteArgs{
			RouteTableId:         privateRouteTables[i].ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			NatGatewayId:         natGW.ID(),
		}, pulumi.Parent(parent)); err != nil {
			return fmt.Errorf("failed to create NAT route for AZ %d: %w", i+1, err)
		}
	}
	return nil
}

// createNatInstance provisions a single fck-nat EC2 instance in the first
// public subnet. All private route tables point to this instance.
// One instance regardless of AZ count -- the accepted cost tradeoff for fck-nat.
//
// The AMI is resolved dynamically from the fck-nat AWS account (568608671756)
// so it always uses the latest stable arm64 release.
func createNatInstance(
	ctx *pulumi.Context,
	name, stage, stageId string,
	instanceType string,
	vpcResource *ec2.Vpc,
	publicSubnet *ec2.Subnet,
	privateRouteTables []*ec2.RouteTable,
	parent pulumi.Resource,
) error {
	// Resolve the latest fck-nat AMI for arm64 (required for t4g instances).
	natAMI, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
		MostRecent: pulumi.BoolRef(true),
		Owners:     []string{"568608671756"},
		Filters: []ec2.GetAmiFilter{
			{
				Name:   "name",
				Values: []string{"fck-nat-al2023-*-arm64-ebs"},
			},
			{
				Name:   "architecture",
				Values: []string{"arm64"},
			},
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return fmt.Errorf("failed to resolve fck-nat AMI: %w", err)
	}

	// Security group for the NAT instance.
	// Inbound: all traffic from the VPC CIDR so private subnets can route through it.
	// Outbound: all traffic to the internet.
	natSGName := provider.PhysicalName(stage, name, "nat-sg", stageId)
	natSG, err := ec2.NewSecurityGroup(ctx, name+"-nat-sg", &ec2.SecurityGroupArgs{
		VpcId:       vpcResource.ID(),
		Description: pulumi.String("Security group for Anvil fck-nat instance"),
		Ingress: ec2.SecurityGroupIngressArray{
			&ec2.SecurityGroupIngressArgs{
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				Protocol:   pulumi.String("-1"),
				CidrBlocks: pulumi.StringArray{vpcResource.CidrBlock},
			},
		},
		Egress: ec2.SecurityGroupEgressArray{
			&ec2.SecurityGroupEgressArgs{
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				Protocol:   pulumi.String("-1"),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(natSGName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return fmt.Errorf("failed to create NAT instance security group: %w", err)
	}

	// NAT EC2 instance in the first public subnet.
	// SourceDestCheck must be false -- the instance forwards packets not
	// addressed to it, which AWS blocks by default.
	natInstanceName := provider.PhysicalName(stage, name, "nat-instance", stageId)
	natInstance, err := ec2.NewInstance(ctx, name+"-nat-instance", &ec2.InstanceArgs{
		Ami:                      pulumi.String(natAMI.Id),
		InstanceType:             pulumi.String(instanceType),
		SubnetId:                 publicSubnet.ID(),
		VpcSecurityGroupIds:      pulumi.StringArray{natSG.ID()},
		SourceDestCheck:          pulumi.Bool(false),
		AssociatePublicIpAddress: pulumi.Bool(true),
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(natInstanceName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return fmt.Errorf("failed to create NAT instance: %w", err)
	}

	// Route all private subnet tables through the NAT instance's primary ENI.
	for i, rt := range privateRouteTables {
		if _, err = ec2.NewRoute(ctx, fmt.Sprintf("%s-private-nat-route-%d", name, i+1), &ec2.RouteArgs{
			RouteTableId:         rt.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			NetworkInterfaceId:   natInstance.PrimaryNetworkInterfaceId,
		}, pulumi.Parent(parent)); err != nil {
			return fmt.Errorf("failed to create NAT instance route for private subnet %d: %w", i+1, err)
		}
	}

	return nil
}
