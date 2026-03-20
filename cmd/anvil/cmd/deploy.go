package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/spf13/cobra"
)

var (
	deployYes   bool
	deployStage string
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy your infrastructure",
	Long:  `Deploy infrastructure to your cloud account. Uses the Anvil provider to create secure-by-default resources.`,
	RunE:  runDeploy,
}

func init() {
	deployCmd.Flags().BoolVarP(&deployYes, "yes", "y", false, "Skip confirmation prompt")
	deployCmd.Flags().StringVar(&deployStage, "stage", "dev", "Stage name for this deployment")
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	if !deployYes {
		fmt.Print("Deploy to stage \"" + deployStage + "\"? [y/N] ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	ctx := context.Background()

	// Work directory — where the user's Pulumi project lives.
	// TODO(2.x): resolve this from the current directory or a config file.
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Backend URL — local file backend by default, overridable via env var.
	backendURL := os.Getenv("ANVIL_BACKEND_URL")
	if backendURL == "" {
		backendURL = "file://~/.anvil-state"
	}

	s, err := auto.UpsertStackLocalSource(ctx, deployStage, workDir,
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       backendURL,
			"PULUMI_CONFIG_PASSPHRASE": "",
		}),
	)
	if err != nil {
		return fmt.Errorf("stack init failed: %w", err)
	}

	// Set AWS region via stack config.
	// TODO(2.x): read from anvil config or flags.
	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: "ap-southeast-2"})

	fmt.Printf("Deploying stage \"%s\"...\n\n", deployStage)
	start := time.Now()

	result, err := s.Up(ctx, optup.ProgressStreams(os.Stdout))
	if err != nil {
		return fmt.Errorf("deploy failed: %w", err)
	}

	duration := time.Since(start).Round(time.Second)

	// Count resources from the summary.
	resourceCount := 0
	if result.Summary.ResourceChanges != nil {
		for _, count := range *result.Summary.ResourceChanges {
			resourceCount += count
		}
	}

	fmt.Printf("\n✓ Deploy complete (%s, %d resources)\n", duration, resourceCount)

	return nil
}
