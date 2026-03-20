package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/spf13/cobra"
)

var (
	destroyYes     bool
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
	destroyCmd.Flags().BoolVarP(&destroyYes, "yes", "y", false, "Confirm destruction (required)")
	destroyCmd.Flags().StringVar(&destroyStage, "stage", "dev", "Stage name to destroy")
	destroyCmd.Flags().BoolVar(&destroyVerbose, "verbose", false, "Show underlying cloud resources")
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
