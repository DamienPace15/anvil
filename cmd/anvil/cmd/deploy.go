package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
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

	// ── Step 4b: Build DSQL bootstrap Lambda (if needed) ─
	if err := buildAndSetDsqlBootstrap(ctx, s); err != nil {
		return fmt.Errorf("DSQL bootstrap build failed: %w", err)
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

	handler.PrintSummary(stage)

	if handler.HasErrors() {
		return fmt.Errorf("deploy failed")
	}

	return nil
}

// buildAndSetDsqlBootstrap compiles the DSQL bootstrap Lambda (Go, arm64) and
// sets its zip path in Pulumi config so the DSQL provider can reference it.
// Only builds if the source directory exists. Skips silently if not needed.
func buildAndSetDsqlBootstrap(ctx context.Context, s auto.Stack) error {
	lambdaDir := "cmd/anvil/dsql-lambda"
	if _, err := os.Stat(filepath.Join(lambdaDir, "main.go")); os.IsNotExist(err) {
		return nil
	}

	outDir := ".anvil/internal"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	binaryPath := filepath.Join(outDir, "bootstrap")
	zipPath := filepath.Join(outDir, "dsql-lambda.zip")

	// Cross-compile for Lambda
	buildCmd := exec.Command("go", "build", "-tags", "lambda.norpc", "-o",
		filepath.Join("..", "..", "..", binaryPath), ".")
	buildCmd.Dir = lambdaDir
	buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("compiling dsql-lambda: %w", err)
	}

	// Zip the binary
	zipCmd := exec.Command("zip", "-j", "dsql-lambda.zip", "bootstrap")
	zipCmd.Dir = outDir
	zipCmd.Stdout = os.Stdout
	zipCmd.Stderr = os.Stderr
	if err := zipCmd.Run(); err != nil {
		return fmt.Errorf("zipping dsql-lambda: %w", err)
	}

	os.Remove(binaryPath)

	absZip, err := filepath.Abs(zipPath)
	if err != nil {
		return fmt.Errorf("resolving zip path: %w", err)
	}

	// Set config for ALL DSQL components. The provider reads
	// "anvil:dsql-bootstrap-{componentName}" — we use a wildcard key pattern
	// since we don't know component names at build time.
	// For now, set the generic key that the provider falls back to.
	key := "anvil:dsql-bootstrap"
	if err := s.SetConfig(ctx, key, auto.ConfigValue{Value: absZip}); err != nil {
		return fmt.Errorf("setting dsql-bootstrap config: %w", err)
	}

	return nil
}
