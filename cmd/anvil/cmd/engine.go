package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// defaultBackendURL is the fallback when no anvil.yaml or env var is set.
const defaultBackendURL = "file://~/.anvil-state"

// defaultRegion is the AWS region used for all stacks.
const defaultRegion = "ap-southeast-2"

// resolveStage determines the effective stage.
// Priority: explicit flag → active stage from anvil.yaml → "dev"
func resolveStage(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}

	config, err := loadAnvilConfig()
	if err == nil && config.Active != "" {
		return config.Active
	}

	fmt.Printf("  %s No active stage set. Defaulting to \"dev\".\n", dim("hint:"))
	fmt.Printf("  %s Run `anvil stage set <name>` to set one.\n\n", dim("     "))

	return "dev"
}

// resolveBackendForStage determines the backend URL for a specific stage.
func resolveBackendForStage(stage string) string {
	if envURL := os.Getenv("ANVIL_BACKEND_URL"); envURL != "" {
		return envURL
	}

	config, err := loadAnvilConfig()
	if err != nil {
		return defaultBackendURL
	}

	sc, ok := config.Stages[stage]
	if !ok || sc.ID == "" {
		return defaultBackendURL
	}

	bucketName := resolveBucketName(stage, config.Project, sc.ID)
	return fmt.Sprintf("s3://%s", bucketName)
}

// resolveRegionForStage determines the AWS region for a specific stage.
func resolveRegionForStage(stage string) string {
	config, err := loadAnvilConfig()
	if err != nil {
		return defaultRegion
	}

	sc, ok := config.Stages[stage]
	if !ok || sc.Region == "" {
		return defaultRegion
	}

	return sc.Region
}

// ensureBootstrapped checks if the given stage is bootstrapped. If not, runs bootstrap automatically.
func ensureBootstrapped(_ context.Context, stage string) error {
	config, err := loadAnvilConfig()
	if err == nil {
		if _, ok := config.Stages[stage]; ok {
			return nil
		}
	}

	fmt.Println("  No state backend found for stage \"" + stage + "\". Setting up...\n")

	bootstrapStage = stage
	bootstrapRegion = ""
	err = runBootstrap(nil, nil)
	if err != nil {
		return fmt.Errorf("auto-bootstrap failed: %w", err)
	}

	fmt.Println()
	return nil
}

// loadStack creates or selects a stack using the Automation API.
func loadStack(ctx context.Context, stage string) (auto.Stack, error) {
	err := ensureBootstrapped(ctx, stage)
	if err != nil {
		return auto.Stack{}, err
	}

	workDir, err := os.Getwd()
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to get working directory: %w", err)
	}

	backendURL := resolveBackendForStage(stage)
	region := resolveRegionForStage(stage)

	s, err := auto.UpsertStackLocalSource(ctx, stage, workDir,
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       backendURL,
			"PULUMI_CONFIG_PASSPHRASE": "",
		}),
	)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("%s", mapError(err.Error()))
	}

	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: region})

	return s, nil
}

// loadStackNoBootstrap loads a stack without auto-bootstrapping.
// Used by destroy — it shouldn't create infrastructure to tear it down.
func loadStackNoBootstrap(ctx context.Context, stage string) (auto.Stack, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to get working directory: %w", err)
	}

	backendURL := resolveBackendForStage(stage)
	if backendURL == defaultBackendURL {
		return auto.Stack{}, fmt.Errorf("Stage \"%s\" has not been bootstrapped.\n  Nothing to destroy.", stage)
	}

	region := resolveRegionForStage(stage)

	s, err := auto.UpsertStackLocalSource(ctx, stage, workDir,
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       backendURL,
			"PULUMI_CONFIG_PASSPHRASE": "",
		}),
	)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("%s", mapError(err.Error()))
	}

	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: region})

	return s, nil
}
