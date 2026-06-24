package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DamienPace15/anvil/codegen"
	"github.com/spf13/cobra"
)

var (
	generateLang   string
	generateOutput string
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate typed database client code from your schema",
	Long: `Reads anvil.schema.json and generates fully-typed database interfaces
for your language. Produces Row, Insert, and Update types plus CRUD query
helpers for every table in every schema.

Supported languages: typescript, python, go, rust

The output directory defaults to .anvil/gen/db/ in your project root.
Add .anvil/gen/ to your .gitignore.`,
	RunE: runGenerate,
}

func init() {
	generateCmd.Flags().StringVar(&generateLang, "lang", "", "Target language: typescript, python, go, rust (auto-detected from entry point if omitted)")
	generateCmd.Flags().StringVar(&generateOutput, "out", "", "Output directory (default: .anvil/gen/db/)")
	rootCmd.AddCommand(generateCmd)
}

var generators = map[string]codegen.Generator{
	"typescript": &codegen.TypeScriptGenerator{},
	"nodejs":     &codegen.TypeScriptGenerator{},
	"python":     &codegen.PythonGenerator{},
	"go":         &codegen.GoGenerator{},
	"rust":       &codegen.RustGenerator{},
}

func runGenerate(cmd *cobra.Command, args []string) error {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}

	schemaPath := filepath.Join(projectRoot, "anvil.schema.json")
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		return fmt.Errorf(
			"No anvil.schema.json found in %s\n"+
				"  Create one with your database schema definitions.\n"+
				"  See: https://anvil.build/docs/codegen",
			projectRoot,
		)
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

	if err := codegen.Run(schemaPath, gen, outDir); err != nil {
		return err
	}

	fmt.Printf("  Generated %s types in %s\n", gen.Name(), outDir)
	return nil
}
