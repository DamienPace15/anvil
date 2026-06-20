package dsql

import (
	"encoding/json"
	"fmt"
	"strings"

	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/DamienPace15/anvil/provider/internal/vpcsg"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	awsbackup "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/backup"
	awsdsql "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/dsql"
	awsec2 "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// ── Nested arg types ───────────────────────────────────────────────────────

type DSQLMultiRegionArgs struct {
	Regions       []string `pulumi:"regions"`
	WitnessRegion string   `pulumi:"witnessRegion"`
}

type DSQLRoleArgs struct {
	Name   string   `pulumi:"name"`
	Schema string   `pulumi:"schema"`
	Grants []string `pulumi:"grants"`
}

type DSQLVpcArgs struct {
	VpcId            pulumi.StringInput      `pulumi:"vpcId"`
	PrivateSubnetIds pulumi.StringArrayInput `pulumi:"privateSubnetIds"`
	HasNat           bool                    `pulumi:"hasNat,optional"`
}

type DSQLBackupArgs struct {
	RetentionDays      int    `pulumi:"retentionDays,optional"`
	ScheduleExpression string `pulumi:"scheduleExpression,optional"`
	ScheduleTimezone   string `pulumi:"scheduleTimezone,optional"`
	VaultArn           string `pulumi:"vaultArn,optional"`
}

// ── Args ───────────────────────────────────────────────────────────────────

type DSQLArgs struct {
	MultiRegion *DSQLMultiRegionArgs              `pulumi:"multiRegion,optional"`
	Roles       []DSQLRoleArgs                    `pulumi:"roles,optional"`
	Vpc         *DSQLVpcArgs                      `pulumi:"vpc,optional"`
	Backup      *DSQLBackupArgs                   `pulumi:"backup,optional"`
	Transform   map[string]map[string]interface{} `pulumi:"transform,optional"`
}

// ── Component ──────────────────────────────────────────────────────────────

type DSQL struct {
	pulumi.ResourceState

	Endpoints                   pulumi.StringMapOutput `pulumi:"endpoints"`
	ClusterArns                 pulumi.StringMapOutput `pulumi:"clusterArns"`
	VpcEndpointIds              pulumi.StringMapOutput `pulumi:"vpcEndpointIds"`
	VpcEndpointSecurityGroupIds pulumi.StringMapOutput `pulumi:"vpcEndpointSecurityGroupIds"`

	hasVpcEndpoints      bool
	vpcEndpointSgOutputs map[string]pulumi.StringOutput

	// roles stores the roles config so GrantConnect can include it in the
	// bootstrap payload exported as a stack output for the CLI.
	roles []DSQLRoleArgs

	name string
}

func (d *DSQL) Name() string {
	return d.name
}

func (d *DSQL) Annotate(a infer.Annotator) {
	a.SetToken("aws", "DSQL")
	a.Describe(&d, "An Anvil-managed Aurora DSQL cluster. Serverless distributed PostgreSQL-compatible database with optional multi-region active-active replication, IAM authentication, deploy-time role bootstrapping, and AWS Backup integration. No VPC required.")
}

func NewDSQL(ctx *pulumi.Context, name string, args DSQLArgs, opts ...pulumi.ResourceOption) (*DSQL, error) {
	d := &DSQL{name: name}

	provider.NewContext(ctx)

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")

	opts = provider.WithDefault(opts, true)

	if err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, d, opts...); err != nil {
		return nil, err
	}

	// ── 1. Resolve default region ──────────────────────────────────────────
	defaultRegion, err := aws.GetRegion(ctx, &aws.GetRegionArgs{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve AWS region: %w", err)
	}
	providerRegion := defaultRegion.Region

	// ── 2. Determine regions and witness ──────────────────────────────────
	isMultiRegion := args.MultiRegion != nil
	var regions []string
	var witnessRegion string

	if isMultiRegion {
		regions = args.MultiRegion.Regions
		witnessRegion = args.MultiRegion.WitnessRegion
	} else {
		regions = []string{providerRegion}
	}

	// Store roles for use in GrantConnect bootstrap payload.
	d.roles = args.Roles

	// ── 3. Regional providers ──────────────────────────────────────────────
	regionalProviders := make(map[string]*aws.Provider, len(regions))
	for _, region := range regions {
		if region == providerRegion {
			regionalProviders[region] = nil
			continue
		}
		rp, err := aws.NewProvider(ctx, fmt.Sprintf("%s-provider-%s", name, region), &aws.ProviderArgs{
			Region: pulumi.String(region),
		}, pulumi.Parent(d))
		if err != nil {
			return nil, fmt.Errorf("creating regional provider for %s: %w", region, err)
		}
		regionalProviders[region] = rp
	}

	resourceOpts := func(region string) []pulumi.ResourceOption {
		base := []pulumi.ResourceOption{pulumi.Parent(d)}
		if rp := regionalProviders[region]; rp != nil {
			base = append(base, pulumi.Provider(rp))
		}
		return base
	}

	// ── 4. Clusters ────────────────────────────────────────────────────────
	clusters := make(map[string]*awsdsql.Cluster, len(regions))

	for _, region := range regions {
		region := region

		clusterMap := pulumi.Map{
			"tags": pulumi.StringMap{
				"ManagedBy": pulumi.String("anvil"),
			},
		}

		if isMultiRegion {
			clusterMap["multiRegionProperties"] = pulumi.Map{
				"witnessRegion": pulumi.String(witnessRegion),
			}
		}

		clusterProps := transform.MergeTransform(args.Transform["cluster"], clusterMap)
		clusterRes := &awsdsql.Cluster{}

		resourceName := name
		if isMultiRegion {
			resourceName = fmt.Sprintf("%s-%s", name, region)
		}

		clusterOpts := append(resourceOpts(region), pulumi.Protect(true))

		if err := ctx.RegisterResource(
			"aws:dsql/cluster:Cluster",
			resourceName,
			clusterProps,
			clusterRes,
			clusterOpts...,
		); err != nil {
			return nil, fmt.Errorf("creating DSQL cluster in %s: %w", region, err)
		}

		clusters[region] = clusterRes
	}

	// ── 5. ClusterPeering ──────────────────────────────────────────────────
	if isMultiRegion && len(regions) == 2 {
		regionA := regions[0]
		regionB := regions[1]
		clusterA := clusters[regionA]
		clusterB := clusters[regionB]

		if _, err := awsdsql.NewClusterPeering(ctx,
			fmt.Sprintf("%s-peer-%s", name, regionA),
			&awsdsql.ClusterPeeringArgs{
				Identifier:    clusterA.Identifier,
				Clusters:      pulumi.StringArray{clusterB.Arn},
				WitnessRegion: pulumi.String(witnessRegion),
			},
			resourceOpts(regionA)...,
		); err != nil {
			return nil, fmt.Errorf("creating cluster peering for %s: %w", regionA, err)
		}

		if _, err := awsdsql.NewClusterPeering(ctx,
			fmt.Sprintf("%s-peer-%s", name, regionB),
			&awsdsql.ClusterPeeringArgs{
				Identifier:    clusterB.Identifier,
				Clusters:      pulumi.StringArray{clusterA.Arn},
				WitnessRegion: pulumi.String(witnessRegion),
			},
			resourceOpts(regionB)...,
		); err != nil {
			return nil, fmt.Errorf("creating cluster peering for %s: %w", regionB, err)
		}
	}

	// ── 6. VPC endpoints ───────────────────────────────────────────────────
	vpcEndpointIds := pulumi.StringMap{}
	vpcEndpointSgIds := pulumi.StringMap{}
	d.vpcEndpointSgOutputs = make(map[string]pulumi.StringOutput)

	if args.Vpc != nil && !args.Vpc.HasNat {
		for _, region := range regions {
			region := region
			cluster := clusters[region]

			sg, err := vpcsg.CreateSecurityGroup(
				ctx,
				fmt.Sprintf("%s-vpce-sg-%s", name, region),
				stage,
				stageId,
				args.Vpc.VpcId,
				d,
			)
			if err != nil {
				return nil, fmt.Errorf("creating VPC endpoint SG for %s: %w", region, err)
			}

			if err := vpcsg.AddIngressRule(
				ctx,
				fmt.Sprintf("%s-vpce-%s-self-ingress", name, region),
				sg.ID().ToStringOutput(),
				sg.ID().ToStringOutput(),
				5432, 5432,
				"tcp",
				d,
			); err != nil {
				return nil, fmt.Errorf("creating self-referencing ingress for %s: %w", region, err)
			}

			endpointOpts := resourceOpts(region)
			endpoint, err := awsec2.NewVpcEndpoint(ctx,
				fmt.Sprintf("%s-vpce-%s", name, region),
				&awsec2.VpcEndpointArgs{
					VpcId:             args.Vpc.VpcId.ToStringOutput(),
					ServiceName:       cluster.VpcEndpointServiceName,
					VpcEndpointType:   pulumi.String("Interface"),
					PrivateDnsEnabled: pulumi.Bool(true),
					SubnetIds:         args.Vpc.PrivateSubnetIds.ToStringArrayOutput(),
					SecurityGroupIds:  pulumi.StringArray{sg.ID()},
					Tags: pulumi.StringMap{
						"Name":      pulumi.String(provider.PhysicalName(stage, fmt.Sprintf("%s-%s", name, region), "vpce", stageId)),
						"ManagedBy": pulumi.String("anvil"),
					},
				},
				endpointOpts...,
			)
			if err != nil {
				return nil, fmt.Errorf("creating VPC endpoint for %s: %w", region, err)
			}

			vpcEndpointIds[region] = endpoint.ID().ToStringOutput()
			vpcEndpointSgIds[region] = sg.ID().ToStringOutput()
			d.vpcEndpointSgOutputs[region] = sg.ID().ToStringOutput()
		}

		d.hasVpcEndpoints = true
	}

	// ── 7. Backup ──────────────────────────────────────────────────────────
	if args.Backup != nil {
		retentionDays := args.Backup.RetentionDays
		if retentionDays == 0 {
			retentionDays = 35
		}
		scheduleExpr := args.Backup.ScheduleExpression
		if scheduleExpr == "" {
			scheduleExpr = "cron(0 0 * * ? *)"
		}
		scheduleTz := args.Backup.ScheduleTimezone
		if scheduleTz == "" {
			scheduleTz = "Etc/UTC"
		}
		vaultName := "Default"
		if args.Backup.VaultArn != "" {
			parts := strings.Split(args.Backup.VaultArn, ":")
			vaultName = parts[len(parts)-1]
		}

		for _, region := range regions {
			region := region
			if _, err := awsbackup.NewRegionSettings(ctx,
				fmt.Sprintf("%s-backup-settings-%s", name, region),
				&awsbackup.RegionSettingsArgs{
					ResourceTypeOptInPreference: pulumi.BoolMap{
						"DSQL": pulumi.Bool(true),
					},
					ResourceTypeManagementPreference: pulumi.BoolMap{
						"DSQL": pulumi.Bool(true),
					},
				},
				resourceOpts(region)...,
			); err != nil {
				return nil, fmt.Errorf("enabling DSQL backup region settings for %s: %w", region, err)
			}
		}

		var copyActions awsbackup.PlanRuleCopyActionArray
		if isMultiRegion && len(regions) == 2 {
			for _, region := range regions {
				region := region
				destVaultArn := clusters[region].Arn.ApplyT(func(arn string) string {
					parts := strings.Split(arn, ":")
					if len(parts) < 5 {
						return ""
					}
					accountId := parts[4]
					return fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", region, accountId, vaultName)
				}).(pulumi.StringOutput)

				copyActions = append(copyActions, awsbackup.PlanRuleCopyActionArgs{
					DestinationVaultArn: destVaultArn,
					Lifecycle: &awsbackup.PlanRuleCopyActionLifecycleArgs{
						DeleteAfter: pulumi.Int(retentionDays),
					},
				})
			}
		}

		planRule := awsbackup.PlanRuleArgs{
			RuleName:                   pulumi.String(fmt.Sprintf("%s-daily", name)),
			TargetVaultName:            pulumi.String(vaultName),
			Schedule:                   pulumi.String(scheduleExpr),
			ScheduleExpressionTimezone: pulumi.String(scheduleTz),
			Lifecycle: &awsbackup.PlanRuleLifecycleArgs{
				DeleteAfter: pulumi.Int(retentionDays),
			},
		}
		if len(copyActions) > 0 {
			planRule.CopyActions = copyActions
		}

		plan, err := awsbackup.NewPlan(ctx,
			fmt.Sprintf("%s-backup-plan", name),
			&awsbackup.PlanArgs{
				Name:  pulumi.String(provider.PhysicalName(stage, name, "backup-plan", stageId)),
				Rules: awsbackup.PlanRuleArray{planRule},
				Tags: pulumi.StringMap{
					"ManagedBy": pulumi.String("anvil"),
				},
			},
			pulumi.Parent(d),
		)
		if err != nil {
			return nil, fmt.Errorf("creating backup plan: %w", err)
		}

		var clusterArns pulumi.StringArray
		for _, cluster := range clusters {
			clusterArns = append(clusterArns, cluster.Arn)
		}

		backupRole, err := awsiam.NewRole(ctx,
			fmt.Sprintf("%s-backup-role", name),
			&awsiam.RoleArgs{
				Name: pulumi.String(provider.PhysicalName(stage, name, "backup-role", stageId)),
				AssumeRolePolicy: pulumi.String(`{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "backup.amazonaws.com" },
    "Action": "sts:AssumeRole"
  }]
}`),
				ManagedPolicyArns: pulumi.StringArray{
					pulumi.String("arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForBackup"),
				},
				Tags: pulumi.StringMap{
					"ManagedBy": pulumi.String("anvil"),
				},
			},
			pulumi.Parent(d),
		)
		if err != nil {
			return nil, fmt.Errorf("creating backup IAM role: %w", err)
		}

		if _, err := awsbackup.NewSelection(ctx,
			fmt.Sprintf("%s-backup-selection", name),
			&awsbackup.SelectionArgs{
				Name:       pulumi.String(provider.PhysicalName(stage, name, "backup-sel", stageId)),
				PlanId:     plan.ID(),
				IamRoleArn: backupRole.Arn,
				Resources:  clusterArns,
			},
			pulumi.Parent(d),
		); err != nil {
			return nil, fmt.Errorf("creating backup selection: %w", err)
		}
	}

	// ── 8. Outputs ─────────────────────────────────────────────────────────
	// aws.dsql.Cluster has no Endpoint output — construct from Identifier.
	// Pattern: {identifier}.dsql.{region}.on.aws
	endpointMap := pulumi.StringMap{}
	arnMap := pulumi.StringMap{}
	for region, cluster := range clusters {
		region := region
		endpointMap[region] = cluster.Identifier.ApplyT(func(id string) string {
			return fmt.Sprintf("%s.dsql.%s.on.aws", id, region)
		}).(pulumi.StringOutput)
		arnMap[region] = cluster.Arn
	}

	d.Endpoints = endpointMap.ToStringMapOutput()
	d.ClusterArns = arnMap.ToStringMapOutput()
	d.VpcEndpointIds = vpcEndpointIds.ToStringMapOutput()
	d.VpcEndpointSecurityGroupIds = vpcEndpointSgIds.ToStringMapOutput()

	ctx.RegisterResourceOutputs(d, pulumi.Map{
		"endpoints":                   endpointMap,
		"clusterArns":                 arnMap,
		"vpcEndpointIds":              vpcEndpointIds,
		"vpcEndpointSecurityGroupIds": vpcEndpointSgIds,
	})

	return d, nil
}

// ── Grant methods ──────────────────────────────────────────────────────────

// GrantConnect grants dsql:DbConnect on all cluster ARNs to the Lambda's
// execution role, then exports a _dsqlBootstrap_* stack output containing
// the full bootstrap payload for the CLI to read after s.Up() completes.
//
// The CLI creates a short-lived Lambda with dsql:DbConnectAdmin scoped to
// the cluster ARN, invokes it to run the bootstrap SQL, then deletes it.
// The deployer's credentials never need dsql:DbConnectAdmin.
//
// When vpc endpoints were created (hasNat false), also wires egress rules
// from the Lambda's SG to each endpoint SG on port 5432.
func (d *DSQL) GrantConnect(
	ctx *pulumi.Context,
	lambda provider.GrantTarget,
	dbRole string,
) error {
	// ── IAM policy — dsql:DbConnect on all cluster ARNs ───────────────────
	policyJSON := d.ClusterArns.ApplyT(func(arns map[string]string) (string, error) {
		resources := make([]string, 0, len(arns))
		for _, arn := range arns {
			resources = append(resources, arn)
		}

		type statement struct {
			Effect   string   `json:"Effect"`
			Action   []string `json:"Action"`
			Resource []string `json:"Resource"`
		}
		type doc struct {
			Version   string      `json:"Version"`
			Statement []statement `json:"Statement"`
		}

		b, err := json.Marshal(doc{
			Version: "2012-10-17",
			Statement: []statement{{
				Effect:   "Allow",
				Action:   []string{"dsql:DbConnect"},
				Resource: resources,
			}},
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal dsql:DbConnect policy: %w", err)
		}
		return string(b), nil
	}).(pulumi.StringOutput)

	roleNameOutput := lambda.RoleARN().ApplyT(func(arn string) string {
		for i := len(arn) - 1; i >= 0; i-- {
			if arn[i] == '/' {
				return arn[i+1:]
			}
		}
		return arn
	}).(pulumi.StringOutput)

	if _, err := awsiam.NewRolePolicy(ctx,
		fmt.Sprintf("%s-%s-dsql-connect", d.name, lambda.Name()),
		&awsiam.RolePolicyArgs{
			Role:   roleNameOutput,
			Policy: policyJSON,
		},
		pulumi.Parent(d),
	); err != nil {
		return fmt.Errorf("granting dsql:DbConnect to %s: %w", lambda.Name(), err)
	}

	// ── Bootstrap payload stack export ────────────────────────────────────
	// Exported as _dsqlBootstrap_{dsqlName}_{lambdaName} stack output.
	// CLI reads all _dsqlBootstrap_* outputs after s.Up(), groups by cluster
	// endpoint, and invokes one ephemeral bootstrap Lambda per cluster.
	// Hash stored in .anvil/dsql-bootstrap-hashes-{stage}.json — only runs
	// when payload changes. Hash only written on successful invocation.
	rolesJSON, _ := json.Marshal(d.roles)

	type bootstrapPayloadType struct {
		DSQLName   string          `json:"dsqlName"`
		Endpoint   string          `json:"endpoint"`
		Region     string          `json:"region"`
		ClusterArn string          `json:"clusterArn"`
		Roles      json.RawMessage `json:"roles"`
		DbRole     string          `json:"dbRole"`
		IamRoleArn string          `json:"iamRoleArn"`
	}

	bootstrapKey := fmt.Sprintf("_dsqlBootstrap_%s_%s", d.name, lambda.Name())

	// Extract primary endpoint, region and cluster ARN from the maps.
	// For single-region there is one entry. For multi-region we use the
	// first entry — AWS IAM GRANT replicates automatically to peer regions.
	primaryEndpoint := d.Endpoints.ApplyT(func(m map[string]string) string {
		for _, v := range m {
			return v
		}
		return ""
	}).(pulumi.StringOutput)

	primaryRegion := d.Endpoints.ApplyT(func(m map[string]string) string {
		for k := range m {
			return k
		}
		return ""
	}).(pulumi.StringOutput)

	primaryClusterArn := d.ClusterArns.ApplyT(func(m map[string]string) string {
		for _, v := range m {
			return v
		}
		return ""
	}).(pulumi.StringOutput)

	bootstrapPayload := pulumi.All(
		primaryEndpoint,
		primaryRegion,
		primaryClusterArn,
		lambda.RoleARN(),
	).ApplyT(func(args []interface{}) string {
		endpoint := args[0].(string)
		region := args[1].(string)
		clusterArn := args[2].(string)
		iamRoleArn := args[3].(string)

		b, _ := json.Marshal(bootstrapPayloadType{
			DSQLName:   d.name,
			Endpoint:   endpoint,
			Region:     region,
			ClusterArn: clusterArn,
			Roles:      json.RawMessage(rolesJSON),
			DbRole:     dbRole,
			IamRoleArn: iamRoleArn,
		})
		return string(b)
	}).(pulumi.StringOutput)

	ctx.Export(bootstrapKey, bootstrapPayload)

	// ── VPC egress rules ──────────────────────────────────────────────────
	// Only when interface endpoints were created during component construction.
	// Iterates vpcEndpointSgOutputs — plain map of region → StringOutput.
	// Cannot use VpcEndpointSecurityGroupIds (StringMapOutput) here because
	// Pulumi resources cannot be created inside an ApplyT.
	if d.hasVpcEndpoints {
		for region, sgIdOutput := range d.vpcEndpointSgOutputs {
			if err := vpcsg.AddEgressRule(
				ctx,
				fmt.Sprintf("%s-%s-vpce-egress-%s", d.name, lambda.Name(), region),
				lambda.SecurityGroupId().ToStringOutput(),
				sgIdOutput,
				5432, 5432,
				"tcp",
				d,
			); err != nil {
				return fmt.Errorf("wiring egress to DSQL endpoint SG in %s: %w", region, err)
			}
		}
	}

	return nil
}

// ── Doc notes ──────────────────────────────────────────────────────────────
//
// BOOTSTRAP SECURITY MODEL (docs):
//   The CI/CD pipeline never needs dsql:DbConnectAdmin. The bootstrap Lambda's
//   execution role has dsql:DbConnectAdmin scoped to the specific cluster ARN
//   only. The Lambda exists for ~15-30 seconds and is deleted immediately.
//   Pipeline credentials only need Lambda management + iam:CreateRole/PassRole.
//
// BOOTSTRAP PAYLOAD (docs):
//   ctx.Export() in GrantConnect registers _dsqlBootstrap_{dsqlName}_{lambdaName}
//   as a Pulumi stack output. The CLI reads these via s.Outputs() after s.Up().
//   Each output contains: endpoint, region, clusterArn, roles[], dbRole, iamRoleArn.
//   The CLI groups by endpoint (one Lambda invocation per cluster) and only
//   re-runs when the SHA256 hash of the payload changes.
//
// SINGLE-REGION VS MULTI-REGION (docs):
//   Single-region is the default — zero config required.
//   Multi-region is opt-in via the multiRegion block.
//   Bootstrap Lambda connects to the primary region endpoint only —
//   AWS IAM GRANT replicates automatically to peer regions.
//
// OCC RETRY BEHAVIOR (docs):
//   DSQL uses Optimistic Concurrency Control. Applications must implement
//   retry logic for transaction aborts at commit time.
//
// ALTER DEFAULT PRIVILEGES NOT SUPPORTED (docs):
//   Aurora DSQL does not support ALTER DEFAULT PRIVILEGES. Grants only apply
//   to tables that exist at bootstrap time. Re-deploy after adding new tables.
//
// VPC ENDPOINT SERVICE NAME (implementation note):
//   DSQL endpoint service name is cluster-specific: com.amazonaws.{region}.dsql-{hex}.
//   Cannot use the existing AwsVpcEndpointService enum or VpcEndpoint component.
//
// BACKUP CROSS-REGION COPY (docs):
//   AWS Backup does not automatically replicate. Anvil wires copy rules
//   automatically when multiRegion is set.
//
// PRIVATELINK PORT (docs):
//   DSQL VPC endpoints use port 5432, not 443. Do not change.
//
// GRANTCONNECT AND VPC SG ATTACHMENT (docs):
//   GrantConnect cannot attach the endpoint SG to the Lambda's vpcConfig —
//   set at Lambda construction time. User must declare DSQL endpoint SGs
//   in vpc.vpcEndpoints on the Lambda when constructing it.
