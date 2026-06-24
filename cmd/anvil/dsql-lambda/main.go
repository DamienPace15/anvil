package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	dsqlauth "github.com/aws/aws-sdk-go-v2/feature/dsql/auth"
	"github.com/jackc/pgx/v5"
)

// ── NOTES FOR FRONTEND / DASHBOARD ──────────────────────────────────────────
//
// This Lambda is triggered ONLY by a DynamoDB Stream. It cannot be invoked
// directly — no lambda:InvokeFunction is granted to anything.
//
// The cluster endpoint and region are set as Lambda environment variables
// (DSQL_ENDPOINT, DSQL_REGION) — they are NOT stored in the DynamoDB items.
// Each DSQL component has its own table and Lambda, so multi-cluster
// deployments are fully isolated.
//
// DynamoDB table schema (composite key):
//
//   PK: roleName (S)   — Postgres role name, e.g. "app_role"
//   SK: sk (S)         — discriminator: "DEFINITION" or an IAM role ARN
//
// Two item types:
//
//   1. Role definition (sk = "DEFINITION"):
//      Written by Pulumi from the roles[] array on the DSQL component.
//      Attributes: schema (S), grants (L of S, e.g. ["SELECT", "INSERT"])
//
//      INSERT → CREATE SCHEMA, CREATE ROLE, GRANT USAGE, GRANT privileges,
//               ALTER DEFAULT PRIVILEGES
//      MODIFY → REVOKE old, GRANT new
//      REMOVE → REVOKE ALL, DROP OWNED BY, DROP ROLE
//
//   2. IAM mapping (sk = full IAM role ARN):
//      Written by DSQLConnect component (via dsql.grantConnect).
//      No extra attributes — roleName + ARN is all this Lambda needs.
//
//      INSERT → AWS IAM GRANT "roleName" TO 'arn:...'
//      REMOVE → AWS IAM REVOKE "roleName" FROM 'arn:...'
//
// The Lambda connects as admin using dsql:DbConnectAdmin IAM auth.
// Auth tokens are generated per-request — no long-lived credentials.
//
// Error handling: if SQL fails, the Lambda returns an error and the DynamoDB
// stream retries (up to the stream retention window of 24 hours). Failed
// items stay in the stream until processed or expired.
//
// ─────────────────────────────────────────────────────────────────────────────

var (
	dsqlEndpoint = os.Getenv("DSQL_ENDPOINT")
	dsqlRegion   = os.Getenv("DSQL_REGION")
)

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event events.DynamoDBEvent) error {
	log.Printf("[dsql-bootstrap] received %d stream record(s) | endpoint=%s region=%s",
		len(event.Records), dsqlEndpoint, dsqlRegion)

	for _, record := range event.Records {
		if err := processRecord(ctx, record); err != nil {
			log.Printf("[dsql-bootstrap] ERROR processing record %s: %v", record.EventID, err)
			return fmt.Errorf("failed to process record %s: %w", record.EventID, err)
		}
	}

	log.Printf("[dsql-bootstrap] all %d record(s) processed successfully", len(event.Records))
	return nil
}

func processRecord(ctx context.Context, record events.DynamoDBEventRecord) error {
	// Dispatch order:
	//   kind == "table"  → table DDL (create/additive-alter; no-op on removal)
	//   sk  starts "arn:" → IAM GRANT/REVOKE mapping
	//   else             → role definition (sk == "DEFINITION")
	var image map[string]events.DynamoDBAttributeValue
	if record.Change.NewImage != nil {
		image = record.Change.NewImage
	} else {
		image = record.Change.OldImage
	}

	if isTable(image) {
		log.Printf("[dsql-bootstrap] record %s | type=TABLE event=%s id=%q",
			record.EventID, record.EventName, image["roleName"].String())
		return processTable(ctx, record)
	}

	if isIAMMapping(image) {
		log.Printf("[dsql-bootstrap] record %s | type=IAM_MAPPING event=%s roleName=%q sk=%q",
			record.EventID, record.EventName, image["roleName"].String(), image["sk"].String())
		return processIAMMapping(ctx, record)
	}

	log.Printf("[dsql-bootstrap] record %s | type=ROLE_DEFINITION event=%s roleName=%q schema=%q",
		record.EventID, record.EventName, image["roleName"].String(), image["schema"].String())

	switch record.EventName {
	case "INSERT":
		return handleInsert(ctx, record.Change.NewImage)
	case "MODIFY":
		return handleModify(ctx, record.Change.OldImage, record.Change.NewImage)
	case "REMOVE":
		return handleRemove(ctx, record.Change.OldImage)
	default:
		return nil
	}
}

// ── INSERT: create schema, role, and grants ─────────────────────────────────

func handleInsert(ctx context.Context, newImage map[string]events.DynamoDBAttributeValue) error {
	role := extractRoleConfig(newImage)

	conn, err := connectAdmin(ctx)
	if err != nil {
		return fmt.Errorf("admin connect failed: %w", err)
	}
	defer conn.Close(ctx)

	if len(role.TableScoping) > 0 {
		log.Printf("[dsql-bootstrap] INSERT role %q in schema %q | grants %v | scoped to tables %v",
			role.RoleName, role.Schema, role.Grants, role.TableScoping)
	} else {
		log.Printf("[dsql-bootstrap] INSERT role %q in schema %q | grants %v | schema-wide",
			role.RoleName, role.Schema, role.Grants)
	}

	// Schema (idempotent) then the role. DSQL has no CREATE ROLE IF NOT EXISTS,
	// so check pg_roles first and only create when absent — makes INSERT
	// re-runnable on retries/redeploys without a "role already exists" error.
	if err := execAll(ctx, conn, []string{
		fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pgIdent(role.Schema)),
	}); err != nil {
		return err
	}

	if err := ensureRole(ctx, conn, role.RoleName); err != nil {
		return err
	}

	// Grants are re-runnable (GRANT is idempotent).
	if err := execAll(ctx, conn, buildRoleGrantStatements(role)); err != nil {
		return err
	}

	logRoleGrants(ctx, conn, role.Schema, role.RoleName)
	return nil
}

// ensureRole creates the role only if it doesn't already exist. DSQL/Postgres
// has no CREATE ROLE IF NOT EXISTS, and DSQL forbids DDL inside DO/PLpgSQL
// blocks, so this is a SELECT (its own txn) followed by a conditional CREATE
// ROLE (its own txn) — both satisfying DSQL's one-DDL-per-transaction rule.
func ensureRole(ctx context.Context, conn *pgx.Conn, roleName string) error {
	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", roleName,
	).Scan(&exists); err != nil {
		return fmt.Errorf("checking role %q existence: %w", roleName, err)
	}
	if exists {
		log.Printf("[dsql-bootstrap]   role %q already exists — skipping CREATE ROLE", roleName)
		return nil
	}
	sql := fmt.Sprintf("CREATE ROLE %s WITH LOGIN", pgIdent(roleName))
	return execAll(ctx, conn, []string{sql})
}

// ── MODIFY: diff grants and update ──────────────────────────────────────────

func handleModify(ctx context.Context, oldImage, newImage map[string]events.DynamoDBAttributeValue) error {
	oldRole := extractRoleConfig(oldImage)
	newRole := extractRoleConfig(newImage)

	conn, err := connectAdmin(ctx)
	if err != nil {
		return fmt.Errorf("admin connect failed: %w", err)
	}
	defer conn.Close(ctx)

	// Revoke-all-then-re-grant: reset everything the role had on the old schema,
	// then apply the new spec (schema-wide or table-scoped). Safe and simple —
	// no per-privilege diffing. Runs only at deploy time, not on app traffic.
	//
	// Phase 1 — revoke old privileges, BEST-EFFORT. The old schema may no longer
	// exist (e.g. it was renamed or dropped), in which case the REVOKE fails with
	// 3F000 and there is simply nothing left to revoke. These must NOT abort the
	// re-grant below: doing so would leave the role with zero privileges on the
	// new schema (the exact failure mode where a schema rename strands a role).
	revokes := []string{
		fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s",
			pgIdent(oldRole.Schema), pgIdent(oldRole.RoleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s REVOKE ALL ON TABLES FROM %s",
			pgIdent(oldRole.Schema), pgIdent(oldRole.RoleName)),
	}
	if oldRole.Schema != newRole.Schema {
		revokes = append(revokes,
			fmt.Sprintf("REVOKE USAGE ON SCHEMA %s FROM %s", pgIdent(oldRole.Schema), pgIdent(oldRole.RoleName)))
	}
	execBestEffort(ctx, conn, revokes)

	// Phase 2 — apply the new spec. These MUST succeed (the schema/tables may not
	// exist yet on the first attempt, in which case the stream retries until the
	// table-creation records have run — same self-healing path as INSERT).
	statements := []string{
		fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pgIdent(newRole.Schema)),
	}
	statements = append(statements, buildRoleGrantStatements(newRole)...)

	return execAll(ctx, conn, statements)
}

// ── REMOVE: revoke everything and drop role ─────────────────────────────────
//
// Best-effort: cleanup statements are logged on failure but do NOT return an
// error. A REMOVE that errored would block (poison) the DynamoDB stream and
// retry forever, stalling every record behind it. The revokes have already
// removed the role's access by the time a drop might fail, so partial cleanup
// is acceptable — far better than a stuck stream.
//
// Note: DSQL does not support DROP OWNED BY (SQLSTATE 0A000). It isn't needed —
// app roles own no objects (the bootstrap Lambda creates tables as admin), so
// DROP ROLE after the revokes is sufficient.
func handleRemove(ctx context.Context, oldImage map[string]events.DynamoDBAttributeValue) error {
	role := extractRoleConfig(oldImage)

	conn, err := connectAdmin(ctx)
	if err != nil {
		// Connection failure IS worth retrying (transient) — return the error.
		return fmt.Errorf("admin connect failed: %w", err)
	}
	defer conn.Close(ctx)

	statements := []string{
		fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s",
			pgIdent(role.Schema), pgIdent(role.RoleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s REVOKE ALL ON TABLES FROM %s",
			pgIdent(role.Schema), pgIdent(role.RoleName)),
		fmt.Sprintf("REVOKE USAGE ON SCHEMA %s FROM %s",
			pgIdent(role.Schema), pgIdent(role.RoleName)),
		fmt.Sprintf("DROP ROLE IF EXISTS %s", pgIdent(role.RoleName)),
	}

	execBestEffort(ctx, conn, statements)
	return nil
}

// ── IAM mapping items (sk = IAM role ARN) ───────────────────────────────────

func isIAMMapping(image map[string]events.DynamoDBAttributeValue) bool {
	sk := image["sk"].String()
	return strings.HasPrefix(sk, "arn:")
}

func processIAMMapping(ctx context.Context, record events.DynamoDBEventRecord) error {
	switch record.EventName {
	case "INSERT":
		roleName := record.Change.NewImage["roleName"].String()
		iamArn := record.Change.NewImage["sk"].String()

		log.Printf("[dsql-bootstrap] INSERT IAM mapping: db role %q ← IAM %s", roleName, iamArn)

		conn, err := connectAdmin(ctx)
		if err != nil {
			return fmt.Errorf("admin connect failed: %w", err)
		}
		defer conn.Close(ctx)

		sql := fmt.Sprintf("AWS IAM GRANT %s TO '%s'", pgIdent(roleName), iamArn)
		log.Printf("[dsql-bootstrap]   SQL → %s", sql)
		if _, err := conn.Exec(ctx, sql); err != nil {
			log.Printf("[dsql-bootstrap]   SQL FAILED → %s | %v", sql, err)
			return fmt.Errorf("SQL failed [%s]: %w", sql, err)
		}
		log.Printf("[dsql-bootstrap]   SQL OK → AWS IAM GRANT")

		// Confirm the mapping is actually present in DSQL's catalog.
		logIAMMappings(ctx, conn, roleName)
		return nil

	case "REMOVE":
		roleName := record.Change.OldImage["roleName"].String()
		iamArn := record.Change.OldImage["sk"].String()

		log.Printf("[dsql-bootstrap] REMOVE IAM mapping: db role %q ✗ IAM %s", roleName, iamArn)

		conn, err := connectAdmin(ctx)
		if err != nil {
			return fmt.Errorf("admin connect failed: %w", err)
		}
		defer conn.Close(ctx)

		// Best-effort: a failed REVOKE must not poison the stream. If the role
		// was already dropped, the mapping is gone anyway.
		execBestEffort(ctx, conn, []string{
			fmt.Sprintf("AWS IAM REVOKE %s FROM '%s'", pgIdent(roleName), iamArn),
		})
		return nil

	default:
		return nil
	}
}

// ── Table items (kind = "table") ────────────────────────────────────────────
//
// Additive-only. The full table definition is stored as a JSON string in the
// "definition" attribute (mirrors provider/aws/dsql tableDefPayload).
//
//   INSERT → CREATE SCHEMA IF NOT EXISTS, CREATE TABLE IF NOT EXISTS,
//            CREATE INDEX ASYNC for each index
//   MODIFY → diff old vs new columns/indexes; ADD COLUMN / CREATE INDEX ASYNC
//            for new ones only. Removed columns/indexes and type changes are
//            ignored (destructive changes are human-owned).
//   REMOVE → no-op (the table stays in DSQL; only Pulumi state drops it)

type columnDef struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Length    int    `json:"length,omitempty"`
	Precision int    `json:"precision,omitempty"`
	Scale     int    `json:"scale,omitempty"`
	NotNull   bool   `json:"notNull,omitempty"`
	Unique    bool   `json:"unique,omitempty"`
	Default   string `json:"default,omitempty"`
}

type indexDef struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

type tableDef struct {
	Schema     string      `json:"schema"`
	Name       string      `json:"name"`
	PrimaryKey []string    `json:"primaryKey"`
	Columns    []columnDef `json:"columns"`
	Indexes    []indexDef  `json:"indexes,omitempty"`
}

func isTable(image map[string]events.DynamoDBAttributeValue) bool {
	if kind, ok := image["kind"]; ok {
		return kind.String() == "table"
	}
	return false
}

func parseTableDef(image map[string]events.DynamoDBAttributeValue) (tableDef, error) {
	var def tableDef
	raw := image["definition"].String()
	if raw == "" {
		return def, fmt.Errorf("table item missing definition attribute")
	}
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		return def, fmt.Errorf("parsing table definition: %w", err)
	}
	return def, nil
}

func processTable(ctx context.Context, record events.DynamoDBEventRecord) error {
	switch record.EventName {
	case "INSERT":
		def, err := parseTableDef(record.Change.NewImage)
		if err != nil {
			return err
		}
		return createTable(ctx, def)

	case "MODIFY":
		oldDef, err := parseTableDef(record.Change.OldImage)
		if err != nil {
			return err
		}
		newDef, err := parseTableDef(record.Change.NewImage)
		if err != nil {
			return err
		}
		return alterTableAdditive(ctx, oldDef, newDef)

	case "REMOVE":
		// Destructive — Anvil never drops tables. The table stays in DSQL;
		// only the Pulumi state / DynamoDB item is removed.
		def, _ := parseTableDef(record.Change.OldImage)
		log.Printf("[dsql-bootstrap] REMOVE table %s.%s — no-op (destructive changes are human-owned; table left intact)",
			def.Schema, def.Name)
		return nil

	default:
		return nil
	}
}

// createTable creates the schema, the table, and its indexes (async).
func createTable(ctx context.Context, def tableDef) error {
	log.Printf("[dsql-bootstrap] CREATE table %s.%s (%d columns, pk=%v, %d indexes)",
		def.Schema, def.Name, len(def.Columns), def.PrimaryKey, len(def.Indexes))

	conn, err := connectAdmin(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	// Schema first (its own transaction), then table (its own transaction).
	if err := execAll(ctx, conn, []string{
		fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pgIdent(def.Schema)),
		buildCreateTable(def),
	}); err != nil {
		return err
	}

	// Indexes — each async, fire-and-log.
	for _, idx := range def.Indexes {
		if err := createIndexAsync(ctx, conn, def, idx); err != nil {
			return err
		}
	}

	// Column-level unique → named async unique index.
	for _, c := range def.Columns {
		if c.Unique {
			if err := createIndexAsync(ctx, conn, def, indexDef{
				Name:    fmt.Sprintf("%s_%s_key", def.Name, c.Name),
				Columns: []string{c.Name},
				Unique:  true,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

// alterTableAdditive adds columns and indexes present in new but not old.
// Removed columns/indexes and any type changes are ignored.
func alterTableAdditive(ctx context.Context, oldDef, newDef tableDef) error {
	oldCols := map[string]bool{}
	for _, c := range oldDef.Columns {
		oldCols[c.Name] = true
	}
	oldIdx := map[string]bool{}
	for _, x := range oldDef.Indexes {
		oldIdx[x.Name] = true
	}

	var newColumns []columnDef
	for _, c := range newDef.Columns {
		if !oldCols[c.Name] {
			newColumns = append(newColumns, c)
		}
	}
	var newIndexes []indexDef
	for _, x := range newDef.Indexes {
		if !oldIdx[x.Name] {
			newIndexes = append(newIndexes, x)
		}
	}

	// New column-level unique indexes (column added with unique:true).
	for _, c := range newColumns {
		if c.Unique {
			newIndexes = append(newIndexes, indexDef{
				Name:    fmt.Sprintf("%s_%s_key", newDef.Name, c.Name),
				Columns: []string{c.Name},
				Unique:  true,
			})
		}
	}

	if len(newColumns) == 0 && len(newIndexes) == 0 {
		log.Printf("[dsql-bootstrap] MODIFY table %s.%s — no additive changes (removals/type-changes ignored)",
			newDef.Schema, newDef.Name)
		return nil
	}

	log.Printf("[dsql-bootstrap] MODIFY table %s.%s — adding %d column(s), %d index(es)",
		newDef.Schema, newDef.Name, len(newColumns), len(newIndexes))

	conn, err := connectAdmin(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	for _, c := range newColumns {
		sql := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN IF NOT EXISTS %s",
			pgIdent(newDef.Schema), pgIdent(newDef.Name), buildColumnDDL(c))
		if err := execAll(ctx, conn, []string{sql}); err != nil {
			return err
		}
	}
	for _, idx := range newIndexes {
		if err := createIndexAsync(ctx, conn, newDef, idx); err != nil {
			return err
		}
	}
	return nil
}

// buildCreateTable composes the CREATE TABLE statement.
func buildCreateTable(def tableDef) string {
	parts := make([]string, 0, len(def.Columns)+1)
	for _, c := range def.Columns {
		parts = append(parts, buildColumnDDL(c))
	}
	pkCols := make([]string, len(def.PrimaryKey))
	for i, pk := range def.PrimaryKey {
		pkCols[i] = pgIdent(pk)
	}
	parts = append(parts, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkCols, ", ")))

	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (%s)",
		pgIdent(def.Schema), pgIdent(def.Name), strings.Join(parts, ", "))
}

// buildColumnDDL composes one column's DDL fragment (no PK — that's table-level).
func buildColumnDDL(c columnDef) string {
	typeStr := c.Type
	switch c.Type {
	case "varchar", "char":
		if c.Length > 0 {
			typeStr = fmt.Sprintf("%s(%d)", c.Type, c.Length)
		}
	case "numeric":
		if c.Precision > 0 {
			if c.Scale > 0 {
				typeStr = fmt.Sprintf("numeric(%d,%d)", c.Precision, c.Scale)
			} else {
				typeStr = fmt.Sprintf("numeric(%d)", c.Precision)
			}
		}
	}

	ddl := fmt.Sprintf("%s %s", pgIdent(c.Name), typeStr)
	if c.NotNull {
		ddl += " NOT NULL"
	}
	if c.Default != "" {
		ddl += " DEFAULT " + c.Default // raw passthrough expression
	}
	return ddl
}

// createIndexAsync fires CREATE INDEX ASYNC and logs the job_id (fire-and-log,
// no polling). DSQL requires the ASYNC variant; the build runs in the background.
func createIndexAsync(ctx context.Context, conn *pgx.Conn, def tableDef, idx indexDef) error {
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = pgIdent(c)
	}
	unique := ""
	if idx.Unique {
		unique = "UNIQUE "
	}
	sql := fmt.Sprintf("CREATE %sINDEX ASYNC IF NOT EXISTS %s ON %s.%s (%s)",
		unique, pgIdent(idx.Name), pgIdent(def.Schema), pgIdent(def.Name), strings.Join(cols, ", "))

	log.Printf("[dsql-bootstrap]   SQL → %s", sql)
	var jobID string
	// CREATE INDEX ASYNC returns a job_id row.
	if err := conn.QueryRow(ctx, sql).Scan(&jobID); err != nil {
		// Some drivers return no row for IF NOT EXISTS no-op; treat as non-fatal
		// only if it's a "no rows" case, otherwise surface it.
		if err.Error() == "no rows in result set" {
			log.Printf("[dsql-bootstrap]   index %q already exists or no job returned", idx.Name)
			return nil
		}
		log.Printf("[dsql-bootstrap]   SQL FAILED → %s | %v", sql, err)
		return fmt.Errorf("creating index %s: %w", idx.Name, err)
	}
	log.Printf("[dsql-bootstrap]   index %q building async (job_id=%s) — monitor via sys.jobs", idx.Name, jobID)
	return nil
}

// ── Admin connection via IAM auth token ─────────────────────────────────────

func connectAdmin(ctx context.Context) (*pgx.Conn, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(dsqlRegion))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	token, err := dsqlauth.GenerateDBConnectAdminAuthToken(ctx, dsqlEndpoint, dsqlRegion, cfg.Credentials)
	if err != nil {
		return nil, fmt.Errorf("generating admin auth token: %w", err)
	}

	// Build the connection config WITHOUT the password — the DSQL auth token
	// is a presigned URL containing '&', '=', '?' which break libpq connection
	// string parsing. Set it on the Password field directly after parsing.
	connStr := fmt.Sprintf(
		"host=%s port=5432 user=admin dbname=postgres sslmode=verify-full",
		dsqlEndpoint,
	)

	connConfig, err := pgx.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parsing connection config: %w", err)
	}
	connConfig.Password = token

	log.Printf("[dsql-bootstrap] connecting as admin to %s (sslmode=verify-full)", dsqlEndpoint)
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		log.Printf("[dsql-bootstrap] admin connection FAILED: %v", err)
		return nil, fmt.Errorf("connecting to DSQL: %w", err)
	}
	log.Printf("[dsql-bootstrap] admin connection established")

	return conn, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

type roleConfig struct {
	RoleName string
	Schema   string
	Grants   []string
	// TableScoping, when non-empty, narrows grants to these specific tables
	// instead of the whole schema.
	TableScoping []string
}

func extractRoleConfig(image map[string]events.DynamoDBAttributeValue) roleConfig {
	r := roleConfig{
		RoleName: image["roleName"].String(),
		Schema:   image["schema"].String(),
	}

	if grantsAttr, ok := image["grants"]; ok {
		for _, item := range grantsAttr.List() {
			r.Grants = append(r.Grants, item.String())
		}
	}
	if scopeAttr, ok := image["tableScoping"]; ok {
		for _, item := range scopeAttr.List() {
			r.TableScoping = append(r.TableScoping, item.String())
		}
	}

	return r
}

// buildRoleGrantStatements returns the GRANT statements for a role.
//   - schema-wide (no TableScoping): GRANT ON ALL TABLES + ALTER DEFAULT
//     PRIVILEGES (covers current + future tables).
//   - table-scoped: GRANT on each named table only (no ALTER DEFAULT
//     PRIVILEGES — scoped roles do NOT auto-cover future tables).
// USAGE on the schema is always granted.
func buildRoleGrantStatements(role roleConfig) []string {
	grants := strings.Join(role.Grants, ", ")
	stmts := []string{
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", pgIdent(role.Schema), pgIdent(role.RoleName)),
	}

	if len(role.TableScoping) > 0 {
		for _, table := range role.TableScoping {
			stmts = append(stmts, fmt.Sprintf("GRANT %s ON %s.%s TO %s",
				grants, pgIdent(role.Schema), pgIdent(table), pgIdent(role.RoleName)))
		}
		return stmts
	}

	// Schema-wide: current + future tables.
	stmts = append(stmts,
		fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA %s TO %s",
			grants, pgIdent(role.Schema), pgIdent(role.RoleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT %s ON TABLES TO %s",
			pgIdent(role.Schema), grants, pgIdent(role.RoleName)),
	)
	return stmts
}

// execBestEffort runs cleanup statements, logging failures but never returning
// an error. Used by REMOVE paths so destructive cleanup can't poison the stream.
func execBestEffort(ctx context.Context, conn *pgx.Conn, statements []string) {
	for _, sql := range statements {
		log.Printf("[dsql-bootstrap]   SQL → %s", sql)
		tag, err := conn.Exec(ctx, sql)
		if err != nil {
			log.Printf("[dsql-bootstrap]   SQL FAILED (ignored, best-effort) → %s | %v", sql, err)
			continue
		}
		log.Printf("[dsql-bootstrap]   SQL OK → %s (%s)", sql, tag.String())
	}
}

func execAll(ctx context.Context, conn *pgx.Conn, statements []string) error {
	for _, sql := range statements {
		log.Printf("[dsql-bootstrap]   SQL → %s", sql)
		tag, err := conn.Exec(ctx, sql)
		if err != nil {
			log.Printf("[dsql-bootstrap]   SQL FAILED → %s | %v", sql, err)
			return fmt.Errorf("SQL failed [%s]: %w", sql, err)
		}
		log.Printf("[dsql-bootstrap]   SQL OK → %s (%s)", sql, tag.String())
	}
	return nil
}

// logRoleGrants queries DSQL's catalog to show the privileges actually held by
// the role on the schema's tables. Purely diagnostic — surfaces in CloudWatch
// so you can confirm grants landed without connecting manually.
func logRoleGrants(ctx context.Context, conn *pgx.Conn, schema, roleName string) {
	// Schema-level USAGE.
	var hasUsage bool
	if err := conn.QueryRow(ctx,
		"SELECT has_schema_privilege($1, $2, 'USAGE')", roleName, schema,
	).Scan(&hasUsage); err != nil {
		log.Printf("[dsql-bootstrap]   verify: could not check schema USAGE: %v", err)
	} else {
		log.Printf("[dsql-bootstrap]   verify: role %q USAGE on schema %q = %v", roleName, schema, hasUsage)
	}

	// Table-level privileges granted to the role in this schema.
	rows, err := conn.Query(ctx, `
		SELECT table_name, privilege_type
		FROM information_schema.role_table_grants
		WHERE grantee = $1 AND table_schema = $2
		ORDER BY table_name, privilege_type`, roleName, schema)
	if err != nil {
		log.Printf("[dsql-bootstrap]   verify: could not list table grants: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var table, priv string
		if err := rows.Scan(&table, &priv); err != nil {
			continue
		}
		log.Printf("[dsql-bootstrap]   verify: grant %s.%s → %s on %q", schema, table, priv, roleName)
		count++
	}
	if count == 0 {
		log.Printf("[dsql-bootstrap]   verify: no existing table grants for %q in %q (expected if schema has no tables yet)", roleName, schema)
	}
}

// logIAMMappings queries DSQL's sys.iam_pg_role_mappings to show which IAM ARNs
// are mapped to which database roles. Confirms grantConnect / AWS IAM GRANT worked.
func logIAMMappings(ctx context.Context, conn *pgx.Conn, roleName string) {
	rows, err := conn.Query(ctx,
		"SELECT arn, pg_role_name FROM sys.iam_pg_role_mappings WHERE pg_role_name = $1", roleName)
	if err != nil {
		log.Printf("[dsql-bootstrap]   verify: could not query sys.iam_pg_role_mappings: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var arn, pgRole string
		if err := rows.Scan(&arn, &pgRole); err != nil {
			continue
		}
		log.Printf("[dsql-bootstrap]   verify: IAM mapping %s → db role %q", arn, pgRole)
		count++
	}
	if count == 0 {
		log.Printf("[dsql-bootstrap]   verify: NO IAM mappings found for db role %q — Lambdas cannot connect as this role yet", roleName)
	}
}

// pgIdent quotes a PostgreSQL identifier to prevent injection.
func pgIdent(name string) string {
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}
