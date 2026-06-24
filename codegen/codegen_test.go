package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSchemaFile(t *testing.T) {
	sf, err := ParseSchemaFile(filepath.Join("testdata", "anvil.schema.json"))
	if err != nil {
		t.Fatalf("ParseSchemaFile: %v", err)
	}

	comp, ok := sf.DSQL["orders"]
	if !ok {
		t.Fatal("expected 'orders' component")
	}
	if len(comp.Schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(comp.Schemas))
	}

	s := comp.Schemas[0]
	if s.Name != "myapp" {
		t.Errorf("expected schema name 'myapp', got %q", s.Name)
	}
	if len(s.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(s.Tables))
	}

	users := s.Tables[0]
	if users.Name != "users" {
		t.Errorf("expected first table 'users', got %q", users.Name)
	}
	if !users.Columns[0].NotNull {
		t.Error("expected PK column 'id' to be auto-NOT-NULL")
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		sf      SchemaFile
		wantErr string
	}{
		{
			name: "empty schema name",
			sf: SchemaFile{DSQL: map[string]DSQLComponent{
				"test": {Schemas: []Schema{{Name: ""}}},
			}},
			wantErr: "schema name must not be empty",
		},
		{
			name: "public schema",
			sf: SchemaFile{DSQL: map[string]DSQLComponent{
				"test": {Schemas: []Schema{{Name: "public"}}},
			}},
			wantErr: "cannot manage the public schema",
		},
		{
			name: "unsupported type",
			sf: SchemaFile{DSQL: map[string]DSQLComponent{
				"test": {Schemas: []Schema{{
					Name: "app",
					Tables: []Table{{
						Name:       "t1",
						PrimaryKey: []string{"id"},
						Columns: []Column{
							{Name: "id", Type: "serial"},
						},
					}},
				}}},
			}},
			wantErr: "unsupported type",
		},
		{
			name: "missing PK column",
			sf: SchemaFile{DSQL: map[string]DSQLComponent{
				"test": {Schemas: []Schema{{
					Name: "app",
					Tables: []Table{{
						Name:       "t1",
						PrimaryKey: []string{"nope"},
						Columns: []Column{
							{Name: "id", Type: "uuid"},
						},
					}},
				}}},
			}},
			wantErr: "primaryKey column \"nope\" not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(&tt.sf)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestTypeScriptGeneration(t *testing.T) {
	sf, err := ParseSchemaFile(filepath.Join("testdata", "anvil.schema.json"))
	if err != nil {
		t.Fatalf("ParseSchemaFile: %v", err)
	}

	outDir := t.TempDir()
	gen := &TypeScriptGenerator{}
	if err := gen.Generate(sf.DSQL, outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "export interface UsersRow")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "export interface UsersInsert")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "export interface OrdersRow")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "createMyappQueries")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "findById")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "insert")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "update")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "delete")
	assertFileContains(t, filepath.Join(outDir, "index.ts"), "export * from './myapp'")
}

func TestPythonGeneration(t *testing.T) {
	sf, err := ParseSchemaFile(filepath.Join("testdata", "anvil.schema.json"))
	if err != nil {
		t.Fatalf("ParseSchemaFile: %v", err)
	}

	outDir := t.TempDir()
	gen := &PythonGenerator{}
	if err := gen.Generate(sf.DSQL, outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	assertFileContains(t, filepath.Join(outDir, "myapp.py"), "class UsersRow")
	assertFileContains(t, filepath.Join(outDir, "myapp.py"), "class UsersInsert")
	assertFileContains(t, filepath.Join(outDir, "myapp.py"), "class MyappQueries")
	assertFileContains(t, filepath.Join(outDir, "myapp.py"), "find_by_id")
	assertFileContains(t, filepath.Join(outDir, "__init__.py"), "from .myapp import")
}

func TestGoGeneration(t *testing.T) {
	sf, err := ParseSchemaFile(filepath.Join("testdata", "anvil.schema.json"))
	if err != nil {
		t.Fatalf("ParseSchemaFile: %v", err)
	}

	outDir := t.TempDir()
	gen := &GoGenerator{}
	if err := gen.Generate(sf.DSQL, outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	assertFileContains(t, filepath.Join(outDir, "myapp.go"), "type UsersRow struct")
	assertFileContains(t, filepath.Join(outDir, "myapp.go"), "type UsersInsert struct")
	assertFileContains(t, filepath.Join(outDir, "myapp.go"), "func (q *UsersQueries) FindByID")
	assertFileContains(t, filepath.Join(outDir, "myapp.go"), "type OrdersRow struct")
}

func TestRustGeneration(t *testing.T) {
	sf, err := ParseSchemaFile(filepath.Join("testdata", "anvil.schema.json"))
	if err != nil {
		t.Fatalf("ParseSchemaFile: %v", err)
	}

	outDir := t.TempDir()
	gen := &RustGenerator{}
	if err := gen.Generate(sf.DSQL, outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	assertFileContains(t, filepath.Join(outDir, "myapp.rs"), "pub struct UsersRow")
	assertFileContains(t, filepath.Join(outDir, "myapp.rs"), "pub struct UsersInsert")
	assertFileContains(t, filepath.Join(outDir, "myapp.rs"), "impl<'a> UsersQueries<'a>")
	assertFileContains(t, filepath.Join(outDir, "myapp.rs"), "pub struct OrdersRow")
	assertFileContains(t, filepath.Join(outDir, "mod.rs"), "pub mod myapp")
}

func TestRunFromManifest(t *testing.T) {
	sf, err := ParseSchemaFile(filepath.Join("testdata", "anvil.schema.json"))
	if err != nil {
		t.Fatalf("ParseSchemaFile: %v", err)
	}

	var entries []ManifestSchemaEntry
	for name, comp := range sf.DSQL {
		raw, err := json.Marshal(comp.Schemas)
		if err != nil {
			t.Fatalf("marshaling schemas: %v", err)
		}
		entries = append(entries, ManifestSchemaEntry{
			Component:   name,
			SchemasJSON: raw,
		})
	}

	outDir := t.TempDir()
	gen := &TypeScriptGenerator{}
	if err := RunFromManifest(entries, gen, outDir); err != nil {
		t.Fatalf("RunFromManifest: %v", err)
	}

	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "export interface UsersRow")
	assertFileContains(t, filepath.Join(outDir, "myapp.ts"), "createMyappQueries")
	assertFileContains(t, filepath.Join(outDir, "index.ts"), "export * from './myapp'")
}

func TestNameConversions(t *testing.T) {
	tests := []struct {
		input  string
		pascal string
		camel  string
		snake  string
	}{
		{"user_id", "UserId", "userId", "user_id"},
		{"created_at", "CreatedAt", "createdAt", "created_at"},
		{"myapp", "Myapp", "myapp", "myapp"},
		{"display-name", "DisplayName", "displayName", "display_name"},
	}

	for _, tt := range tests {
		if got := ToPascalCase(tt.input); got != tt.pascal {
			t.Errorf("ToPascalCase(%q) = %q, want %q", tt.input, got, tt.pascal)
		}
		if got := ToCamelCase(tt.input); got != tt.camel {
			t.Errorf("ToCamelCase(%q) = %q, want %q", tt.input, got, tt.camel)
		}
		if got := ToSnakeCase(tt.input); got != tt.snake {
			t.Errorf("ToSnakeCase(%q) = %q, want %q", tt.input, got, tt.snake)
		}
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Errorf("%s does not contain %q\n\nFull content:\n%s", path, want, string(data))
	}
}
