package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	unlockStage string
)

var unlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Release a locked stage",
	Long: `Release the lock on a stage that is stuck from a previous failed or interrupted operation.

This is a dangerous operation — only use it if you are sure no other deployment
is currently running against this stage.`,
	RunE: runUnlock,
}

func init() {
	unlockCmd.Flags().StringVar(&unlockStage, "stage", "dev", "Stage name to unlock")
	rootCmd.AddCommand(unlockCmd)
}

func runUnlock(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	s, err := loadStack(ctx, unlockStage)
	if err != nil {
		return err
	}

	fmt.Printf("  Unlocking stage \"%s\"...\n", unlockStage)

	err = s.Cancel(ctx)
	if err != nil {
		return fmt.Errorf("failed to unlock stage \"%s\": %w", unlockStage, err)
	}

	if isTTY() {
		fmt.Printf("\n  %s Stage \"%s\" unlocked.\n", green("✔"), unlockStage)
	} else {
		fmt.Printf("\n  Stage \"%s\" unlocked.\n", unlockStage)
	}

	return nil
}
