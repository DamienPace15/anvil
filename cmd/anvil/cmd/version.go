package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Anvil CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("anvil %s (commit: %s, built: %s)\n", version, commit, date)
	},
}
