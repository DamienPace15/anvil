package lambda

import (
	"encoding/json"
	"fmt"
	"os"

	provider "github.com/DamienPace15/anvil/provider/internal/shared"
	"github.com/DamienPace15/anvil/provider/internal/transform"
	"github.com/DamienPace15/anvil/provider/internal/vpcsg"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	c "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Compile-time check: Lambda must implement provider.GrantTarget
var _ provider.GrantTarget = (*Lambda)(nil)

// LambdaPermission defines an inline IAM permission for the Lambda execution role.
// Use this for ad-hoc permissions not covered by the grant system.
//
// Example:
//
//	permissions: [
//	    { actions: ["s3:GetObject", "s3:GetObjectVersion"], resource: bucket.arn },
//	    { actions: ["s3:GetObject"], resource: bucket.arn, path: "/uploads" },
//	]
type LambdaPermission struct {
	// Actions is the list of IAM actions to grant.
	Actions []string `pulumi:"actions"`

	// Resource is the ARN of the resource to grant access to.
	// Accepts plain strings and Pulumi outputs (e.g. bucket.arn).
	Resource interface{} `pulumi:"resource"`

	// Path is an optional path suffix appended to the resource ARN.
	// e.g. path: "/uploads" → resource ARN + "/uploads/*"
	Path string `pulumi:"path,optional"`
}

// LambdaVpcEndpointArgs defines a VPC endpoint to grant this Lambda access to.
// Pass ep.securityGroupId and ep.endpointId from an Anvil VpcEndpoint.
//
// Usage:
//
//	vpcEndpoints: [
//	    { securityGroupId: ssm.securityGroupId, endpointId: ssm.endpointId },
//	    { securityGroupId: secrets.securityGroupId, endpointId: secrets.endpointId },
//	]
type LambdaVpcEndpointArgs struct {
	// SecurityGroupId is the endpoint's dedicated SG ID.
	// Use ep.securityGroupId from an Anvil VpcEndpoint.
	SecurityGroupId string `pulumi:"securityGroupId"`

	// EndpointId is the AWS VPC endpoint ID, e.g. vpce-0abc1234567890abc.
	// Used for SG rule naming — rules are named ${lambda}-${endpointId}-endpoint-egress.
	// Use ep.endpointId from an Anvil VpcEndpoint.
	EndpointId string `pulumi:"endpointId"`
}

// LambdaVpcCidrArgs defines a CIDR-scoped egress rule.
// One SecurityGroupEgressRule is created per port per CIDR.
//
// Usage:
//
//	cidrs: [
//	    { range: "10.0.0.0/8", ports: [5432] },         // RDS in peered VPC
//	    { range: "172.16.0.0/12", ports: [6379, 6380] }, // ElastiCache cluster
//	]
type LambdaVpcCidrArgs struct {
	// Range is the IPv4 CIDR block to allow outbound traffic to.
	// e.g. "10.0.0.0/8", "203.0.113.0/24"
	Range string `pulumi:"range"`

	// Ports is the list of TCP ports to allow to this CIDR.
	// Required — be explicit about which ports each range needs.
	Ports []int `pulumi:"ports"`
}

// LambdaVpcArgs defines the VPC placement and network access configuration
// for a Lambda. All network access is declared here at construction time —
// nothing is reachable until explicitly declared.
//
// Network access patterns:
//   - vpcEndpoints: AWS services via PrivateLink (port 443, stays on AWS backbone)
//   - hasNat: internet via NAT Gateway or fck-nat (port 443 to 0.0.0.0/0)
//   - cidrs: specific CIDR ranges at specific ports (peered VPCs, on-premise)
type LambdaVpcArgs struct {
	// VpcId is the ID of the VPC to place the Lambda in.
	VpcId string `pulumi:"vpcId"`

	// PrivateSubnetIds are the IDs of the private subnets to attach the Lambda to.
	// Always private — Lambda must never be placed in public subnets.
	PrivateSubnetIds []string `pulumi:"privateSubnetIds"`

	// HasNat indicates whether this VPC has a NAT Gateway or fck-nat instance.
	// When true, Anvil adds an internet egress rule (port 443) automatically.
	// Only needed for imported VPCs — omit when using an Anvil Vpc component.
	HasNat bool `pulumi:"hasNat,optional"`

	// VpcEndpoints are the VPC endpoints this Lambda needs access to.
	// Anvil wires both sides of the SG rules automatically at construction time.
	// Pass ep.securityGroupId and ep.endpointId from each VpcEndpoint.
	VpcEndpoints []LambdaVpcEndpointArgs `pulumi:"vpcEndpoints,optional"`

	// CIDRs defines structured CIDR-scoped egress rules.
	// Use for peered VPCs, on-premise ranges, or any non-AWS-service destination.
	// One SecurityGroupEgressRule is created per port per CIDR entry.
	// Ports are required per entry — be explicit about what each range needs.
	CIDRs []LambdaVpcCidrArgs `pulumi:"cidrs,optional"`
}

// lambdaEndpointTarget adapts LambdaVpcEndpointArgs to satisfy vpcsg.EndpointTarget.
// Normalises the user-supplied endpoint args into what vpcsg.GrantEndpointAccess needs.
type lambdaEndpointTarget struct {
	args LambdaVpcEndpointArgs
}

func (t *lambdaEndpointTarget) Name() string {
	return t.args.EndpointId
}

func (t *lambdaEndpointTarget) SecurityGroupOutput() pulumi.StringOutput {
	return pulumi.String(t.args.SecurityGroupId).ToStringOutput()
}

// LambdaArgs defines the inputs for an Anvil-managed Lambda function.
//
// Tier 1 controls (always on, free):
//   - Least-privilege IAM execution role
//   - CloudWatch log group with 1y retention default
//   - arm64 (Graviton) architecture
//   - No public function URL
//
// Tier 2 controls incur cost and must be explicitly opted into.
type LambdaArgs struct {
	// Entry is the path to the function's entry point file, relative to the
	// project root. e.g. "functions/api/index.ts".
	// Omit to deploy a blank Lambda placeholder.
	Entry string `pulumi:"entry,optional"`

	// Runtime is the Lambda runtime identifier.
	// e.g. "nodejs20.x", "nodejs18.x", "go1.x", "python3.12", "provided.al2023"
	Runtime string `pulumi:"runtime,optional"`

	// Handler is the exported function name in the entry file.
	// e.g. "handler" for a function exported as `export const handler = ...`
	Handler string `pulumi:"handler,optional"`

	// Memory is the amount of memory in MB. Default: 1024.
	// Valid values: 128 to 32768 in 1MB increments.
	Memory int `pulumi:"memory,optional"`

	// Timeout is the function timeout in seconds. Default: 5.
	// Valid values: 1 to 900.
	Timeout int `pulumi:"timeout,optional"`

	// Environment variables available to the function at runtime.
	// Supports plain strings and Pulumi outputs (e.g. database connection strings).
	Environment map[string]string `pulumi:"environment,optional"`

	// Architecture is the CPU architecture. Default: "arm64" (Graviton — 20% cheaper).
	// Set to "x86_64" for x86-specific native dependencies.
	Architecture string `pulumi:"architecture,optional"`

	// URL enables a direct HTTPS endpoint for the function.
	// Auth mode is AWS_IAM — the endpoint is never public.
	// Default: false.
	URL bool `pulumi:"url,optional"`

	// Vpc places the Lambda inside a VPC for access to private resources.
	// Declare vpcEndpoints, hasNat, and cidrs to wire network access at
	// construction time. Nothing is reachable until explicitly declared.
	Vpc *LambdaVpcArgs `pulumi:"vpc,optional"`

	// Permissions defines inline IAM permissions added to the execution role.
	// Use this for ad-hoc access not covered by the grant system.
	Permissions []LambdaPermission `pulumi:"permissions,optional"`

	// LogRetention sets the CloudWatch log group retention period.
	// Presets: "7d" | "30d" | "90d" | "1y" | "3y" | "6y" | "7y"
	// Default: "1y" (satisfies SOC 2, ISO 27001, PCI-DSS baseline)
	LogRetention string `pulumi:"logRetention,optional"`

	// Tracing enables AWS X-Ray tracing. Incurs per-trace cost (~$5/million traces).
	// Default: false.
	Tracing bool `pulumi:"tracing,optional"`

	// Encryption upgrades environment variable encryption from the default
	// AWS managed key to a customer-managed KMS key. Incurs KMS API call cost.
	// Set to "kms" to enable. Required by HIPAA, PCI-DSS, FedRAMP.
	// Supply kmsKeyArn via transform["lambda"].
	Encryption string `pulumi:"encryption,optional"`

	Transform map[string]map[string]interface{} `pulumi:"transform,optional"`
}

type Lambda struct {
	pulumi.ResourceState

	Arn             pulumi.StringOutput `pulumi:"arn"`
	FunctionName    pulumi.StringOutput `pulumi:"functionName"`
	RoleArn         pulumi.StringOutput `pulumi:"roleArn"`
	FunctionUrl     pulumi.StringOutput `pulumi:"functionUrl"`
	SecurityGroupID pulumi.StringOutput `pulumi:"securityGroupId"`
	name            string
}

// Name implements provider.GrantTarget.
func (l *Lambda) Name() string {
	return l.name
}

// RoleARN implements provider.GrantTarget.
func (l *Lambda) RoleARN() pulumi.StringOutput {
	return l.RoleArn
}

// SecurityGroupId implements provider.GrantTarget.
// Returns an empty StringOutput if the Lambda is not VPC-attached.
func (l *Lambda) SecurityGroupId() pulumi.StringOutput {
	return l.SecurityGroupID
}

func (l *Lambda) Annotate(a infer.Annotator) {
	a.SetToken("aws", "Lambda")
	a.Describe(&l, "An Anvil-managed Lambda function. Tier 1 controls (least-privilege role, CloudWatch logs with 1y retention, arm64 architecture) are enforced automatically at no cost, satisfying the technical baseline of CIS Benchmarks, ISO 27017, SOC 2, NIST 800-53, and ISO 27001.")
}

// resolveDays converts a log retention preset to CloudWatch retention days.
func resolveDays(preset string) int {
	switch preset {
	case "7d":
		return 7
	case "30d":
		return 30
	case "90d":
		return 90
	case "1y":
		return 365
	case "3y":
		return 1095
	case "6y":
		return 2190
	case "7y":
		return 2555
	default:
		return 365 // default: 1 year
	}
}

// sanitizeCIDR converts a CIDR like "10.0.0.0/8" to "10-0-0-0-8"
// for use in resource names.
func sanitizeCIDR(cidr string) string {
	result := make([]byte, len(cidr))
	for i := 0; i < len(cidr); i++ {
		c := cidr[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') {
			result[i] = c
		} else {
			result[i] = '-'
		}
	}
	return string(result)
}

// blankHandler is the placeholder deployed when no entry is provided.
const blankHandler = `exports.handler = async () => ({
  statusCode: 200,
  body: JSON.stringify({
    message: "This Lambda has no code deployed yet. Deploy your function code to replace this placeholder.",
    anvil: true,
  }),
});
`

func NewLambda(ctx *pulumi.Context, name string, args LambdaArgs, opts ...pulumi.ResourceOption) (*Lambda, error) {
	// ── Build mode intercept ───────────────────────────
	// When ANVIL_BUILD_MODE=true, write this function's metadata to the
	// build manifest instead of registering Pulumi resources. The CLI
	// reads the manifest to discover functions that need bundling before
	// running the real deploy. See ANVIL-L002.
	if os.Getenv("ANVIL_BUILD_MODE") == "true" {
		if args.Entry != "" {
			if err := appendToManifest(name, args); err != nil {
				return nil, fmt.Errorf("build manifest write failed for %s: %w", name, err)
			}
		}
		return &Lambda{name: name}, nil
	}

	l := &Lambda{name: name}

	provider.NewContext(ctx)

	cfg := c.New(ctx, "anvil")
	stage := cfg.Require("stage")
	stageId := cfg.Require("stageId")
	zipPath := cfg.Get(fmt.Sprintf("fn-%s", name))

	physicalName := provider.PhysicalName(stage, name, "lambda", stageId)

	// ── Defaults ───────────────────────────────────────
	memory := args.Memory
	if memory == 0 {
		memory = 1024
	}

	timeout := args.Timeout
	if timeout == 0 {
		timeout = 5
	}

	architecture := args.Architecture
	if architecture == "" {
		architecture = "arm64"
	}

	runtime := args.Runtime

	handler := args.Handler
	if handler == "" {
		handler = "handler"
	}

	logRetentionDays := resolveDays(args.LogRetention)

	tracingMode := "PassThrough"
	if args.Tracing {
		tracingMode = "Active"
	}

	opts = provider.WithDefault(opts, true)

	err := ctx.RegisterComponentResource(p.GetTypeToken(ctx), name, l, opts...)
	if err != nil {
		return nil, err
	}

	// ── 1. CloudWatch Log Group ────────────────────────
	// Tier 1: always created before the function so logs are captured
	// from the very first invocation. Retention defaults to 1y which
	// satisfies SOC 2, ISO 27001, and PCI-DSS baseline requirements.
	logGroupName := fmt.Sprintf("/aws/lambda/%s", physicalName)
	logGroupProps := transform.MergeTransform(args.Transform["logGroup"], pulumi.Map{
		"name":            pulumi.String(logGroupName),
		"retentionInDays": pulumi.Int(logRetentionDays),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	})
	logGroup := &cloudwatch.LogGroup{}
	err = ctx.RegisterResource("aws:cloudwatch/logGroup:LogGroup", name+"-logs", logGroupProps, logGroup, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	// ── 2. IAM Execution Role ──────────────────────────
	// Tier 1: least-privilege role. Only CloudWatch Logs access is
	// attached by default. Additional permissions come via the grant
	// system (bucket.GrantRead etc.) or the permissions field.
	assumeRolePolicy := `{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": { "Service": "lambda.amazonaws.com" },
			"Action": "sts:AssumeRole"
		}]
	}`

	role := &iam.Role{}
	roleProps := transform.MergeTransform(args.Transform["role"], pulumi.Map{
		"name":             pulumi.String(provider.PhysicalName(stage, name, "role", stageId)),
		"assumeRolePolicy": pulumi.String(assumeRolePolicy),
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	})
	err = ctx.RegisterResource("aws:iam/role:Role", name+"-role", roleProps, role, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	// ── 3. Basic execution policy ──────────────────────
	// Grants CloudWatch Logs write access. Required for any Lambda.
	// When VPC-attached, AWSLambdaVPCAccessExecutionRole is used instead
	// as it is a superset — it includes CloudWatch Logs + ENI management.
	var basicExecPolicyArn string
	if args.Vpc != nil {
		basicExecPolicyArn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
	} else {
		basicExecPolicyArn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
	}

	err = ctx.RegisterResource("aws:iam/rolePolicyAttachment:RolePolicyAttachment", name+"-basic-exec", pulumi.Map{
		"role":      role.Name,
		"policyArn": pulumi.String(basicExecPolicyArn),
	}, &iam.RolePolicyAttachment{}, pulumi.Parent(l))
	if err != nil {
		return nil, err
	}

	// ── 4. Inline permissions ──────────────────────────
	// Additional IAM permissions from the permissions field.
	// Each permission becomes a separate inline role policy for a
	// clear audit trail and easy revocation.
	for i, perm := range args.Permissions {
		i, perm := i, perm

		policyJSON := pulumi.ToOutput(perm.Resource).ApplyT(func(res interface{}) (string, error) {
			resource := fmt.Sprintf("%v", res)
			if perm.Path != "" {
				resource = fmt.Sprintf("%s/%s/*", resource, perm.Path)
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
					Action:   perm.Actions,
					Resource: []string{resource},
				}},
			})
			if err != nil {
				return "", fmt.Errorf("failed to marshal permission %d: %w", i, err)
			}
			return string(b), nil
		}).(pulumi.StringOutput)

		err = ctx.RegisterResource("aws:iam/rolePolicy:RolePolicy",
			fmt.Sprintf("%s-perm-%d", name, i),
			pulumi.Map{
				"role":   role.Name,
				"policy": policyJSON,
			},
			&iam.RolePolicy{},
			pulumi.Parent(l),
		)
		if err != nil {
			return nil, err
		}
	}

	// ── 5. VPC security group + network wiring (optional) ─────────────────
	// When vpc is set, create a dedicated security group with zero inbound
	// and zero outbound rules. All network access is declared explicitly via
	// vpcEndpoints, hasNat, and cidrs — nothing is reachable until declared.
	// AWSLambdaVPCAccessExecutionRole (attached above) gives the Lambda
	// permission to create and manage its own ENIs in the VPC.
	if args.Vpc != nil {
		sg, err := vpcsg.CreateSecurityGroup(ctx, name, stage, stageId, args.Vpc.VpcId, l)
		if err != nil {
			return nil, err
		}
		l.SecurityGroupID = sg.ID().ToStringOutput()

		// Wire VPC endpoint access — creates both SG rules per endpoint:
		//   - Egress port 443 from Lambda SG → endpoint SG
		//   - Ingress port 443 on endpoint SG ← Lambda SG
		for _, ep := range args.Vpc.VpcEndpoints {
			ep := ep
			target := &lambdaEndpointTarget{args: ep}
			if err := vpcsg.GrantEndpointAccess(
				ctx,
				name,
				l.SecurityGroupID,
				[]vpcsg.EndpointTarget{target},
				l,
			); err != nil {
				return nil, fmt.Errorf("vpcEndpoints: failed to wire %s: %w", ep.EndpointId, err)
			}
		}

		// Wire internet egress via NAT — port 443 out to 0.0.0.0/0.
		// Only wired when hasNat is true. For imported VPCs set hasNat
		// explicitly. For Anvil-managed VPCs this is inferred automatically.
		if args.Vpc.HasNat {
			if err := vpcsg.AddInternetEgressRule(
				ctx,
				fmt.Sprintf("%s-nat-egress", name),
				l.SecurityGroupID,
				443, 443,
				l,
			); err != nil {
				return nil, err
			}
		}

		// Wire CIDR-scoped egress rules — one SG rule per port per CIDR.
		// Use for peered VPCs, on-premise ranges, or any non-AWS-service destination.
		for _, cidrRule := range args.Vpc.CIDRs {
			if len(cidrRule.Ports) == 0 {
				return nil, fmt.Errorf(
					"vpc.cidrs: range %q requires at least one port — be explicit about which ports this range needs",
					cidrRule.Range,
				)
			}
			for _, port := range cidrRule.Ports {
				ruleName := fmt.Sprintf("%s-cidr-%s-%d", name, sanitizeCIDR(cidrRule.Range), port)
				if err := vpcsg.AddCIDREgressRule(
					ctx,
					ruleName,
					l.SecurityGroupID,
					cidrRule.Range,
					port, port,
					l,
				); err != nil {
					return nil, fmt.Errorf("vpc.cidrs: failed to wire %s:%d: %w", cidrRule.Range, port, err)
				}
			}
		}
	}

	// ── 6. Lambda Function ─────────────────────────────
	var codeArchive pulumi.Archive
	if zipPath != "" {
		codeArchive = pulumi.NewFileArchive(zipPath)
	} else {
		codeArchive = pulumi.NewAssetArchive(map[string]interface{}{
			"index.js": pulumi.NewStringAsset(blankHandler),
		})
	}

	lambdaMap := pulumi.Map{
		"name":          pulumi.String(physicalName),
		"runtime":       pulumi.String(runtime),
		"handler":       pulumi.String(handler),
		"role":          role.Arn,
		"memorySize":    pulumi.Int(memory),
		"timeout":       pulumi.Int(timeout),
		"architectures": pulumi.StringArray{pulumi.String(architecture)},
		"code":          codeArchive,
		"tracingConfig": pulumi.Map{
			"mode": pulumi.String(tracingMode),
		},
		"tags": pulumi.StringMap{
			"ManagedBy": pulumi.String("anvil"),
		},
	}

	// VPC config — attach to private subnets with dedicated SG.
	if args.Vpc != nil {
		subnetIds := make(pulumi.StringArray, len(args.Vpc.PrivateSubnetIds))
		for i, id := range args.Vpc.PrivateSubnetIds {
			subnetIds[i] = pulumi.String(id)
		}
		lambdaMap["vpcConfig"] = pulumi.Map{
			"subnetIds":        subnetIds,
			"securityGroupIds": pulumi.StringArray{l.SecurityGroupID},
		}
	}

	// Environment variables.
	if len(args.Environment) > 0 {
		envVars := pulumi.StringMap{}
		for k, v := range args.Environment {
			envVars[k] = pulumi.String(v)
		}
		lambdaMap["environment"] = pulumi.Map{
			"variables": envVars,
		}
	}

	if args.Encryption == "kms" {
		ctx.Log.Info("ℹ KMS encryption enabled for environment variables. Supply kmsKeyArn via transform[\"lambda\"].", nil)
	}

	lambdaProps := transform.MergeTransform(args.Transform["lambda"], lambdaMap)

	res := &lambda.Function{}
	err = ctx.RegisterResource("aws:lambda/function:Function", name, lambdaProps, res,
		pulumi.Parent(l),
		pulumi.DependsOn([]pulumi.Resource{logGroup}),
	)
	if err != nil {
		return nil, err
	}

	// ── 7. Function URL ────────────────────────────────
	var functionUrl pulumi.StringOutput
	if args.URL {
		urlProps := transform.MergeTransform(args.Transform["url"], pulumi.Map{
			"functionName":      res.Name,
			"authorizationType": pulumi.String("AWS_IAM"),
		})
		urlRes := &lambda.FunctionUrl{}
		err = ctx.RegisterResource("aws:lambda/functionUrl:FunctionUrl", name+"-url", urlProps, urlRes, pulumi.Parent(l))
		if err != nil {
			return nil, err
		}
		functionUrl = urlRes.FunctionUrl
	}

	// ── 8. Wire outputs ────────────────────────────────
	l.Arn = res.Arn
	l.FunctionName = res.Name
	l.RoleArn = role.Arn
	if args.URL {
		l.FunctionUrl = functionUrl
	}

	ctx.RegisterResourceOutputs(l, pulumi.Map{
		"arn":             res.Arn,
		"functionName":    res.Name,
		"roleArn":         role.Arn,
		"functionUrl":     functionUrl,
		"securityGroupId": l.SecurityGroupID,
	})

	return l, nil
}

// ── Build manifest ─────────────────────────────────────────

type manifestEntry struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Runtime      string `json:"runtime"`
	Entry        string `json:"entry"`
	Handler      string `json:"handler"`
	Architecture string `json:"architecture"`
}

type manifest struct {
	Functions []manifestEntry `json:"functions"`
}

func appendToManifest(name string, args LambdaArgs) error {
	const manifestPath = ".anvil/build-manifest.json"

	if err := os.MkdirAll(".anvil", 0755); err != nil {
		return err
	}

	m := manifest{}
	if data, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(data, &m)
	}

	architecture := args.Architecture
	if architecture == "" {
		architecture = "arm64"
	}

	m.Functions = append(m.Functions, manifestEntry{
		Name:         name,
		Type:         "anvil:aws:Lambda",
		Runtime:      args.Runtime,
		Entry:        args.Entry,
		Handler:      args.Handler,
		Architecture: architecture,
	})

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(manifestPath, data, 0644)
}
