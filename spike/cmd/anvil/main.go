package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
)

func main() {
	ctx := context.Background()

	// Points at a directory containing a Pulumi.yaml + your program
	workDir := "/Users/damienpace/Desktop/anvil-core.nosync/test-app" // e.g. ./test/spike-project
	stackName := "dev"

	// Local file backend — no Pulumi Cloud account needed
	s, err := auto.UpsertStackLocalSource(ctx, stackName, workDir,
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       "file://~/.anvil-state",
			"PULUMI_CONFIG_PASSPHRASE": "", // empty = no encryption
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stack init failed: %v\n", err)
		os.Exit(1)
	}

	// Set AWS region via stack config
	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: "ap-southeast-2"})

	switch cmd := "destroy"; cmd { // swap this to test each operation
	case "preview":
		result, err := s.Preview(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "preview failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result.ChangeSummary)

	case "up":
		result, err := s.Up(ctx, optup.ProgressStreams(os.Stdout))
		if err != nil {
			fmt.Fprintf(os.Stderr, "up failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("update summary: %v\n", result.Summary)

	case "destroy":
		result, err := s.Destroy(ctx, optdestroy.ProgressStreams(os.Stdout))
		if err != nil {
			fmt.Fprintf(os.Stderr, "destroy failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("destroy summary: %v\n", result.Summary)
	}
}
