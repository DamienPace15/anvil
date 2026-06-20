package cmd

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/spf13/cobra"
)

var (
	deployStage      string
	deployVerbose    bool
	deployPartial    bool
	deployForceCache bool
	deployRefresh    bool
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy your infrastructure",
	Long:  `Deploy infrastructure to your cloud account. Uses the Anvil provider to create secure-by-default resources.`,
	RunE:  runDeploy,
}

func init() {
	deployCmd.Flags().StringVar(&deployStage, "stage", "", "Stage name for this deployment")
	deployCmd.Flags().BoolVar(&deployVerbose, "verbose", false, "Show underlying cloud resources")
	deployCmd.Flags().BoolVar(&deployPartial, "partial", false, "Deploy successfully built functions even if some builds fail")
	deployCmd.Flags().BoolVar(&deployForceCache, "force-cache", false, "Skip rebuild and use last cached build artifacts")
	deployCmd.Flags().BoolVar(&deployRefresh, "refresh", false, "Refresh state from cloud before deploying")
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	stage := resolveStage(deployStage)
	ctx := context.Background()

	// ── Step 1: Discover Lambda functions ─────────────
	// Runs the program with ANVIL_BUILD_MODE=true using a separate
	// stack instance so the real deploy stack is never contaminated.
	// Makes no AWS API calls — purely a manifest discovery step.
	functions, err := discoverFunctions(ctx, stage)
	if err != nil {
		return fmt.Errorf("function discovery failed: %w", err)
	}

	// ── Step 2: Load real deploy stack ────────────────
	s, err := loadStack(ctx, stage)
	if err != nil {
		return err
	}

	printBanner()
	fmt.Printf("  Deploying to %s...\n\n", stage)

	// ── Step 3: Build functions ────────────────────────
	// Bundle each discovered function and collect the zip paths.
	// Skips rebuild if source hash matches cached hash (unless --force-cache).
	if len(functions) > 0 {
		fmt.Printf("Building %d Lambda function(s)...\n\n", len(functions))

		artifacts, buildErrors := buildFunctions(functions, deployForceCache, deployPartial)

		// Surface build errors
		if len(buildErrors) > 0 {
			for _, e := range buildErrors {
				fmt.Printf("  ❌ Build failed: %s\n", e)
			}
			if !deployPartial {
				return fmt.Errorf("build failed — use --partial to deploy successfully built functions, or --force-cache to skip rebuild")
			}
			fmt.Printf("\n  ⚠️  --partial: deploying %d successfully built function(s), skipping %d failed\n\n",
				len(artifacts), len(buildErrors))
		}

		// ── Step 4: Set artifact paths in Pulumi config ─
		// Provider reads anvil:fn:{name} to locate the zip during deploy.
		if err := setFunctionArtifacts(ctx, s, artifacts); err != nil {
			return fmt.Errorf("failed to set function artifacts: %w", err)
		}
	}

	// ── Step 5: Real deploy ────────────────────────────
	handler := NewEventHandler(deployVerbose, "deploy")
	eventCh := make(chan events.EngineEvent)

	go func() {
		for event := range eventCh {
			handler.HandleEvent(event)
		}
	}()

	upOpts := []optup.Option{
		optup.EventStreams(eventCh),
		optup.Parallel(10),
	}
	if deployRefresh {
		upOpts = append(upOpts, optup.Refresh())
	}

	_, err = s.Up(ctx, upOpts...)

	// ── Step 6: DSQL Bootstrap ─────────────────────────
	// Runs after infrastructure deploy completes successfully.
	// Detects _dsqlBootstrap_* stack outputs, groups by cluster, and for
	// each changed cluster: creates a short-lived Lambda with
	// dsql:DbConnectAdmin scoped to that cluster ARN, invokes it to run
	// the bootstrap SQL, then deletes the Lambda and its IAM role.
	//
	// Skipped entirely if no DSQL components use grantConnect.
	// Skipped per-cluster if the payload hash matches the stored hash in
	// .anvil/dsql-bootstrap-hashes-{stage}.json
	//
	// Does not fail the deploy if bootstrap errors — infrastructure is up,
	// bootstrap retries automatically on next deploy (hash not written).
	if !handler.HasErrors() {
		outputs, outErr := s.Outputs(ctx)
		if outErr != nil {
			fmt.Printf("  ⚠ Could not read stack outputs for DSQL bootstrap: %v\n", outErr)
		} else {
			fmt.Printf("  🔍 Debug: found %d stack outputs\n", len(outputs))
			for k := range outputs {
				fmt.Printf("  🔍 Output key: %s\n", k)
			}
			region := resolveRegionForStage(stage)
			if bootstrapErr := runDSQLBootstrap(ctx, outputs, stage, region); bootstrapErr != nil {
				fmt.Printf("\n  ✘ DSQL bootstrap error: %v\n", bootstrapErr)
			}
		}
	}

	handler.PrintSummary(stage)

	if handler.HasErrors() {
		return fmt.Errorf("deploy failed")
	}

	return nil
}
