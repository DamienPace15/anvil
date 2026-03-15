package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const awsVersion = "v7.21.0"

func main() {
	fmt.Println("⏳ Fetching official AWS Schema (this might take a moment)...")

	// 1. Fetch AWS Schema
	cmd := exec.Command("pulumi", "package", "get-schema", "aws")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		log.Fatalf("❌ Error fetching AWS schema: %v", err)
	}

	// ── TIMER STARTS AFTER DOWNLOAD ──
	startTime := time.Now()

	var awsSchema map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &awsSchema); err != nil {
		log.Fatalf("❌ Error parsing AWS schema: %v", err)
	}
	fmt.Println("✅ AWS Schema loaded.\n")

	awsResources, ok := awsSchema["resources"].(map[string]interface{})
	if !ok {
		log.Fatal("❌ Invalid AWS schema format: missing 'resources'")
	}

	// 2. Path relative to provider/ (cd provider && go run ../scripts/...)
	awsDir := "aws"

	entries, err := os.ReadDir(awsDir)
	if err != nil {
		log.Fatalf("❌ Error reading aws directory at %s: %v\n   Make sure you're running from the project root with: cd provider && go run ../scripts/generate/generate_schemas.go", awsDir, err)
	}

	// 3. Process each component folder
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := entry.Name()
		schemaPath := filepath.Join(awsDir, folder, "schema.json")

		schemaBytes, err := os.ReadFile(schemaPath)
		if err != nil {
			continue
		}

		var anvilSchema map[string]interface{}
		if err := json.Unmarshal(schemaBytes, &anvilSchema); err != nil {
			log.Printf("⚠️  Error parsing %s: %v", schemaPath, err)
			continue
		}

		targetAwsToken, ok := anvilSchema["x-aws-token"].(string)
		if !ok || targetAwsToken == "" {
			fmt.Printf("⏭️  Skipping aws/%s: Missing \"x-aws-token\"\n", folder)
			continue
		}

		fmt.Printf("⚙️  Processing: aws/%s -> %s\n", folder, targetAwsToken)

		mainResRaw, exists := awsResources[targetAwsToken]
		if !exists {
			fmt.Printf("   ❌ Error: Could not find AWS resource \"%s\"\n", targetAwsToken)
			continue
		}
		mainRes := mainResRaw.(map[string]interface{})
		mainInputsRaw, _ := mainRes["inputProperties"].(map[string]interface{})

		if anvilSchema["types"] == nil {
			anvilSchema["types"] = make(map[string]interface{})
		}
		anvilTypes := anvilSchema["types"].(map[string]interface{})
		anvilResourceName := toPascalCase(folder)

		cleanInputs := make(map[string]interface{})
		extraTransforms := make(map[string]map[string]interface{})
		transformArgsProps := make(map[string]interface{})

		// 4. S3 Bucket Special Logic
		if targetAwsToken == "aws:s3/bucket:Bucket" {
			replacementRegex := regexp.MustCompile(`aws\.s3\.([a-zA-Z0-9]+)`)
			for k, v := range mainInputsRaw {
				prop := v.(map[string]interface{})
				depMsg, hasDep := prop["deprecationMessage"].(string)

				if hasDep && depMsg != "" {
					match := replacementRegex.FindStringSubmatch(depMsg)
					if len(match) > 1 {
						awsResName := match[1]
						lowerFirst := strings.ToLower(awsResName[:1]) + awsResName[1:]
						fullToken := fmt.Sprintf("aws:s3/%s:%s", lowerFirst, awsResName)

						if sidecarRaw, ok := awsResources[fullToken]; ok {
							sidecar := sidecarRaw.(map[string]interface{})
							sidecarInputs := sidecar["inputProperties"].(map[string]interface{})
							rewriteRefs(sidecarInputs)

							typeName := fmt.Sprintf("anvil:aws:%sTransform", awsResName)
							extraTransforms[k] = map[string]interface{}{
								"typeName":   typeName,
								"properties": sidecarInputs,
							}
						}
					}
				} else {
					cleanInputs[k] = v
				}
			}

			pabToken := "aws:s3/bucketPublicAccessBlock:BucketPublicAccessBlock"
			if pabRaw, ok := awsResources[pabToken]; ok {
				pab := pabRaw.(map[string]interface{})
				pabInputs := pab["inputProperties"].(map[string]interface{})
				rewriteRefs(pabInputs)

				anvilTypes["anvil:aws:PABTransform"] = map[string]interface{}{"type": "object", "properties": pabInputs}
				transformArgsProps["publicAccessBlock"] = map[string]interface{}{"$ref": "#/types/anvil:aws:PABTransform"}
			}
		} else {
			// 5. General Logic
			for k, v := range mainInputsRaw {
				prop := v.(map[string]interface{})
				if _, hasDep := prop["deprecationMessage"]; !hasDep {
					cleanInputs[k] = v
				}
			}
		}

		// 6. Final Wiring
		rewriteRefs(cleanInputs)
		transformTypeName := fmt.Sprintf("anvil:aws:%sTransform", anvilResourceName)
		anvilTypes[transformTypeName] = map[string]interface{}{"type": "object", "properties": cleanInputs}
		transformArgsProps[folder] = map[string]interface{}{"$ref": fmt.Sprintf("#/types/%s", transformTypeName)}

		for key, data := range extraTransforms {
			typeName := data["typeName"].(string)
			anvilTypes[typeName] = map[string]interface{}{"type": "object", "properties": data["properties"]}
			transformArgsProps[key] = map[string]interface{}{"$ref": fmt.Sprintf("#/types/%s", typeName)}
		}

		anvilTypes["anvil:aws:TransformArgs"] = map[string]interface{}{"type": "object", "properties": transformArgsProps}

		if resources, ok := anvilSchema["resources"].(map[string]interface{}); ok {
			resKey := fmt.Sprintf("anvil:aws:%s", anvilResourceName)
			if resObj, ok := resources[resKey].(map[string]interface{}); ok {
				if inputs, ok := resObj["inputProperties"].(map[string]interface{}); ok {
					inputs["transform"] = map[string]interface{}{"$ref": "#/types/anvil:aws:TransformArgs"}
				}
			}
		}

		finalJson, _ := json.MarshalIndent(anvilSchema, "", "  ")
		os.WriteFile(schemaPath, finalJson, 0644)
		fmt.Printf("   ✅ Success! Mapped %d additional transforms.\n\n", len(extraTransforms))
	}

	// ── FINAL TIMER LOG (Outside the loop) ──
	fmt.Printf("✨ Total Go processing finished in: %v\n", time.Since(startTime))
}

// ── Helpers ──

func toPascalCase(s string) string {
	parts := regexp.MustCompile(`[-_]`).Split(s, -1)
	var result string
	for _, p := range parts {
		if len(p) > 0 {
			result += strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return result
}

func rewriteRefs(obj interface{}) {
	switch v := obj.(type) {
	case map[string]interface{}:
		for key, val := range v {
			if key == "$ref" {
				if strVal, ok := val.(string); ok && strings.HasPrefix(strVal, "#/types/aws:") {
					v[key] = fmt.Sprintf("/aws/%s/schema.json%s", awsVersion, strVal)
				}
			} else {
				rewriteRefs(val)
			}
		}
	case []interface{}:
		for _, val := range v {
			rewriteRefs(val)
		}
	}
}
