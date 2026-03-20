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
func ensureBootstrapped(ctx context.Context, stage string) error {
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
