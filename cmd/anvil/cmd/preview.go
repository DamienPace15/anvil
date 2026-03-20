package cmd

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/spf13/cobra"
)

var (
	previewStage   string
	previewVerbose bool
)

var previewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Preview changes without deploying",
	Long:  `Show a diff of what would change in your infrastructure without actually applying it.`,
	RunE:  runPreview,
}

func init() {
	previewCmd.Flags().StringVar(&previewStage, "stage", "", "Stage name for this deployment")
	previewCmd.Flags().BoolVar(&previewVerbose, "verbose", false, "Show underlying cloud resources")
	rootCmd.AddCommand(previewCmd)
}

func runPreview(cmd *cobra.Command, args []string) error {
	stage := resolveStage(previewStage)

	ctx := context.Background()

	s, err := loadStack(ctx, stage)
	if err != nil {
		return err
	}

	fmt.Printf("\n  Previewing %s...\n\n", stage)

	handler := NewEventHandler(previewVerbose)
	eventCh := make(chan events.EngineEvent)

	go func() {
		for event := range eventCh {
			handler.HandleEvent(event)
		}
	}()

	_, err = s.Preview(ctx, optpreview.EventStreams(eventCh))

	handler.PrintSummary("preview", stage)

	if err != nil {
		return fmt.Errorf("preview failed")
	}

	return nil
}
