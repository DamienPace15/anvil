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
	Long: `Destroy all resources in the specified stage and remove it from anvil.yaml.

Requires --stage to be set explicitly. The active stage is ignored for safety.`,
	RunE: runDestroy,
}

func init() {
	destroyCmd.Flags().StringVar(&destroyStage, "stage", "", "Stage name to destroy (required)")
	destroyCmd.Flags().BoolVar(&destroyVerbose, "verbose", false, "Show underlying cloud resources")
	rootCmd.AddCommand(destroyCmd)
}

func runDestroy(cmd *cobra.Command, args []string) error {
	if destroyStage == "" {
		fmt.Fprintf(os.Stderr, "  Destroy requires an explicit --stage flag for safety.\n")
		fmt.Fprintf(os.Stderr, "  Example: anvil destroy --stage dev\n")
		return fmt.Errorf("--stage is required for destroy")
	}

	ctx := context.Background()

	s, err := loadStackNoBootstrap(ctx, destroyStage)

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

	// ── Remove stage from anvil.yaml ──
	config, configErr := loadAnvilConfig()
	if configErr != nil {
		return nil
	}

	if _, ok := config.Stages[destroyStage]; ok {
		delete(config.Stages, destroyStage)

		// If active stage was the one destroyed, clear it
		if config.Active == destroyStage {
			config.Active = ""
		}

		if len(config.Stages) == 0 {
			os.Remove("anvil.yaml")
			printCheck("anvil.yaml removed (no stages remaining)")
		} else {
			err = writeAnvilConfig(*config)
			if err != nil {
				fmt.Printf("  %s Could not update anvil.yaml: %s\n", yellow("⚠"), err)
			} else {
				printCheck(fmt.Sprintf("Stage \"%s\" removed from anvil.yaml", destroyStage))
			}
		}
	}

	// Clean up Pulumi stack config file
	os.Remove(fmt.Sprintf("Pulumi.%s.yaml", destroyStage))

	return nil
}
