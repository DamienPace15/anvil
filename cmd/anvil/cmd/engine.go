package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// defaultBackendURL is the fallback when ANVIL_BACKEND_URL is not set.
const defaultBackendURL = "file://~/.anvil-state"

// defaultRegion is the AWS region used for all stacks.
// TODO: read from anvil config or flags.
const defaultRegion = "ap-southeast-2"

// loadStack creates or selects a stack using the Automation API.
// It reads the backend URL from ANVIL_BACKEND_URL (falling back to a local file backend),
// uses the current working directory as the Pulumi project root,
// and configures the AWS region on the stack.
func loadStack(ctx context.Context, stage string) (auto.Stack, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to get working directory: %w", err)
	}

	backendURL := os.Getenv("ANVIL_BACKEND_URL")
	if backendURL == "" {
		backendURL = defaultBackendURL
	}

	s, err := auto.UpsertStackLocalSource(ctx, stage, workDir,
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       backendURL,
			"PULUMI_CONFIG_PASSPHRASE": "",
		}),
	)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("stack init failed: %w", err)
	}

	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: defaultRegion})

	return s, nil
}
