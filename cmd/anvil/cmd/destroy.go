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
	destroyStage   string
	destroyVerbose bool
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Tear down a deployment",
	Long:  `Destroy all resources in the specified stage and remove it from anvil.yaml.`,
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

	// ── 1. Destroy app resources via Pulumi ──
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

	// ── 2. Remove stage from anvil.yaml ──
	config, configErr := loadAnvilConfig()
	if configErr != nil {
		return nil
	}

	if _, ok := config.Stages[destroyStage]; !ok {
		return nil
	}

	delete(config.Stages, destroyStage)

	if len(config.Stages) == 0 {
		// No stages left — remove anvil.yaml entirely
		os.Remove("anvil.yaml")
		printCheck("anvil.yaml removed (no stages remaining)")
	} else {
		// Other stages still exist — update the file
		err = writeAnvilConfig(*config)
		if err != nil {
			fmt.Printf("  %s Could not update anvil.yaml: %s\n", yellow("⚠"), err)
		} else {
			printCheck(fmt.Sprintf("Stage \"%s\" removed from anvil.yaml", destroyStage))
		}
	}

	os.Remove(fmt.Sprintf("Pulumi.%s.yaml", destroyStage))

	return nil
}
