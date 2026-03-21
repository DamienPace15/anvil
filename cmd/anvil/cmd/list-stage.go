package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
)

var stageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stages",
	Long:  `Lists all stages found in the S3 backend and marks the active one.`,
	RunE:  runStageList,
}

func init() {
	stageCmd.AddCommand(stageListCmd)
}

func runStageList(cmd *cobra.Command, args []string) error {
	config, err := loadAnvilConfig()
	if err != nil {
		fmt.Println()
		fmt.Println("  No anvil.yaml found. Run `anvil deploy` to get started.")
		fmt.Println()
		return nil
	}

	if len(config.Stages) == 0 {
		fmt.Println()
		fmt.Println("  No stages found. Run `anvil deploy` to create your first stage.")
		fmt.Println()
		return nil
	}

	// Discover stacks from S3 backends, merged with anvil.yaml entries.
	stacks := discoverStacks(config)

	// Collect and sort for stable output.
	names := make([]string, 0, len(stacks))
	for name := range stacks {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build display lines.
	type displayLine struct {
		name   string
		region string
		active bool
	}

	activeStage := config.Active
	var lines []displayLine
	maxNameLen := 0
	maxRegionLen := 0

	for _, name := range names {
		region := ""
		if sc, ok := config.Stages[name]; ok && sc.Region != "" {
			region = sc.Region
		}
		if len(name) > maxNameLen {
			maxNameLen = len(name)
		}
		if len(region) > maxRegionLen {
			maxRegionLen = len(region)
		}
		lines = append(lines, displayLine{
			name:   name,
			region: region,
			active: name == activeStage,
		})
	}

	// Minimum column widths for aesthetics.
	if maxNameLen < 6 {
		maxNameLen = 6
	}
	if maxRegionLen < 6 {
		maxRegionLen = 6
	}

	fmt.Println()
	printBanner()

	if isTTY() {
		fmt.Printf("  %s Stages %s\n", green("⚒"), dim(fmt.Sprintf("(%s)", config.Project)))
		fmt.Println()

		// Column headers — "  " prefix aligns with the "* " / "  " indicator.
		fmt.Printf("  %-*s   %s\n",
			maxNameLen+2, dim("  STAGE"),
			dim("REGION"),
		)
		fmt.Printf("  %-*s   %s\n",
			maxNameLen+2, dim("  "+strings.Repeat("─", maxNameLen)),
			dim(strings.Repeat("─", maxRegionLen)),
		)

		for _, l := range lines {
			indicator := "  "
			nameStr := l.name
			if l.active {
				indicator = green("*") + " "
				nameStr = bold(l.name)
			}

			regionStr := ""
			if l.region != "" {
				regionStr = dim(l.region)
			}

			// Use raw name length for padding (bold adds invisible escape chars).
			namePad := strings.Repeat(" ", maxNameLen-len(l.name))

			fmt.Printf("  %s%s%s   %s\n",
				indicator,
				nameStr,
				namePad,
				regionStr,
			)
		}

		fmt.Println()
		fmt.Printf("  %s = active stage\n", green("*"))
		fmt.Println()
	} else {
		// Plain output for piping/CI.
		fmt.Printf("  Stages (%s)\n\n", config.Project)
		for _, l := range lines {
			marker := " "
			if l.active {
				marker = "*"
			}
			fmt.Printf("  %s %-*s   %s\n", marker, maxNameLen, l.name, l.region)
		}
		fmt.Println()
	}

	return nil
}

// discoverStacks queries each bootstrapped S3 backend for .pulumi/stacks/*.json files
// and returns a deduplicated set of stack names found.
func discoverStacks(config *anvilConfig) map[string]bool {
	found := make(map[string]bool)

	if config.Project == "" {
		return found
	}

	type bucketInfo struct {
		name   string
		region string
	}
	seen := make(map[string]bool)
	var buckets []bucketInfo

	for stage, sc := range config.Stages {
		if sc == nil || sc.ID == "" {
			continue
		}
		bucketName := resolveBucketName(stage, config.Project, sc.ID)
		if seen[bucketName] {
			continue
		}
		seen[bucketName] = true
		buckets = append(buckets, bucketInfo{name: bucketName, region: sc.Region})
	}

	ctx := context.Background()

	for _, b := range buckets {
		stackNames, err := listStacksInBucket(ctx, b.name, b.region)
		if err != nil {
			continue
		}
		for _, name := range stackNames {
			// Filter out Pulumi project-qualified names (e.g. "test-app/damien").
			// These are internal — only show clean stage names.
			if strings.Contains(name, "/") {
				continue
			}
			found[name] = true
		}
	}

	// Include all config stages — bootstrapped even if not yet deployed.
	for name := range config.Stages {
		found[name] = true
	}

	return found
}

// listStacksInBucket lists Pulumi stack files in an S3 bucket.
func listStacksInBucket(ctx context.Context, bucketName, region string) ([]string, error) {
	var cfgOpts []func(*awsconfig.LoadOptions) error
	if region != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg)

	prefix := ".pulumi/stacks/"
	output, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucketName,
		Prefix: &prefix,
	})
	if err != nil {
		return nil, err
	}

	var names []string
	for _, obj := range output.Contents {
		if obj.Key == nil {
			continue
		}
		key := *obj.Key
		name := strings.TrimPrefix(key, prefix)
		if strings.HasSuffix(name, ".json") {
			name = strings.TrimSuffix(name, ".json")
			if name != "" {
				names = append(names, name)
			}
		}
	}

	return names, nil
}
