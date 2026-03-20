package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/spf13/cobra"
)

var (
	destroyYes   bool
	destroyStage string
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Tear down a deployment",
	Long:  `Destroy all resources in the specified stage. Requires --yes to confirm.`,
	RunE:  runDestroy,
}

func init() {
	destroyCmd.Flags().BoolVarP(&destroyYes, "yes", "y", false, "Confirm destruction (required)")
	destroyCmd.Flags().StringVar(&destroyStage, "stage", "dev", "Stage name to destroy")
	rootCmd.AddCommand(destroyCmd)
}

func runDestroy(cmd *cobra.Command, args []string) error {
	if !destroyYes {
		fmt.Fprintf(os.Stderr, "Refusing to destroy without confirmation. Pass --yes to proceed.\n")
		return fmt.Errorf("destroy requires --yes flag")
	}

	ctx := context.Background()

	s, err := loadStack(ctx, destroyStage)
	if err != nil {
		return err
	}

	fmt.Printf("Destroying stage \"%s\"...\n\n", destroyStage)
	start := time.Now()

	result, err := s.Destroy(ctx, optdestroy.ProgressStreams(os.Stdout))
	if err != nil {
		return fmt.Errorf("destroy failed: %w", err)
	}

	duration := time.Since(start).Round(time.Second)

	resourceCount := 0
	if result.Summary.ResourceChanges != nil {
		for _, count := range *result.Summary.ResourceChanges {
			resourceCount += count
		}
	}

	fmt.Printf("\n✓ Destroy complete (%s, %d resources destroyed)\n", duration, resourceCount)

	return nil
}
