package cmd

import (
	"context"
	"fmt"
	"os"

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

	s, err := loadStack(ctx, previewStage)
	if err != nil {
		return err
	}

	fmt.Printf("Previewing stage \"%s\"...\n\n", previewStage)

	result, err := s.Preview(ctx, optpreview.ProgressStreams(os.Stdout))
	if err != nil {
		return fmt.Errorf("preview failed: %w", err)
	}

	fmt.Printf("\nChanges: %v\n", result.ChangeSummary)

	return nil
}
