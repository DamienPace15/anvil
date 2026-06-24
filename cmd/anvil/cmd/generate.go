package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DamienPace15/anvil/codegen"
	"github.com/spf13/cobra"
)

var (
	generateLang   string
	generateOutput string
	generateStage  string
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate typed database client code from your schema",
	Long: `Discovers DSQL schemas from your Pulumi program and generates fully-typed
database interfaces for your language. Produces Row, Insert, and Update types
plus CRUD query helpers for every table in every schema.

Runs a Pulumi preview (no real resources created) to extract schema definitions,
then generates code to .anvil/gen/db/.

Supported languages: typescript, python, go, rust`,
	RunE: runGenerate,
}

func init() {
	generateCmd.Flags().StringVar(&generateLang, "lang", "", "Target language: typescript, python, go, rust (auto-detected from entry point if omitted)")
	generateCmd.Flags().StringVar(&generateOutput, "out", "", "Output directory (default: .anvil/gen/db/)")
	generateCmd.Flags().StringVar(&generateStage, "stage", "", "Stage name for schema discovery")
	rootCmd.AddCommand(generateCmd)
}

var generators = map[string]codegen.Generator{
	"typescript": &codegen.TypeScriptGenerator{},
	"nodejs":     &codegen.TypeScriptGenerator{},
	"python":     &codegen.PythonGenerator{},
	"go":         &codegen.GoGenerator{},
	"rust":       &codegen.RustGenerator{},
}

// refreshGeneratedTypes regenerates typed database client code from the build manifest.
// Called after deploy to keep generated types in sync. Errors are non-fatal.
func refreshGeneratedTypes(manifest *buildManifest) {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return
	}

	rt, err := detectRuntime(projectRoot)
	if err != nil {
		return
	}

	gen, ok := generators[rt.Name]
	if !ok {
		return
	}

	outDir := filepath.Join(projectRoot, ".anvil", "gen", "db")

	entries := make([]codegen.ManifestSchemaEntry, len(manifest.Schemas))
	for i, s := range manifest.Schemas {
		entries[i] = codegen.ManifestSchemaEntry{
			Component:   s.Component,
			SchemasJSON: s.Schemas,
		}
	}

	if err := codegen.RunFromManifest(entries, gen, outDir); err != nil {
		fmt.Printf("  ⚠️  Schema codegen refresh failed: %s\n", err)
		return
	}

	fmt.Printf("  Updated generated %s types in %s\n", gen.Name(), outDir)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}

	lang := generateLang
	if lang == "" {
		rt, err := detectRuntime(projectRoot)
		if err != nil {
			return fmt.Errorf("could not auto-detect language (use --lang): %w", err)
		}
		lang = rt.Name
	}

	gen, ok := generators[lang]
	if !ok {
		return fmt.Errorf("unsupported language %q — supported: typescript, python, go, rust", lang)
	}

	outDir := generateOutput
	if outDir == "" {
		outDir = filepath.Join(projectRoot, ".anvil", "gen", "db")
	}

	stage := resolveStage(generateStage)
	ctx := context.Background()

	fmt.Println("  Discovering schemas...")

	_, err = discoverFunctions(ctx, stage)
	if err != nil {
		return fmt.Errorf("schema discovery failed: %w", err)
	}

	manifest, err := readFullManifest()
	if err != nil {
		return err
	}
	if manifest == nil || len(manifest.Schemas) == 0 {
		return fmt.Errorf("no database schemas found — make sure your Pulumi program defines DSQL schemas")
	}

	entries := make([]codegen.ManifestSchemaEntry, len(manifest.Schemas))
	for i, s := range manifest.Schemas {
		entries[i] = codegen.ManifestSchemaEntry{
			Component:   s.Component,
			SchemasJSON: s.Schemas,
		}
	}

	if err := codegen.RunFromManifest(entries, gen, outDir); err != nil {
		return err
	}

	fmt.Printf("  Generated %s types in %s\n", gen.Name(), outDir)
	return nil
}
