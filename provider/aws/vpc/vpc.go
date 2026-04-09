package vpc

import (
	"encoding/json"
	"fmt"

	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
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

// BastionArgs configures the SSM bastion host.
//
// The bastion is a jump box that gives you access into the private network
// for local debugging, database migrations, and incident response.
//
// # How to connect
//
// Prerequisites: AWS CLI + Session Manager plugin installed locally.
//
//	aws ssm start-session --target <bastionInstanceId>
//
// To forward a local port to RDS (replace values as needed):
//
//	aws ssm start-session \
//	  --target <bastionInstanceId> \
//	  --document-name AWS-StartPortForwardingSessionToRemoteHost \
//	  --parameters '{"host":["<rds-endpoint>"],"portNumber":["5432"],"localPortNumber":["5432"]}'
//
// Then connect locally:
//
//	psql -h localhost -p 5432 -U <user> -d <dbname>
//
// The bastion never exposes port 22 or accepts any inbound connections.
// The SSM agent inside the instance initiates an outbound HTTPS connection
// to the AWS SSM service, which brokers your session. Access is controlled
// entirely through IAM — revoke access by revoking IAM permissions.
type BastionArgs struct {
	// InstanceType is the EC2 instance type. Default: "t4g.nano".
	// The bastion is purely a jump box with no CPU/memory requirements.
	InstanceType string `pulumi:"instanceType,optional"`

	// AllowedCidrs restricts which source IPs can initiate SSM sessions
	// via an IAM policy condition. Omit to allow any authenticated IAM
	// principal to start a session.
	// Example: ["203.0.113.0/32"] to restrict to your office IP.
	AllowedCidrs []string `pulumi:"allowedCidrs,optional"`
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

	// Bastion enables an SSM bastion host in the first public subnet.
	// Use this to connect to private resources (RDS, ElastiCache) locally.
	// No SSH, no port 22, no key pairs — access via AWS SSM Session Manager only.
	Bastion *BastionArgs `pulumi:"bastion,optional"`
}

// Vpc is the Anvil-managed VPC component resource.
type Vpc struct {
	pulumi.ResourceState

	VpcId                  pulumi.StringOutput      `pulumi:"vpcId"`
	PrivateSubnetIds       pulumi.StringArrayOutput `pulumi:"privateSubnetIds"`
	PublicSubnetIds        pulumi.StringArrayOutput `pulumi:"publicSubnetIds"`
	AvailabilityZones      pulumi.StringArrayOutput `pulumi:"availabilityZones"`
	DefaultSecurityGroupId pulumi.StringOutput      `pulumi:"defaultSecurityGroupId"`

	// BastionInstanceId is the EC2 instance ID of the bastion host.
	// Use this with: aws ssm start-session --target <BastionInstanceId>
	// Only populated when bastion is enabled.
	BastionInstanceId pulumi.StringOutput `pulumi:"bastionInstanceId"`

	// BastionSecurityGroupId is the security group ID of the bastion host.
	// Forward-looking: use this to grant the bastion access to private resources.
	// Example: db.grant(network.bastion, { access: "readWrite" })
	// Only populated when bastion is enabled.
	BastionSecurityGroupId pulumi.StringOutput `pulumi:"bastionSecurityGroupId"`

	name string
}

func (v *Vpc) Annotate(a infer.Annotator) {
	a.SetToken("aws", "Vpc")
	a.Describe(&v, "An Anvil-managed VPC. Provides public and private subnets across one to three Availability Zones with an Internet Gateway and correct route tables. Optional NAT Gateway or fck-nat EC2 instance for outbound internet access. Optional SSM bastion host for private network access. DNS hostnames and resolution are enabled by default, required for RDS, ECS service discovery, and PrivateLink.")
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

func resolveBastionInstanceType(bastion *BastionArgs) string {
	if bastion.InstanceType != "" {
		return bastion.InstanceType
	}
	return "t4g.nano"
}

// publicSubnetCIDR returns the CIDR for public subnet at index i.
// Offset 0: 10.0.0.0/24, 10.0.1.0/24, 10.0.2.0/24
func publicSubnetCIDR(vpcCIDR string, i int) string {
	return fmt.Sprintf("%s.%d.0/24", vpcBase(vpcCIDR), i)
}

// privateSubnetCIDR returns the CIDR for private subnet at index i.
// Offset 10: 10.0.10.0/24, 10.0.11.0/24, 10.0.12.0/24
func privateSubnetCIDR(vpcCIDR string, i int) string {
	return fmt.Sprintf("%s.%d.0/24", vpcBase(vpcCIDR), 10+i)
}

// privateSubnetCIDRs returns all private subnet CIDRs for the given AZ count.
func privateSubnetCIDRs(vpcCIDR string, azCount int) []string {
	cidrs := make([]string, azCount)
	for i := 0; i < azCount; i++ {
		cidrs[i] = privateSubnetCIDR(vpcCIDR, i)
	}
	return cidrs
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

	// 1. Validate nat config early.
	if args.Nat != nil {
		switch args.Nat.NatType {
		case "gateway", "fck-nat":
		default:
			return nil, fmt.Errorf("invalid nat.natType %q: must be \"gateway\" or \"fck-nat\"", args.Nat.NatType)
		}
	}

	// 2. Resolve AZ names via aws.GetAvailabilityZones.
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
	if args.Nat != nil {
		switch args.Nat.NatType {
		case "gateway":
			if err := createNatGateways(ctx, name, stage, stageId, resolvedAZs, publicSubnets, privateRouteTables, v); err != nil {
				return nil, err
			}
		case "fck-nat":
			instanceType := resolveNatInstanceType(args.Nat)
			privateCIDRs := privateSubnetCIDRs(vpcCIDR, azCount)
			if err := createNatInstance(ctx, name, stage, stageId, instanceType, privateCIDRs, publicSubnets[0], privateRouteTables, v); err != nil {
				return nil, err
			}
		}
	}

	// 8. Bastion host (optional).
	if args.Bastion != nil {
		instanceType := resolveBastionInstanceType(args.Bastion)
		bastionInstance, bastionSG, err := createBastion(ctx, name, stage, stageId, instanceType, args.Bastion.AllowedCidrs, vpcResource, publicSubnets[0], v)
		if err != nil {
			return nil, err
		}
		v.BastionInstanceId = bastionInstance.ID().ToStringOutput()
		v.BastionSecurityGroupId = bastionSG.ID().ToStringOutput()
	}

	// 9. Lock down the VPC default security group.
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

	// 10. Wire outputs.
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
		"bastionInstanceId":      v.BastionInstanceId,
		"bastionSecurityGroupId": v.BastionSecurityGroupId,
	})

	return v, nil
}

// createBastion provisions a hardened SSM-only bastion host in the first
// public subnet. No SSH, no port 22, no key pairs. Access is via AWS SSM
// Session Manager only, controlled entirely through IAM.
//
// # How to connect
//
// Install prerequisites:
//
//	brew install awscli session-manager-plugin   # macOS
//
// Start a session:
//
//	aws ssm start-session --target <bastionInstanceId>
//
// Forward a port to RDS:
//
//	aws ssm start-session \
//	  --target <bastionInstanceId> \
//	  --document-name AWS-StartPortForwardingSessionToRemoteHost \
//	  --parameters '{"host":["<rds-endpoint>"],"portNumber":["5432"],"localPortNumber":["5432"]}'
//
// Then connect locally:
//
//	psql -h localhost -p 5432 -U <user> -d <dbname>
//
// The bastion polls the SSM service outbound over HTTPS — it never receives
// inbound connections, so its security group has zero inbound rules.
func createBastion(
	ctx *pulumi.Context,
	name, stage, stageId string,
	instanceType string,
	allowedCidrs []string,
	vpcResource *ec2.Vpc,
	publicSubnet *ec2.Subnet,
	parent pulumi.Resource,
) (*ec2.Instance, *ec2.SecurityGroup, error) {

	// 1. IAM role — EC2 trust policy.
	// AmazonSSMManagedInstanceCore grants the SSM agent permission to:
	//   - Register with the SSM service
	//   - Receive session commands
	//   - Write session logs
	assumeRolePolicy := `{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": { "Service": "ec2.amazonaws.com" },
			"Action": "sts:AssumeRole"
		}]
	}`

	bastionRole := &iam.Role{}
	if err := ctx.RegisterResource("aws:iam/role:Role", name+"-bastion-role", pulumi.Map{
		"name":             pulumi.String(provider.PhysicalName(stage, name+"-bastion", "role", stageId)),
		"assumeRolePolicy": pulumi.String(assumeRolePolicy),
		"tags": pulumi.StringMap{
			"Name":      pulumi.String(provider.PhysicalName(stage, name+"-bastion", "role", stageId)),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, bastionRole, pulumi.Parent(parent)); err != nil {
		return nil, nil, fmt.Errorf("failed to create bastion IAM role: %w", err)
	}

	// 2. Attach AmazonSSMManagedInstanceCore — the minimum policy for SSM.
	if err := ctx.RegisterResource("aws:iam/rolePolicyAttachment:RolePolicyAttachment", name+"-bastion-ssm-policy", pulumi.Map{
		"role":      bastionRole.Name,
		"policyArn": pulumi.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
	}, &iam.RolePolicyAttachment{}, pulumi.Parent(parent)); err != nil {
		return nil, nil, fmt.Errorf("failed to attach SSM policy to bastion role: %w", err)
	}

	// 3. Optional: restrict SSM session initiation to specific source CIDRs.
	// This IAM condition checks the source IP of the ssm:StartSession API call
	// (the developer's IP calling the AWS API) not the session traffic itself.
	if len(allowedCidrs) > 0 {
		type condition struct {
			IpAddress map[string][]string `json:"IpAddress"`
		}
		type statement struct {
			Effect    string    `json:"Effect"`
			Action    string    `json:"Action"`
			Resource  string    `json:"Resource"`
			Condition condition `json:"Condition"`
		}
		type doc struct {
			Version   string      `json:"Version"`
			Statement []statement `json:"Statement"`
		}

		policyDoc, err := json.Marshal(doc{
			Version: "2012-10-17",
			Statement: []statement{{
				Effect:   "Allow",
				Action:   "ssm:StartSession",
				Resource: "*",
				Condition: condition{
					IpAddress: map[string][]string{
						"aws:SourceIp": allowedCidrs,
					},
				},
			}},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal bastion CIDR policy: %w", err)
		}

		if err := ctx.RegisterResource("aws:iam/rolePolicy:RolePolicy", name+"-bastion-cidr-policy", pulumi.Map{
			"role":   bastionRole.Name,
			"policy": pulumi.String(string(policyDoc)),
		}, &iam.RolePolicy{}, pulumi.Parent(parent)); err != nil {
			return nil, nil, fmt.Errorf("failed to attach CIDR restriction policy to bastion: %w", err)
		}
	}

	// 4. Instance profile wrapping the role.
	// EC2 instances reference profiles, not roles directly.
	bastionProfile := &iam.InstanceProfile{}
	if err := ctx.RegisterResource("aws:iam/instanceProfile:InstanceProfile", name+"-bastion-profile", pulumi.Map{
		"name": pulumi.String(provider.PhysicalName(stage, name+"-bastion", "profile", stageId)),
		"role": bastionRole.Name,
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	}, bastionProfile, pulumi.Parent(parent)); err != nil {
		return nil, nil, fmt.Errorf("failed to create bastion instance profile: %w", err)
	}

	// 5. Security group — zero inbound rules.
	// The bastion never receives inbound connections. The SSM agent initiates
	// an outbound HTTPS connection to ssm.amazonaws.com which brokers sessions.
	// Outbound 443 is required to reach SSM, SSMMessages, and EC2Messages endpoints.
	bastionSGName := provider.PhysicalName(stage, name, "bastion-sg", stageId)
	bastionSG, err := ec2.NewSecurityGroup(ctx, name+"-bastion-sg", &ec2.SecurityGroupArgs{
		VpcId:       vpcResource.ID(),
		Description: pulumi.String("Anvil bastion host — SSM only, zero inbound rules"),
		// No ingress rules -- the bastion never receives inbound connections.
		Egress: ec2.SecurityGroupEgressArray{
			&ec2.SecurityGroupEgressArgs{
				Description: pulumi.String("HTTPS outbound for SSM agent (ssm, ssmmessages, ec2messages)"),
				FromPort:    pulumi.Int(443),
				ToPort:      pulumi.Int(443),
				Protocol:    pulumi.String("tcp"),
				CidrBlocks:  pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(bastionSGName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create bastion security group: %w", err)
	}

	// 6. Resolve latest Amazon Linux 2023 arm64 AMI.
	// AL2023 ships with the SSM agent pre-installed and enabled.
	// We use the official Amazon AMI (owner: amazon) so no third-party trust required.
	bastionAMI, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
		MostRecent: pulumi.BoolRef(true),
		Owners:     []string{"amazon"},
		Filters: []ec2.GetAmiFilter{
			{
				Name:   "name",
				Values: []string{"al2023-ami-2023.*-kernel-*-arm64"},
			},
			{
				Name:   "architecture",
				Values: []string{"arm64"},
			},
			{
				Name:   "virtualization-type",
				Values: []string{"hvm"},
			},
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve Amazon Linux 2023 arm64 AMI: %w", err)
	}

	// 7. User data — ensures SSM agent is running.
	// AL2023 ships with SSM agent pre-installed but this makes it explicit
	// and ensures it starts on boot even after unexpected stops.
	userData := `#!/bin/bash
set -e
systemctl enable amazon-ssm-agent
systemctl start amazon-ssm-agent
`

	// 8. Bastion EC2 instance.
	// IMDSv2 enforced. No key pair. No public IP needed — SSM routes via
	// the outbound HTTPS connection the agent initiates. Public IP is kept
	// enabled so the instance can reach ssm.amazonaws.com without NAT,
	// but this can be removed if VpcEndpoints for SSM are present.
	bastionInstanceName := provider.PhysicalName(stage, name, "bastion", stageId)
	bastionInstance, err := ec2.NewInstance(ctx, name+"-bastion", &ec2.InstanceArgs{
		Ami:                      pulumi.String(bastionAMI.Id),
		InstanceType:             pulumi.String(instanceType),
		SubnetId:                 publicSubnet.ID(),
		VpcSecurityGroupIds:      pulumi.StringArray{bastionSG.ID()},
		IamInstanceProfile:       bastionProfile.Name,
		AssociatePublicIpAddress: pulumi.Bool(true),
		UserData:                 pulumi.String(userData),
		MetadataOptions: &ec2.InstanceMetadataOptionsArgs{
			HttpEndpoint:            pulumi.String("enabled"),
			HttpTokens:              pulumi.String("required"), // IMDSv2 only
			HttpPutResponseHopLimit: pulumi.Int(1),
		},
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(bastionInstanceName),
			"ManagedBy": pulumi.String("anvil"),
			// Tag makes it easy to find in the console and target via SSM fleet manager
			"Role": pulumi.String("bastion"),
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create bastion instance: %w", err)
	}

	return bastionInstance, bastionSG, nil
}

// createNatGateways provisions one AWS managed NAT Gateway per AZ.
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

// createNatInstance provisions a single fck-nat EC2 instance.
func createNatInstance(
	ctx *pulumi.Context,
	name, stage, stageId string,
	instanceType string,
	privateCIDRs []string,
	publicSubnet *ec2.Subnet,
	privateRouteTables []*ec2.RouteTable,
	parent pulumi.Resource,
) error {
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

	ingressRules := make(ec2.SecurityGroupIngressArray, len(privateCIDRs))
	for i, cidr := range privateCIDRs {
		ingressRules[i] = &ec2.SecurityGroupIngressArgs{
			Description: pulumi.String(fmt.Sprintf("NAT forwarding from private subnet %d", i+1)),
			FromPort:    pulumi.Int(0),
			ToPort:      pulumi.Int(0),
			Protocol:    pulumi.String("-1"),
			CidrBlocks:  pulumi.StringArray{pulumi.String(cidr)},
		}
	}

	natSGName := provider.PhysicalName(stage, name, "nat-sg", stageId)
	natSG, err := ec2.NewSecurityGroup(ctx, name+"-nat-sg", &ec2.SecurityGroupArgs{
		VpcId:       publicSubnet.VpcId,
		Description: pulumi.String("Security group for Anvil fck-nat instance — inbound from private subnets only"),
		Ingress:     ingressRules,
		Egress: ec2.SecurityGroupEgressArray{
			&ec2.SecurityGroupEgressArgs{
				Description: pulumi.String("Allow all outbound for NAT forwarding"),
				FromPort:    pulumi.Int(0),
				ToPort:      pulumi.Int(0),
				Protocol:    pulumi.String("-1"),
				CidrBlocks:  pulumi.StringArray{pulumi.String("0.0.0.0/0")},
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

	natInstanceName := provider.PhysicalName(stage, name, "nat-instance", stageId)
	natInstance, err := ec2.NewInstance(ctx, name+"-nat-instance", &ec2.InstanceArgs{
		Ami:                      pulumi.String(natAMI.Id),
		InstanceType:             pulumi.String(instanceType),
		SubnetId:                 publicSubnet.ID(),
		VpcSecurityGroupIds:      pulumi.StringArray{natSG.ID()},
		SourceDestCheck:          pulumi.Bool(false),
		AssociatePublicIpAddress: pulumi.Bool(true),
		MetadataOptions: &ec2.InstanceMetadataOptionsArgs{
			HttpEndpoint:            pulumi.String("enabled"),
			HttpTokens:              pulumi.String("required"),
			HttpPutResponseHopLimit: pulumi.Int(1),
		},
		Tags: pulumi.StringMap{
			"Name":      pulumi.String(natInstanceName),
			"ManagedBy": pulumi.String("anvil"),
		},
	}, pulumi.Parent(parent))
	if err != nil {
		return fmt.Errorf("failed to create NAT instance: %w", err)
	}

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
