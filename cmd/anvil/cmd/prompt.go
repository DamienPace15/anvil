package cmd

import (
	"fmt"

	"github.com/manifoldco/promptui"
)

// promptEnvironment shows an interactive picker for environment selection.
// Returns "prod" or "nonprod". Falls back to text input if not a TTY.
func promptEnvironment() (string, error) {
	if !isTTY() {
		return "", fmt.Errorf("No environment specified and terminal is not interactive.\n  Pass --environment prod or --environment nonprod.")
	}

	prompt := promptui.Select{
		Label: "Select environment",
		Items: []string{"nonprod", "prod"},
		Templates: &promptui.SelectTemplates{
			Label:    "  {{ . }}",
			Active:   "  ▸ {{ . | cyan }}",
			Inactive: "    {{ . }}",
			Selected: fmt.Sprintf("  %s Environment: {{ . }}", green("✔")),
		},
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", fmt.Errorf("environment selection cancelled")
	}

	return result, nil
}
