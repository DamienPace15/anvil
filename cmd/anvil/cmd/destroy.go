package cmd

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/spf13/cobra"
)

var (
	destroyStage   string
	destroyVerbose bool
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Tear down a deployment",
	Long:  `Destroy all resources in the specified stage. Requires --yes to confirm.`,
	RunE:  runDestroy,
}

func init() {
	destroyCmd.Flags().StringVar(&destroyStage, "stage", "dev", "Stage name to destroy")
	destroyCmd.Flags().BoolVar(&destroyVerbose, "verbose", false, "Show underlying cloud resources")
	rootCmd.AddCommand(destroyCmd)
}

func runDestroy(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	s, err := loadStack(ctx, destroyStage)
	if err != nil {
		return err
	}

	printBanner()
	fmt.Printf("  Destroying %s...\n\n", destroyStage)

	handler := NewEventHandler(destroyVerbose)
	eventCh := make(chan events.EngineEvent)

	go func() {
		for event := range eventCh {
			handler.HandleEvent(event)
		}
	}()

	_, err = s.Destroy(ctx, optdestroy.EventStreams(eventCh))

	handler.PrintSummary("destroy", destroyStage)

	if err != nil {
		return fmt.Errorf("destroy failed")
	}

	return nil
}
