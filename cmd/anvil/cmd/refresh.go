package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/spf13/cobra"
)

var (
	refreshStage   string
	refreshVerbose bool
)

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Sync Anvil state with actual cloud state",
	Long: `Reconcile Anvil's state file with the actual state of your cloud resources.

Use this to:
  - Clean up pending operations from an interrupted deploy
  - Resolve provider version mismatch warnings
  - Detect drift between Anvil state and live infrastructure

Does not deploy or destroy any resources — state only.`,
	RunE: runRefresh,
}

func init() {
	refreshCmd.Flags().StringVar(&refreshStage, "stage", "", "Stage name to refresh (required)")
	refreshCmd.Flags().BoolVar(&refreshVerbose, "verbose", false, "Show underlying cloud resources")
	rootCmd.AddCommand(refreshCmd)
}

func runRefresh(cmd *cobra.Command, args []string) error {
	if refreshStage == "" {
		fmt.Fprintf(os.Stderr, "  Refresh requires an explicit --stage flag.\n")
		fmt.Fprintf(os.Stderr, "  Example: anvil refresh --stage dev\n")
		return fmt.Errorf("--stage is required for refresh")
	}

	ctx := context.Background()

	s, err := loadStackNoBootstrap(ctx, refreshStage)
	if err != nil {
		return err
	}

	printBanner()
	fmt.Printf("  Refreshing %s...\n\n", refreshStage)

	handler := NewEventHandler(refreshVerbose, "refresh")
	eventCh := make(chan events.EngineEvent)

	go func() {
		for event := range eventCh {
			handler.HandleEvent(event)
		}
	}()

	_, err = s.Refresh(ctx, optrefresh.EventStreams(eventCh))

	handler.PrintSummary(refreshStage)

	if err != nil {
		return fmt.Errorf("refresh failed")
	}

	return nil
}
