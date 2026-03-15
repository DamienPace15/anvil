package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Runs from provider/ directory (cd provider && go run ../scripts/merge/merge_schemas.go)

func main() {
	fmt.Println("🔗 Starting Schema Merger...")
	start := time.Now()

	// 1. Load the base skeleton (provider/base-schema.json)
	basePath := "base-schema.json"
	baseBytes, err := os.ReadFile(basePath)
	if err != nil {
		log.Fatalf("❌ Could not find base-schema.json: %v", err)
	}

	var masterSchema map[string]interface{}
	if err := json.Unmarshal(baseBytes, &masterSchema); err != nil {
		log.Fatalf("❌ Invalid JSON in base-schema.json: %v", err)
	}

	// Ensure maps exist
	if masterSchema["resources"] == nil {
		masterSchema["resources"] = make(map[string]interface{})
	}
	if masterSchema["types"] == nil {
		masterSchema["types"] = make(map[string]interface{})
	}

	masterResources := masterSchema["resources"].(map[string]interface{})
	masterTypes := masterSchema["types"].(map[string]interface{})

	// 2. Crawl cloud provider directories (aws/, azure/, gcp/ inside provider/)
	entries, err := os.ReadDir(".")
	if err != nil {
		log.Fatalf("❌ Could not read current directory: %v", err)
	}

	// Directories to skip (not cloud providers)
	skip := map[string]bool{"cmd": true, "scripts": true, "sdk": true}
	count := 0

	for _, entry := range entries {
		if !entry.IsDir() || skip[entry.Name()] {
			continue
		}

		providerName := entry.Name()
		resources, err := os.ReadDir(providerName)
		if err != nil {
			continue
		}

		for _, res := range resources {
			if !res.IsDir() {
				continue
			}

			fragmentPath := filepath.Join(providerName, res.Name(), "schema.json")
			fragBytes, err := os.ReadFile(fragmentPath)
			if err != nil {
				continue
			}

			var fragment map[string]interface{}
			if err := json.Unmarshal(fragBytes, &fragment); err != nil {
				fmt.Printf("⚠️  Skipping %s (invalid JSON)\n", fragmentPath)
				continue
			}

			// Merge Resources
			if fr, ok := fragment["resources"].(map[string]interface{}); ok {
				for k, v := range fr {
					masterResources[k] = v
				}
			}

			// Merge Types
			if ft, ok := fragment["types"].(map[string]interface{}); ok {
				for k, v := range ft {
					masterTypes[k] = v
				}
			}

			fmt.Printf("   📦 %s/%s\n", providerName, res.Name())
			count++
		}
	}

	// 3. Write final output (provider/schema.json)
	output, _ := json.MarshalIndent(masterSchema, "", "  ")
	if err := os.WriteFile("schema.json", output, 0644); err != nil {
		log.Fatalf("❌ Could not write schema.json: %v", err)
	}

	fmt.Printf("✅ Merged %d components into schema.json in %v\n", count, time.Since(start))
}
