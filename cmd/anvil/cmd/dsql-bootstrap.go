package cmd

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// dsqlLambdaZip is the compiled DSQL bootstrap Lambda binary, embedded at build time.
// Built by: go run build.go build-dsql-lambda
// The zip contains a single binary named "bootstrap" for the provided.al2023 runtime.
//
//go:embed embed/dsql-lambda-bootstrap.zip
var dsqlLambdaZip []byte

// ── Payload types ──────────────────────────────────────────────────────────

// dsqlBootstrapEntry is one _dsqlBootstrap_* stack output value.
type dsqlBootstrapEntry struct {
	DSQLName   string           `json:"dsqlName"`
	Endpoint   string           `json:"endpoint"`
	Region     string           `json:"region"`
	ClusterArn string           `json:"clusterArn"`
	Roles      []dsqlRoleConfig `json:"roles,omitempty"`
	DbRole     string           `json:"dbRole"`
	IamRoleArn string           `json:"iamRoleArn"`
}

// dsqlRoleConfig mirrors the DSQLRoleArgs from the provider.
type dsqlRoleConfig struct {
	Name   string   `json:"name"`
	Schema string   `json:"schema"`
	Grants []string `json:"grants"`
}

// dsqlLambdaPayload is the combined payload sent to the bootstrap Lambda.
// Groups all grants for one cluster into a single invocation.
type dsqlLambdaPayload struct {
	Endpoint string           `json:"endpoint"`
	Region   string           `json:"region"`
	Roles    []dsqlRoleConfig `json:"roles,omitempty"`
	Grants   []dsqlGrantPair  `json:"grants"`
}

// dsqlGrantPair is one IAM role ARN → database role mapping.
type dsqlGrantPair struct {
	DbRole     string `json:"dbRole"`
	IamRoleArn string `json:"iamRoleArn"`
}

// dsqlLambdaResult is the response from the bootstrap Lambda.
type dsqlLambdaResult struct {
	Success  bool     `json:"success"`
	Executed []string `json:"executed"`
	Error    string   `json:"error,omitempty"`
}

// dsqlBootstrapHashes is the on-disk hash store in
// .anvil/dsql-bootstrap-hashes-{stage}.json.
// Keyed by the _dsqlBootstrap_* output name.
// Only written on successful Lambda invocation.
type dsqlBootstrapHashes map[string]string

// ── Entry point ────────────────────────────────────────────────────────────

// runDSQLBootstrap is called from deploy.go after s.Up() succeeds.
// It scans stack outputs for _dsqlBootstrap_* keys, groups them by cluster,
// and for each changed cluster: creates a temp Lambda, invokes it, cleans up.
func runDSQLBootstrap(ctx context.Context, outputs auto.OutputMap, stage, region string) error {
	// Collect all bootstrap entries from stack outputs.
	entries := map[string]dsqlBootstrapEntry{}
	for key, output := range outputs {
		if !strings.HasPrefix(key, "_dsqlBootstrap_") {
			continue
		}
		raw, ok := output.Value.(string)
		if !ok {
			continue
		}
		var entry dsqlBootstrapEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return fmt.Errorf("failed to parse bootstrap output %s: %w", key, err)
		}
		entries[key] = entry
	}

	if len(entries) == 0 {
		return nil
	}

	// Load stored hashes from .anvil/dsql-bootstrap-hashes-{stage}.json
	hashes, err := loadBootstrapHashes(stage)
	if err != nil {
		return fmt.Errorf("failed to load bootstrap hashes: %w", err)
	}

	// Group entries by endpoint — one Lambda invocation per cluster.
	// Multiple grantConnect calls on the same cluster produce multiple entries.
	type clusterGroup struct {
		endpoint   string
		region     string
		clusterArn string
		roles      []dsqlRoleConfig
		grants     []dsqlGrantPair
		keys       []string
	}
	groups := map[string]*clusterGroup{}

	for key, entry := range entries {
		g, ok := groups[entry.Endpoint]
		if !ok {
			g = &clusterGroup{
				endpoint:   entry.Endpoint,
				region:     entry.Region,
				clusterArn: entry.ClusterArn,
			}
			groups[entry.Endpoint] = g
		}
		// Merge roles — deduplicate by name.
		for _, r := range entry.Roles {
			found := false
			for _, existing := range g.roles {
				if existing.Name == r.Name {
					found = true
					break
				}
			}
			if !found {
				g.roles = append(g.roles, r)
			}
		}
		g.grants = append(g.grants, dsqlGrantPair{
			DbRole:     entry.DbRole,
			IamRoleArn: entry.IamRoleArn,
		})
		g.keys = append(g.keys, key)
	}

	// Process each cluster group.
	for _, group := range groups {
		payload := dsqlLambdaPayload{
			Endpoint: group.endpoint,
			Region:   group.region,
			Roles:    group.roles,
			Grants:   group.grants,
		}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal bootstrap payload: %w", err)
		}
		hash := fmt.Sprintf("sha256:%x", sha256.Sum256(payloadJSON))

		// Use the first key as the group representative hash key.
		groupKey := group.keys[0]
		if stored, ok := hashes[groupKey]; ok && stored == hash {
			fmt.Printf("  ✓ DSQL bootstrap unchanged for %s — skipping\n", group.endpoint)
			continue
		}

		fmt.Printf("  ⚡ Running DSQL bootstrap for %s...\n", group.endpoint)

		awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(group.region))
		if err != nil {
			return fmt.Errorf("failed to load AWS config for region %s: %w", group.region, err)
		}

		if err := invokeBootstrapLambda(ctx, awsCfg, group.clusterArn, payload, stage); err != nil {
			// Do not write hash — next deploy will retry automatically.
			fmt.Printf("  ✘ DSQL bootstrap failed for %s: %v\n", group.endpoint, err)
			fmt.Printf("    Bootstrap will retry on next deploy.\n")
			return fmt.Errorf("DSQL bootstrap failed: %w", err)
		}

		// Success — write hash so next deploy skips this cluster.
		hashes[groupKey] = hash
		if err := saveBootstrapHashes(stage, hashes); err != nil {
			// Non-fatal — next deploy will re-run bootstrap but that is idempotent.
			fmt.Printf("  ⚠ Failed to save bootstrap hash for %s: %v\n", group.endpoint, err)
		}

		fmt.Printf("  ✓ DSQL bootstrap complete for %s\n", group.endpoint)
	}

	return nil
}

// ── Lambda lifecycle ───────────────────────────────────────────────────────

// invokeBootstrapLambda creates a short-lived Lambda, invokes it with the
// bootstrap payload, waits for the result, then deletes the Lambda and role.
// The Lambda's execution role has dsql:DbConnectAdmin scoped to the cluster ARN.
// The deployer's credentials never need dsql:DbConnectAdmin.
func invokeBootstrapLambda(
	ctx context.Context,
	awsCfg aws.Config,
	clusterArn string,
	payload dsqlLambdaPayload,
	stage string,
) error {
	iamClient := iam.NewFromConfig(awsCfg)
	lambdaClient := lambda.NewFromConfig(awsCfg)

	// Unique name per invocation — prevents collisions on concurrent deploys.
	ts := time.Now().UnixMilli()
	roleName := fmt.Sprintf("anvil-dsql-bootstrap-%s-%d", stage, ts)
	funcName := fmt.Sprintf("anvil-dsql-bootstrap-%s-%d", stage, ts)

	// Always cleanup — even if invocation fails.
	defer func() {
		cleanupBootstrapLambda(ctx, iamClient, lambdaClient, roleName, funcName)
	}()

	// ── 1. Create IAM execution role ───────────────────────────────────────
	roleArn, err := createBootstrapRole(ctx, iamClient, roleName, clusterArn)
	if err != nil {
		return fmt.Errorf("creating bootstrap IAM role: %w", err)
	}

	// IAM propagation delay — Lambda creation fails if role is not ready yet.
	// 10s is the standard workaround for IAM eventual consistency.
	fmt.Printf("    Waiting for IAM role propagation...\n")
	time.Sleep(10 * time.Second)

	// ── 2. Create Lambda function ──────────────────────────────────────────
	if err := createBootstrapFunction(ctx, lambdaClient, funcName, roleArn); err != nil {
		return fmt.Errorf("creating bootstrap Lambda: %w", err)
	}

	// ── 3. Wait for Lambda to be Active ───────────────────────────────────
	if err := waitForLambdaActive(ctx, lambdaClient, funcName); err != nil {
		return fmt.Errorf("waiting for bootstrap Lambda to be active: %w", err)
	}

	// ── 4. Invoke Lambda ───────────────────────────────────────────────────
	payloadJSON, _ := json.Marshal(payload)
	invokeOut, err := lambdaClient.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(funcName),
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        payloadJSON,
	})
	if err != nil {
		return fmt.Errorf("invoking bootstrap Lambda: %w", err)
	}

	// ── 5. Check result ────────────────────────────────────────────────────
	if invokeOut.FunctionError != nil {
		return fmt.Errorf("bootstrap Lambda function error: %s\nPayload: %s",
			*invokeOut.FunctionError, string(invokeOut.Payload))
	}

	var result dsqlLambdaResult
	if err := json.Unmarshal(invokeOut.Payload, &result); err != nil {
		return fmt.Errorf("failed to parse Lambda response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("bootstrap SQL failed: %s", result.Error)
	}

	for _, stmt := range result.Executed {
		fmt.Printf("    ✓ %s\n", stmt)
	}

	return nil
}

// createBootstrapRole creates a short-lived IAM role for the bootstrap Lambda.
// dsql:DbConnectAdmin is scoped to the specific cluster ARN only.
func createBootstrapRole(ctx context.Context, iamClient *iam.Client, roleName, clusterArn string) (string, error) {
	trustPolicy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "lambda.amazonaws.com" },
    "Action": "sts:AssumeRole"
  }]
}`

	createOut, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(trustPolicy),
		Description:              aws.String("Anvil DSQL bootstrap role — ephemeral, deleted after bootstrap"),
		Tags: []iamtypes.Tag{
			{Key: aws.String("ManagedBy"), Value: aws.String("anvil")},
			{Key: aws.String("Purpose"), Value: aws.String("dsql-bootstrap")},
		},
	})
	if err != nil {
		return "", fmt.Errorf("CreateRole: %w", err)
	}
	roleArn := *createOut.Role.Arn

	// Inline policy — dsql:DbConnectAdmin scoped to this cluster ARN only.
	// Basic Lambda execution permissions for CloudWatch logs.
	inlinePolicy := fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "dsql:DbConnectAdmin",
      "Resource": "%s"
    },
    {
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup",
        "logs:CreateLogStream",
        "logs:PutLogEvents"
      ],
      "Resource": "*"
    }
  ]
}`, clusterArn)

	if _, err = iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String("dsql-bootstrap-policy"),
		PolicyDocument: aws.String(inlinePolicy),
	}); err != nil {
		return "", fmt.Errorf("PutRolePolicy: %w", err)
	}

	return roleArn, nil
}

// createBootstrapFunction creates the Lambda function from the embedded zip.
func createBootstrapFunction(ctx context.Context, lambdaClient *lambda.Client, funcName, roleArn string) error {
	_, err := lambdaClient.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: aws.String(funcName),
		Description:  aws.String("Anvil DSQL bootstrap — ephemeral, deleted after bootstrap"),
		Runtime:      lambdatypes.RuntimeProvidedal2023,
		Architectures: []lambdatypes.Architecture{
			lambdatypes.ArchitectureArm64,
		},
		Role:    aws.String(roleArn),
		Handler: aws.String("bootstrap"),
		Code: &lambdatypes.FunctionCode{
			ZipFile: dsqlLambdaZip,
		},
		Timeout:    aws.Int32(60),
		MemorySize: aws.Int32(256),
		Tags: map[string]string{
			"ManagedBy": "anvil",
			"Purpose":   "dsql-bootstrap",
		},
	})
	return err
}

// waitForLambdaActive polls until the Lambda function state is Active.
// Typically takes 5-10 seconds after creation.
func waitForLambdaActive(ctx context.Context, lambdaClient *lambda.Client, funcName string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := lambdaClient.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String(funcName),
		})
		if err != nil {
			return fmt.Errorf("GetFunction: %w", err)
		}
		state := out.Configuration.State
		if state == lambdatypes.StateActive {
			return nil
		}
		if state == lambdatypes.StateFailed {
			reason := ""
			if out.Configuration.StateReasonCode != "" {
				reason = string(out.Configuration.StateReasonCode)
			}
			return fmt.Errorf("Lambda entered failed state: %s", reason)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for Lambda to become active")
}

// cleanupBootstrapLambda deletes the Lambda function and its IAM role.
// Called via defer — always runs regardless of success or failure.
// Errors are logged but not returned since cleanup failure is non-fatal.
// Orphaned resources are tagged ManagedBy:anvil Purpose:dsql-bootstrap
// for easy identification and manual cleanup if needed.
func cleanupBootstrapLambda(
	ctx context.Context,
	iamClient *iam.Client,
	lambdaClient *lambda.Client,
	roleName, funcName string,
) {
	if _, err := lambdaClient.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(funcName),
	}); err != nil {
		fmt.Printf("  ⚠ Failed to delete bootstrap Lambda %s: %v\n", funcName, err)
	}

	if _, err := iamClient.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
		RoleName:   aws.String(roleName),
		PolicyName: aws.String("dsql-bootstrap-policy"),
	}); err != nil {
		fmt.Printf("  ⚠ Failed to delete bootstrap role policy %s: %v\n", roleName, err)
	}

	if _, err := iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	}); err != nil {
		fmt.Printf("  ⚠ Failed to delete bootstrap IAM role %s: %v\n", roleName, err)
	}
}

// ── Hash persistence ───────────────────────────────────────────────────────

// bootstrapHashesPath returns the path to the hash store for the given stage.
func bootstrapHashesPath(stage string) (string, error) {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(projectRoot, ".anvil", fmt.Sprintf("dsql-bootstrap-hashes-%s.json", stage)), nil
}

// loadBootstrapHashes reads stored hashes from disk.
// Returns an empty map if the file does not exist.
func loadBootstrapHashes(stage string) (dsqlBootstrapHashes, error) {
	path, err := bootstrapHashesPath(stage)
	if err != nil {
		return dsqlBootstrapHashes{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return dsqlBootstrapHashes{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading bootstrap hashes: %w", err)
	}
	var hashes dsqlBootstrapHashes
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil, fmt.Errorf("parsing bootstrap hashes: %w", err)
	}
	return hashes, nil
}

// saveBootstrapHashes writes the hash store to disk.
// Only called on successful Lambda invocation.
func saveBootstrapHashes(stage string, hashes dsqlBootstrapHashes) error {
	path, err := bootstrapHashesPath(stage)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating .anvil directory: %w", err)
	}
	data, err := json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling bootstrap hashes: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ── Doc notes ──────────────────────────────────────────────────────────────
//
// SECURITY MODEL (docs):
//   The CI/CD pipeline never needs dsql:DbConnectAdmin. The bootstrap Lambda's
//   execution role has dsql:DbConnectAdmin scoped to the specific cluster ARN
//   only. The Lambda exists for ~15-30 seconds and is deleted immediately after
//   running. Pipeline credentials only need:
//     - lambda:CreateFunction, lambda:DeleteFunction, lambda:InvokeFunction
//     - lambda:GetFunction (for polling active state)
//     - iam:CreateRole, iam:DeleteRole, iam:PutRolePolicy, iam:DeleteRolePolicy
//     - iam:PassRole (to pass the bootstrap role to Lambda)
//   No database credentials, no dsql:DbConnectAdmin, no persistent admin access.
//
// IDEMPOTENCY (docs):
//   The bootstrap is idempotent by design:
//   - Hash of the combined payload (roles + grants) stored in
//     .anvil/dsql-bootstrap-hashes-{stage}.json
//   - Hash only written on successful invocation
//   - Failed runs leave no hash — next deploy retries automatically
//   - The Lambda SQL is also idempotent:
//     CREATE SCHEMA IF NOT EXISTS, CREATE ROLE (ignores already exists),
//     GRANT ... ON ALL TABLES (safe to re-run)
//     AWS IAM GRANT (ignores already exists)
//
// HASH FILE (docs):
//   .anvil/dsql-bootstrap-hashes-{stage}.json is gitignored along with the
//   rest of .anvil/. It is machine-local — each developer and each CI/CD
//   runner maintains its own hash file. This means:
//   - First deploy on a new machine always runs the bootstrap
//   - CI/CD always runs the bootstrap on the first run after a new runner
//   - Subsequent runs on the same machine/runner skip if unchanged
//   The bootstrap SQL is idempotent so re-running on a new machine is safe.
//
// ALTER DEFAULT PRIVILEGES NOT SUPPORTED (docs):
//   Aurora DSQL does not support ALTER DEFAULT PRIVILEGES. Grants only apply
//   to tables that exist at bootstrap time. When new tables are added to the
//   schema, re-deploy to update grants. Anvil detects this automatically if
//   the roles block changes (hash changes). If tables are added without
//   changing the roles block, make any change to the roles config to trigger
//   a re-run. Future: --force-bootstrap flag.
//
// IAM PROPAGATION DELAY (docs):
//   AWS IAM has eventual consistency — newly created roles are not immediately
//   available to Lambda. The 10-second sleep after role creation is a known
//   workaround. If Lambda creation fails with InvalidParameterValueException
//   referencing the role, the propagation delay may need increasing.
//
// MULTI-REGION (docs):
//   The bootstrap Lambda connects to the primary region endpoint only.
//   AWS IAM GRANT replicates automatically to peer regions in a multi-region
//   DSQL cluster. No need to run the bootstrap against each region endpoint.
//
// LAMBDA ZIP EMBEDDING (docs):
//   dsql-lambda-bootstrap.zip is embedded in the anvil binary at build time
//   using Go's //go:embed directive. The zip contains a single binary named
//   "bootstrap" compiled for Linux arm64 (Graviton). This means:
//   - No build step at deploy time
//   - The zip ships with every anvil release
//   - To update the Lambda code: go run build.go build
//   - The embedded zip adds ~10MB to the anvil binary size
//
// CLEANUP (docs):
//   Lambda and IAM role are always deleted after invocation via defer.
//   Cleanup runs even if invocation panics. If cleanup fails, a warning is
//   printed but the deploy does not fail. Orphaned resources are tagged
//   ManagedBy:anvil Purpose:dsql-bootstrap for easy manual identification.
//
// SINGLE-REGION ONLY FOR SYDNEY/MELBOURNE (docs):
//   ap-southeast-2 (Sydney) and ap-southeast-4 (Melbourne) are only available
//   as single-region DSQL clusters. The multiRegion block will deploy but
//   DSQL rejects the peering. Document this limitation clearly.
