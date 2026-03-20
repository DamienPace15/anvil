package cmd

import (
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
)

var stageCmd = &cobra.Command{
	Use:   "stage",
	Short: "Manage stages",
	Long:  `View or set the active stage for deploy and preview commands.`,
}

var stageSetCmd = &cobra.Command{
	Use:   "set [name]",
	Short: "Set the active stage",
	Long:  `Set the default stage used by deploy and preview. Destroy always requires --stage explicitly.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runStageSet,
}

var stageGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the active stage",
	RunE:  runStageGet,
}

func init() {
	stageCmd.AddCommand(stageSetCmd)
	stageCmd.AddCommand(stageGetCmd)
	rootCmd.AddCommand(stageCmd)
}

var validStageName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

func runStageSet(cmd *cobra.Command, args []string) error {
	name := args[0]

	if !validStageName.MatchString(name) {
		return fmt.Errorf("Invalid stage name \"%s\".\n  Use alphanumeric characters and hyphens only.", name)
	}

	config, err := loadAnvilConfig()
	if err != nil {
		config = &anvilConfig{
			Stages: make(map[string]*stageConfig),
		}
	}

	config.Active = name

	err = writeAnvilConfig(*config)
	if err != nil {
		return fmt.Errorf("failed to write anvil.yaml: %w", err)
	}

	if isTTY() {
		fmt.Printf("  %s Stage set to: %s\n", green("✔"), bold(name))
	} else {
		fmt.Printf("  Stage set to: %s\n", name)
	}

	return nil
}

func runStageGet(cmd *cobra.Command, args []string) error {
	config, err := loadAnvilConfig()
	if err != nil || config.Active == "" {
		fmt.Println("  No active stage set. Using default: dev")
		fmt.Println("  Run `anvil stage set <name>` to set one.")
		return nil
	}

	if isTTY() {
		fmt.Printf("  Active stage: %s\n", bold(config.Active))
	} else {
		fmt.Printf("  Active stage: %s\n", config.Active)
	}

	return nil
}
