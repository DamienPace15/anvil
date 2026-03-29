// Schema generator for Anvil site components.
//
// Reads Go struct definitions with `pulumi:"..."` and `schema:"..."` tags,
// extracts doc comments as descriptions, and emits Pulumi schema.json files.
//
// This generator handles embedded structs (shared inputs from internal/sites/)
// so that schemas are written once and reused across providers.
//
// Usage:
//
//	cd provider && go run ../scripts/generate-site-schemas/main.go
//
// Or via Makefile:
//
//	make gen-site-schemas
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// schemaProperty represents a single property in a Pulumi schema.
type schemaProperty struct {
	Type                 string          `json:"type,omitempty"`
	Description          string          `json:"description,omitempty"`
	Items                *schemaProperty `json:"items,omitempty"`
	AdditionalProperties *schemaProperty `json:"additionalProperties,omitempty"`
	Ref                  string          `json:"$ref,omitempty"`
}

// componentSchema is the per-resource schema.json fragment.
type componentSchema struct {
	Resources map[string]resourceDef `json:"resources"`
	Types     map[string]interface{} `json:"types"`
}

type resourceDef struct {
	IsComponent     bool                      `json:"isComponent"`
	Description     string                    `json:"description,omitempty"`
	InputProperties map[string]schemaProperty `json:"inputProperties"`
	RequiredInputs  []string                  `json:"requiredInputs,omitempty"`
	Properties      map[string]schemaProperty `json:"properties,omitempty"`
	Required        []string                  `json:"required,omitempty"`
}

// parsedField holds extracted field info from Go AST.
type parsedField struct {
	PulumiName  string
	GoType      string
	Description string
	Required    bool
	Optional    bool
	IsOutput    bool // true if the Go type is pulumi.*Output
}

// componentConfig describes a component to generate a schema for.
type componentConfig struct {
	// Cloud provider name (aws, gcp).
	Cloud string
	// Resource directory name (sveltekitsite).
	Resource string
	// Pulumi token (e.g. anvil:aws:SvelteKitSite).
	Token string
	// Path to the component's Go file, relative to provider/.
	GoFile string
	// Name of the Args struct in the component file.
	ArgsStruct string
	// Name of the component struct in the component file.
	ComponentStruct string
}

func main() {
	fmt.Println("🔧 Starting Site Schema Generator...")
	start := time.Now()

	// Define components to generate schemas for.
	// When you add a new site component, add an entry here.
	components := []componentConfig{
		{
			Cloud:           "aws",
			Resource:        "sveltekitsite",
			Token:           "anvil:aws:SvelteKitSite",
			GoFile:          "aws/sveltekitsite/sveltekitsite.go",
			ArgsStruct:      "SvelteKitSiteArgs",
			ComponentStruct: "SvelteKitSite",
		},
		{
			Cloud:           "gcp",
			Resource:        "sveltekitsite",
			Token:           "anvil:gcp:SvelteKitSite",
			GoFile:          "gcp/sveltekitsite/sveltekitsite.go",
			ArgsStruct:      "SvelteKitSiteArgs",
			ComponentStruct: "SvelteKitSite",
		},
	}

	// Parse the shared types file once — embedded structs reference these.
	sharedTypesFile := "internal/sites/types.go"
	sharedStructs, err := parseGoFile(sharedTypesFile)
	if err != nil {
		log.Fatalf("❌ Failed to parse shared types %s: %v", sharedTypesFile, err)
	}

	for _, comp := range components {
		fmt.Printf("   📦 %s/%s\n", comp.Cloud, comp.Resource)

		// Parse the component's Go file.
		componentStructs, err := parseGoFile(comp.GoFile)
		if err != nil {
			log.Fatalf("❌ Failed to parse %s: %v", comp.GoFile, err)
		}

		// Extract input properties from ArgsStruct.
		inputFields, err := resolveFields(comp.ArgsStruct, componentStructs, sharedStructs)
		if err != nil {
			log.Fatalf("❌ Failed to resolve inputs for %s: %v", comp.Token, err)
		}

		// Extract output properties from ComponentStruct.
		outputFields, err := resolveFields(comp.ComponentStruct, componentStructs, sharedStructs)
		if err != nil {
			log.Fatalf("❌ Failed to resolve outputs for %s: %v", comp.Token, err)
		}

		// Build schema.
		inputProps := make(map[string]schemaProperty)
		var requiredInputs []string
		for _, f := range inputFields {
			inputProps[f.PulumiName] = goTypeToSchemaProperty(f)
			if f.Required {
				requiredInputs = append(requiredInputs, f.PulumiName)
			}
		}

		outputProps := make(map[string]schemaProperty)
		var requiredOutputs []string
		for _, f := range outputFields {
			if !f.IsOutput {
				continue // Skip non-output fields (ResourceState, etc.)
			}
			outputProps[f.PulumiName] = goTypeToSchemaProperty(f)
			if !f.Optional {
				requiredOutputs = append(requiredOutputs, f.PulumiName)
			}
		}

		// Build the component description from the struct's doc comment.
		compDescription := ""
		if structInfo, ok := componentStructs[comp.ComponentStruct]; ok {
			compDescription = structInfo.Doc
		}

		resDef := resourceDef{
			IsComponent:     true,
			Description:     compDescription,
			InputProperties: inputProps,
			RequiredInputs:  requiredInputs,
			Properties:      outputProps,
			Required:        requiredOutputs,
		}

		schema := componentSchema{
			Resources: map[string]resourceDef{
				comp.Token: resDef,
			},
			Types: map[string]interface{}{},
		}

		// Write schema.json.
		outPath := filepath.Join(comp.Cloud, comp.Resource, "schema.json")
		if err := writeSchemaJSON(outPath, schema); err != nil {
			log.Fatalf("❌ Failed to write %s: %v", outPath, err)
		}
	}

	fmt.Printf("✅ Generated %d site schemas in %v\n", len(components), time.Since(start))
}

// structInfo holds parsed struct data.
type structInfo struct {
	Doc    string
	Fields []parsedField
	// EmbeddedTypes lists the short names of embedded struct types
	// (e.g. "SvelteKitSiteInputs" from sites.SvelteKitSiteInputs).
	EmbeddedTypes []string
}

// parseGoFile parses a Go source file and extracts all struct definitions
// with their fields, tags, and doc comments.
func parseGoFile(path string) (map[string]*structInfo, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	structs := make(map[string]*structInfo)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			info := &structInfo{}

			// Extract doc comment from the type declaration.
			if genDecl.Doc != nil {
				info.Doc = cleanDocComment(genDecl.Doc.Text())
			}
			// TypeSpec-level doc takes precedence if present.
			if typeSpec.Doc != nil {
				info.Doc = cleanDocComment(typeSpec.Doc.Text())
			}

			for _, field := range structType.Fields.List {
				// Handle embedded struct (no names).
				if len(field.Names) == 0 {
					embeddedName := resolveTypeName(field.Type)
					if embeddedName != "" {
						info.EmbeddedTypes = append(info.EmbeddedTypes, embeddedName)
					}
					continue
				}

				// Named field — extract pulumi tag and doc comment.
				if field.Tag == nil {
					continue
				}

				tag := field.Tag.Value
				pulumiTag := extractTag(tag, "pulumi")
				if pulumiTag == "" {
					continue
				}

				pf := parsedField{}

				// Parse pulumi tag: "name,optional" or just "name".
				parts := strings.SplitN(pulumiTag, ",", 2)
				pf.PulumiName = parts[0]
				if len(parts) > 1 && parts[1] == "optional" {
					pf.Optional = true
				}

				// Parse schema tag for "required" override.
				schemaTag := extractTag(tag, "schema")
				if strings.Contains(schemaTag, "required") {
					pf.Required = true
					pf.Optional = false
				} else if !pf.Optional {
					// If not explicitly optional and not explicitly required,
					// default to optional (safe default).
					pf.Optional = true
				}

				// Extract Go type as string.
				pf.GoType = typeToString(field.Type)

				// Check if it's a pulumi.*Output type.
				pf.IsOutput = strings.Contains(pf.GoType, "Output")

				// Extract doc comment.
				if field.Doc != nil {
					pf.Description = cleanDocComment(field.Doc.Text())
				}

				info.Fields = append(info.Fields, pf)
			}

			structs[typeSpec.Name.Name] = info
		}
	}

	return structs, nil
}

// resolveFields collects all fields for a struct, including fields from
// embedded structs. It looks up embedded types in both the component file's
// structs and the shared types file's structs.
func resolveFields(structName string, componentStructs, sharedStructs map[string]*structInfo) ([]parsedField, error) {
	info, ok := componentStructs[structName]
	if !ok {
		return nil, fmt.Errorf("struct %s not found in component file", structName)
	}

	var allFields []parsedField

	// First, resolve embedded types.
	for _, embeddedName := range info.EmbeddedTypes {
		// Look in shared structs first, then component structs.
		if shared, ok := sharedStructs[embeddedName]; ok {
			// Recursively resolve (in case shared struct also embeds).
			embedded, err := resolveFieldsFromInfo(shared, sharedStructs)
			if err != nil {
				return nil, fmt.Errorf("resolving embedded %s: %w", embeddedName, err)
			}
			allFields = append(allFields, embedded...)
		} else if local, ok := componentStructs[embeddedName]; ok {
			embedded, err := resolveFieldsFromInfo(local, componentStructs)
			if err != nil {
				return nil, fmt.Errorf("resolving embedded %s: %w", embeddedName, err)
			}
			allFields = append(allFields, embedded...)
		}
		// Skip unresolvable embeds (like pulumi.ResourceState) — they're not schema fields.
	}

	// Then add the struct's own fields.
	allFields = append(allFields, info.Fields...)

	return allFields, nil
}

// resolveFieldsFromInfo is the recursive helper for resolveFields.
func resolveFieldsFromInfo(info *structInfo, available map[string]*structInfo) ([]parsedField, error) {
	var allFields []parsedField

	for _, embeddedName := range info.EmbeddedTypes {
		if embedded, ok := available[embeddedName]; ok {
			fields, err := resolveFieldsFromInfo(embedded, available)
			if err != nil {
				return nil, err
			}
			allFields = append(allFields, fields...)
		}
	}

	allFields = append(allFields, info.Fields...)
	return allFields, nil
}

// goTypeToSchemaProperty maps a Go type to a Pulumi schema property.
func goTypeToSchemaProperty(f parsedField) schemaProperty {
	prop := schemaProperty{
		Description: f.Description,
	}

	// Strip pulumi. wrapper types for output detection.
	goType := f.GoType
	goType = strings.TrimPrefix(goType, "pulumi.")
	goType = strings.TrimSuffix(goType, "Output")

	switch {
	case goType == "string" || goType == "String":
		prop.Type = "string"
	case goType == "int" || goType == "Int" || goType == "int64":
		prop.Type = "integer"
	case goType == "float64" || goType == "Float64":
		prop.Type = "number"
	case goType == "bool" || goType == "Bool":
		prop.Type = "boolean"
	case strings.HasPrefix(goType, "map[string]string") || strings.HasPrefix(goType, "Map"):
		prop.Type = "object"
		prop.AdditionalProperties = &schemaProperty{Type: "string"}
	case strings.HasPrefix(goType, "[]"):
		prop.Type = "array"
		elemType := strings.TrimPrefix(goType, "[]")
		prop.Items = &schemaProperty{Type: goTypeToSchemaType(elemType)}
	default:
		// Fallback — treat unknown types as string.
		// This handles cases like custom types we haven't mapped.
		prop.Type = "string"
	}

	return prop
}

// goTypeToSchemaType maps a simple Go type name to a Pulumi schema type string.
func goTypeToSchemaType(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "int", "int64":
		return "integer"
	case "float64":
		return "number"
	case "bool":
		return "boolean"
	default:
		return "string"
	}
}

// typeToString converts an ast.Expr type to a readable string.
func typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeToString(t.X) + "." + t.Sel.Name
	case *ast.MapType:
		return "map[" + typeToString(t.Key) + "]" + typeToString(t.Value)
	case *ast.ArrayType:
		return "[]" + typeToString(t.Elt)
	case *ast.StarExpr:
		return "*" + typeToString(t.X)
	default:
		return "unknown"
	}
}

// resolveTypeName extracts the short struct name from an embedded type expression.
// e.g. sites.SvelteKitSiteInputs → SvelteKitSiteInputs
//
//	pulumi.ResourceState → ResourceState
func resolveTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return resolveTypeName(t.X)
	default:
		return ""
	}
}

// extractTag pulls a specific tag value from a raw Go struct tag string.
// e.g. extractTag(`\x60pulumi:"path" schema:"required"\x60`, "pulumi") → "path"
func extractTag(raw string, key string) string {
	// Strip backticks.
	raw = strings.Trim(raw, "`")

	search := key + `:"`
	idx := strings.Index(raw, search)
	if idx == -1 {
		return ""
	}

	start := idx + len(search)
	end := strings.Index(raw[start:], `"`)
	if end == -1 {
		return ""
	}

	return raw[start : start+end]
}

// cleanDocComment trims and normalises a Go doc comment string.
func cleanDocComment(s string) string {
	s = strings.TrimSpace(s)
	// Collapse multiple whitespace/newlines into single spaces.
	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, " ")
}

// writeSchemaJSON writes a componentSchema to disk as indented JSON.
func writeSchemaJSON(path string, schema componentSchema) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
