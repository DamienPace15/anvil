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

// DSQLMultiRegionArgs configures multi-region active-active replication.
// When set, Anvil creates one cluster per region and links them via
// ClusterPeering. Both regions accept reads and writes with strong consistency.
// When omitted, a single-region cluster is created in the provider's default region.
type DSQLMultiRegionArgs struct {
	Regions       []string `pulumi:"regions"`
	WitnessRegion string   `pulumi:"witnessRegion"`
}

// DSQLRoleArgs defines a database role to bootstrap at deploy time.
// Anvil connects as admin using the deployer's credentials and runs the
// required SQL. Only re-runs when role definitions change — idempotent
// via Pulumi state diffing.
type DSQLRoleArgs struct {
	// Name is the Postgres role name. Must be a valid PostgreSQL identifier.
	Name string `pulumi:"name"`

	// Schema is the Postgres schema this role operates in.
	// Anvil creates it if it does not exist and grants USAGE.
	// Must not be "public" — public schema is owned by admin.
	Schema string `pulumi:"schema"`

	// Grants are the table-level privileges to apply on all current
	// and future tables in the schema.
	Grants []string `pulumi:"grants"`
}

// DSQLVpcArgs configures VPC placement and network routing for DSQL.
// When set without HasNat, Anvil creates interface VPC endpoints (PrivateLink)
// per region so traffic stays on the AWS backbone.
// When HasNat is true, endpoint creation is skipped — traffic routes via
// the existing NAT gateway to the public DSQL endpoint.
// Anvil never creates the NAT gateway.
type DSQLVpcArgs struct {
	VpcId            pulumi.StringInput      `pulumi:"vpcId"`
	PrivateSubnetIds pulumi.StringArrayInput `pulumi:"privateSubnetIds"`
	HasNat           bool                    `pulumi:"hasNat,optional"`
}

// DSQLBackupArgs configures AWS Backup for the cluster(s).
// Anvil opts in the DSQL resource type per region, creates a backup plan,
// and for multi-region clusters automatically adds cross-region copy rules.
type DSQLBackupArgs struct {
	RetentionDays      int    `pulumi:"retentionDays,optional"`
	ScheduleExpression string `pulumi:"scheduleExpression,optional"`
	ScheduleTimezone   string `pulumi:"scheduleTimezone,optional"`
	VaultArn           string `pulumi:"vaultArn,optional"`
}

// ── Args ───────────────────────────────────────────────────────────────────

type DSQLArgs struct {
	// MultiRegion enables multi-region active-active replication.
	// When omitted, a single-region cluster is created in the provider's
	// default region.
	MultiRegion *DSQLMultiRegionArgs `pulumi:"multiRegion,optional"`

	// Roles are the database roles to bootstrap at deploy time.
	// When omitted, no SQL is run — schema management is left to the user.
	Roles []DSQLRoleArgs `pulumi:"roles,optional"`

	// Vpc configures VPC placement and network routing.
	// When omitted, the Lambda connects to the public DSQL endpoint.
	Vpc *DSQLVpcArgs `pulumi:"vpc,optional"`

	// Backup configures AWS Backup for the cluster(s).
	// When omitted, no backup resources are created.
	Backup *DSQLBackupArgs `pulumi:"backup,optional"`

	// Transform allows low-level overrides of the underlying aws.dsql.Cluster.
	// Keys: "cluster". Use for KMS key ARN and other cluster-level settings
	// not exposed by the Anvil API.
	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

// ── Component ──────────────────────────────────────────────────────────────

type DSQL struct {
	pulumi.ResourceState

	// Endpoints is a map of region to cluster endpoint.
	// Single-region: one entry keyed by the provider region.
	// Multi-region: one entry per region.
	Endpoints pulumi.StringMapOutput `pulumi:"endpoints"`

	// ClusterArns is a map of region to cluster ARN.
	ClusterArns pulumi.StringMapOutput `pulumi:"clusterArns"`

	// VpcEndpointIds is a map of region to VPC endpoint ID.
	// Only populated when vpc is set and hasNat is false.
	VpcEndpointIds pulumi.StringMapOutput `pulumi:"vpcEndpointIds"`

	// VpcEndpointSecurityGroupIds is a map of region to endpoint SG ID.
	// Only populated when vpc is set and hasNat is false.
	// Pass to LambdaVpcEndpointArgs.securityGroupId.
	VpcEndpointSecurityGroupIds pulumi.StringMapOutput `pulumi:"vpcEndpointSecurityGroupIds"`

	// hasVpcEndpoints tracks whether interface endpoints were created.
	// Used by GrantConnect to decide whether to wire SG egress rules.
	hasVpcEndpoints bool

	// vpcEndpointSgOutputs stores endpoint SG IDs keyed by region as plain
	// StringOutputs. Populated during construction when vpc is set and hasNat
	// is false. Used by GrantConnect to wire egress rules at graph construction
	// time — cannot use VpcEndpointSecurityGroupIds (StringMapOutput) for this
	// because Pulumi resources cannot be created inside an ApplyT.
	vpcEndpointSgOutputs map[string]pulumi.StringOutput

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
	// Used for single-region clusters and as the output map key.
	// Safe to call outside Apply — synchronous SDK call.
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

	// ── 3. Regional providers ──────────────────────────────────────────────
	// One provider per region. Reuses the default provider for the stack's
	// default region to avoid creating a redundant provider.
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

	// resourceOpts returns the correct provider option for a given region.
	// Always includes pulumi.Parent(d) for correct resource hierarchy.
	resourceOpts := func(region string) []pulumi.ResourceOption {
		base := []pulumi.ResourceOption{pulumi.Parent(d)}
		if rp := regionalProviders[region]; rp != nil {
			base = append(base, pulumi.Provider(rp))
		}
		return base
	}

	// ── 4. Clusters ────────────────────────────────────────────────────────
	// Single-region: one cluster, no multiRegionProperties.
	// Multi-region: two clusters each with witnessRegion set.
	// Both enter PENDING_SETUP — peering (phase 5) completes the link.
	clusters := make(map[string]*awsdsql.Cluster, len(regions))

	for _, region := range regions {

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
	// Only for multi-region. Creates two ClusterPeering resources that
	// cross-reference each other's ARN. Pulumi waits for both clusters
	// before running either peering resource — no circular dependency.
	// After peering: PENDING_SETUP → CREATING → ACTIVE.
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
	// Only when vpc is set and hasNat is false.
	// One interface endpoint per region with a dedicated SG.
	// Self-referencing ingress on port 5432 (Postgres).
	// grantConnect wires the egress side on the Lambda SG.
	//
	// The endpoint service name is cluster-specific:
	// com.amazonaws.{region}.dsql-{hex} — exposed as cluster.VpcEndpointServiceName.
	// This differs from standard AWS services which have fixed service names,
	// which is why DSQL cannot use the existing VpcEndpoint component or enum.
	vpcEndpointIds := pulumi.StringMap{}
	vpcEndpointSgIds := pulumi.StringMap{}
	d.vpcEndpointSgOutputs = make(map[string]pulumi.StringOutput)

	if args.Vpc != nil && !args.Vpc.HasNat {
		for _, region := range regions {
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
	// Only when backup block is set.
	// Opts in DSQL per region, creates a plan with schedule and retention.
	// Multi-region: cross-region copy rules are wired automatically so
	// restores work in both regions — AWS Backup does not replicate by default.
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

		// Opt in DSQL resource type per region — required before AWS Backup
		// can protect DSQL clusters in that region.
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

		// Cross-region copy actions for multi-region clusters.
		// One copy action per region so each region has its own recovery point.
		// Without this, multi-region restore fails because the peer region
		// has no recovery point to restore from.
		var copyActions awsbackup.PlanRuleCopyActionArray
		if isMultiRegion && len(regions) == 2 {
			for _, region := range regions {
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

		// Collect all cluster ARNs for the backup selection resource.
		var clusterArns pulumi.StringArray
		for _, cluster := range clusters {
			clusterArns = append(clusterArns, cluster.Arn)
		}

		// IAM role for AWS Backup to access DSQL clusters.
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

	// ── 8. Bootstrap ───────────────────────────────────────────────────────
	// Only when roles is set and non-empty.
	// Implemented in bootstrap.go — DSQLBootstrap dynamic provider.
	// Connects as admin using the deployer's dsql:DbConnectAdmin credentials.
	// Runs: CREATE SCHEMA IF NOT EXISTS, CREATE ROLE WITH LOGIN,
	//       AWS IAM GRANT, GRANT USAGE ON SCHEMA,
	//       GRANT ON ALL TABLES, ALTER DEFAULT PRIVILEGES.
	// For multi-region: depends on peering completing (clusters must be ACTIVE).
	// Idempotent — Pulumi state diff prevents re-run when inputs unchanged.
	if len(args.Roles) > 0 {
		// TODO: wire DSQLBootstrap dynamic provider once bootstrap.go is implemented.
		// Blocked on: clusters ACTIVE (after peering for multi-region).
		// Requires: github.com/jackc/pgx/v5, aws-sdk-go-v2 dsql auth package.
		_ = args.Roles
	}

	// ── 9. Outputs ─────────────────────────────────────────────────────────
	// The standard aws provider does not expose an Endpoint output field on
	// aws.dsql.Cluster. The endpoint follows a fixed pattern:
	// {identifier}.dsql.{region}.on.aws
	// Constructed from the Identifier output and the known region string.
	endpointMap := pulumi.StringMap{}
	arnMap := pulumi.StringMap{}
	for region, cluster := range clusters {
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

// GrantConnect grants dsql:DbConnect on all cluster ARNs to the given Lambda's
// execution role. Allows the Lambda to generate IAM auth tokens and connect
// to the cluster as the mapped database role.
//
// Never grants dsql:DbConnectAdmin — admin access is only used by the
// bootstrap resource at deploy time using the deployer's own credentials.
//
// When vpc endpoints were created (hasNat false), also wires egress rules
// from the Lambda's SG to each endpoint SG on port 5432.
//
// The Lambda must declare the endpoint SGs in vpc.vpcEndpoints at construction
// time — GrantConnect cannot attach SGs to an already-constructed Lambda.
//
// dbRole is informational — it documents which database role this Lambda
// connects as. The actual role mapping is wired by the bootstrap resource.
func (d *DSQL) GrantConnect(
	ctx *pulumi.Context,
	lambda provider.GrantTarget,
	dbRole string,
) error {
	// Build dsql:DbConnect IAM policy scoped to all cluster ARNs.
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

	// Extract role name from ARN for iam.NewRolePolicy.
	// ARN format: arn:aws:iam::{account}:role/{name}
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

	// Wire egress rules from Lambda SG to each endpoint SG on port 5432.
	// Only when interface endpoints were created during component construction.
	// Iterates d.vpcEndpointSgOutputs — a plain map of region → StringOutput
	// populated at construction time. Cannot use VpcEndpointSecurityGroupIds
	// (StringMapOutput) here because Pulumi resources cannot be created inside
	// an ApplyT.
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
// ADMIN CREDENTIALS AT DEPLOY TIME (docs + error handling):
//   The bootstrap resource requires dsql:DbConnectAdmin on both cluster ARNs
//   at deploy time. This is satisfied by the deployer's IAM identity — whoever
//   runs `anvil deploy`. If the connection fails, surface the required
//   permission and cluster ARN explicitly — raw pgx auth errors are opaque.
//
// SINGLE-REGION VS MULTI-REGION (docs):
//   Single-region is the default — zero config required for a working cluster.
//   Multi-region is opt-in via the multiRegion block. Tradeoffs:
//   ~$43-87/month in PrivateLink costs (if VPC is used), cross-region data
//   transfer charges, and OCC retry handling at the application layer.
//   Document this clearly so users make an informed choice.
//
// OCC RETRY BEHAVIOR (docs):
//   DSQL uses Optimistic Concurrency Control. Under contention, transactions
//   abort at commit time rather than blocking. Applications must implement
//   retry logic — fundamentally different from standard Postgres locking.
//   Recommend surfacing this in the component description and linking to
//   the DSQL OCC documentation. Consider emitting cluster endpoints as env
//   vars named DSQL_ENDPOINT_{REGION} so Lambda code can identify the db type.
//
// BOOTSTRAP IDEMPOTENCY (docs):
//   The bootstrap resource only re-runs when the roles block changes — Pulumi
//   state diffing handles this. CREATE ROLE IF NOT EXISTS and
//   CREATE SCHEMA IF NOT EXISTS are safety nets for manual intervention
//   scenarios. ALTER DEFAULT PRIVILEGES is critical — without it, grants only
//   apply to tables that exist at bootstrap time. New tables added later
//   are automatically accessible because of this statement.
//
// VPC ENDPOINT SERVICE NAME (implementation note):
//   Unlike SSM, SQS, and other standard services where the service name is
//   fixed (com.amazonaws.{region}.sqs), DSQL's service name is cluster-specific:
//   com.amazonaws.{region}.dsql-{hex}. It is exposed as cluster.VpcEndpointServiceName.
//   This is why DSQL cannot use the existing AwsVpcEndpointService enum or
//   the existing VpcEndpoint component.
//
// BACKUP CROSS-REGION COPY (docs):
//   AWS Backup does not automatically replicate across regions. Without a
//   cross-region copy rule, multi-region restore fails because the peer region
//   has no recovery point. Anvil wires copy rules automatically when multiRegion
//   is set. Single-region clusters only get a local recovery point.
//
// PRIVATELINK PORT (docs):
//   DSQL VPC endpoints use port 5432 (Postgres wire protocol), not 443 like
//   most AWS service endpoints (SSM, SQS, Secrets Manager). The self-referencing
//   ingress rule and Lambda egress rules are wired on 5432. Do not change to 443.
//
// ROLES OPTIONAL (docs):
//   When roles is omitted, no SQL runs at deploy time. Intended for teams using
//   migration tools (Atlas, Flyway) or manual bootstrapping. GrantConnect still
//   works — it only grants IAM-level dsql:DbConnect. The database-level role
//   mapping (AWS IAM GRANT) must be handled separately when roles is omitted.
//
// GRANTCONNECT AND VPC SG ATTACHMENT (docs):
//   GrantConnect wires the egress rule from the Lambda SG to the DSQL endpoint
//   SG. However, it cannot attach the endpoint SG to the Lambda's vpcConfig —
//   that is set at Lambda construction time and cannot be modified post-deploy
//   without replacing the function. The user must declare the DSQL endpoint SGs
//   in vpc.vpcEndpoints on the Lambda when constructing it.
