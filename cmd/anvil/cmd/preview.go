package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/spf13/cobra"
)

var previewStage string

var previewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Preview changes without deploying",
	Long:  `Show a diff of what would change in your infrastructure without actually applying it.`,
	RunE:  runPreview,
}

func init() {
	previewCmd.Flags().StringVar(&previewStage, "stage", "dev", "Stage name for this deployment")
	rootCmd.AddCommand(previewCmd)
}

func runPreview(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	backendURL := os.Getenv("ANVIL_BACKEND_URL")
	if backendURL == "" {
		backendURL = "file://~/.anvil-state"
	}

	s, err := auto.UpsertStackLocalSource(ctx, previewStage, workDir,
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       backendURL,
			"PULUMI_CONFIG_PASSPHRASE": "",
		}),
	)
	if err != nil {
		return fmt.Errorf("stack init failed: %w", err)
	}

	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: "ap-southeast-2"})

	fmt.Printf("Previewing stage \"%s\"...\n\n", previewStage)

	result, err := s.Preview(ctx, optpreview.ProgressStreams(os.Stdout))
	if err != nil {
		return fmt.Errorf("preview failed: %w", err)
	}

	fmt.Printf("\nChanges: %v\n", result.ChangeSummary)

	return nil
}
