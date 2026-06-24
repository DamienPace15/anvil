package codegen

// Schema types mirror provider/aws/dsql/dsql.go but without Pulumi dependencies.
// This keeps the codegen engine self-contained and importable from the CLI.

type SchemaFile struct {
	DSQL map[string]DSQLComponent `json:"dsql"`
}

type DSQLComponent struct {
	Schemas []Schema `json:"schemas"`
}

type Schema struct {
	Name   string  `json:"name"`
	Roles  []Role  `json:"roles,omitempty"`
	Tables []Table `json:"tables,omitempty"`
}

type Role struct {
	Name         string   `json:"name"`
	Grants       []string `json:"grants"`
	TableScoping []string `json:"tableScoping,omitempty"`
}

type Table struct {
	Name       string   `json:"name"`
	Columns    []Column `json:"columns"`
	PrimaryKey []string `json:"primaryKey"`
	Indexes    []Index  `json:"indexes,omitempty"`
}

type Column struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Length    int    `json:"length,omitempty"`
	Precision int    `json:"precision,omitempty"`
	Scale     int    `json:"scale,omitempty"`
	NotNull   bool   `json:"notNull,omitempty"`
	Unique    bool   `json:"unique,omitempty"`
	Default   string `json:"default,omitempty"`
}

type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

// Generator is the interface each language implements.
// Adding a new language = one new file implementing this interface.
type Generator interface {
	Name() string
	Generate(schemas map[string]DSQLComponent, outDir string) error
}

// TypeMapping holds the native type string for each supported language.
type TypeMapping struct {
	TypeScript string
	Python     string
	Go         string
	Rust       string
}

var supportedTypes = map[string]bool{
	"uuid": true, "text": true, "varchar": true, "char": true,
	"boolean": true, "smallint": true, "integer": true, "bigint": true,
	"real": true, "double precision": true, "numeric": true,
	"date": true, "time": true, "timestamp": true, "timestamptz": true,
	"interval": true, "bytea": true, "json": true, "jsonb": true,
}

var typeMappings = map[string]TypeMapping{
	"uuid":             {"string", "str", "string", "uuid::Uuid"},
	"text":             {"string", "str", "string", "String"},
	"varchar":          {"string", "str", "string", "String"},
	"char":             {"string", "str", "string", "String"},
	"boolean":          {"boolean", "bool", "bool", "bool"},
	"smallint":         {"number", "int", "int16", "i16"},
	"integer":          {"number", "int", "int32", "i32"},
	"bigint":           {"bigint", "int", "int64", "i64"},
	"real":             {"number", "float", "float32", "f32"},
	"double precision": {"number", "float", "float64", "f64"},
	"numeric":          {"string", "Decimal", "string", "rust_decimal::Decimal"},
	"date":             {"string", "date", "time.Time", "chrono::NaiveDate"},
	"time":             {"string", "time", "string", "chrono::NaiveTime"},
	"timestamp":        {"Date", "datetime", "time.Time", "chrono::NaiveDateTime"},
	"timestamptz":      {"Date", "datetime", "time.Time", "chrono::DateTime<Utc>"},
	"interval":         {"string", "timedelta", "string", "String"},
	"bytea":            {"Buffer", "bytes", "[]byte", "Vec<u8>"},
	"json":             {"unknown", "Any", "json.RawMessage", "serde_json::Value"},
	"jsonb":            {"unknown", "Any", "json.RawMessage", "serde_json::Value"},
}
