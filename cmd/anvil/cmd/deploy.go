package cmd

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/spf13/cobra"
)

var (
	deployYes     bool
	deployStage   string
	deployVerbose bool
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
	deployCmd.Flags().BoolVar(&deployVerbose, "verbose", false, "Show underlying cloud resources")
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

	s, err := loadStack(ctx, deployStage)
	if err != nil {
		return err
	}

	printBanner()
	fmt.Printf("  Deploying to %s...\n\n", deployStage)

	handler := NewEventHandler(deployVerbose)
	eventCh := make(chan events.EngineEvent)

	go func() {
		for event := range eventCh {
			handler.HandleEvent(event)
		}
	}()

	_, err = s.Up(ctx, optup.EventStreams(eventCh))

	handler.PrintSummary("deploy", deployStage)

	if err != nil {
		return fmt.Errorf("deploy failed")
	}

	return nil
}
