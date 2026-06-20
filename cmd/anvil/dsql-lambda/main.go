package main

import (
	"context"
	"fmt"
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
// DynamoDB table schema:
//
//   PK: roleName (S)   — Postgres role name, e.g. "app_role"
//   SK: sk (S)         — currently always "DEFINITION"
//
//   Role definition item (sk = "DEFINITION"):
//     roleName  (S)  — Postgres role name
//     sk        (S)  — literal "DEFINITION"
//     schema    (S)  — Postgres schema name, e.g. "myapp"
//     grants    (S)  — Comma-separated privileges, e.g. "SELECT,INSERT,UPDATE,DELETE"
//
// Stream event types → SQL actions:
//
//   INSERT → CREATE SCHEMA IF NOT EXISTS, CREATE ROLE, GRANT USAGE,
//            GRANT privileges, ALTER DEFAULT PRIVILEGES
//
//   MODIFY → REVOKE old grants, GRANT new grants, ALTER DEFAULT PRIVILEGES
//            (compares old vs new image)
//
//   REMOVE → REVOKE ALL, DROP OWNED BY, DROP ROLE
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
	for _, record := range event.Records {
		if err := processRecord(ctx, record); err != nil {
			return fmt.Errorf("failed to process record %s: %w", record.EventID, err)
		}
	}
	return nil
}

func processRecord(ctx context.Context, record events.DynamoDBEventRecord) error {
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

	statements := []string{
		fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pgIdent(role.Schema)),
		fmt.Sprintf("CREATE ROLE %s WITH LOGIN", pgIdent(role.RoleName)),
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", pgIdent(role.Schema), pgIdent(role.RoleName)),
		fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA %s TO %s",
			strings.Join(role.Grants, ", "), pgIdent(role.Schema), pgIdent(role.RoleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT %s ON TABLES TO %s",
			pgIdent(role.Schema), strings.Join(role.Grants, ", "), pgIdent(role.RoleName)),
	}

	return execAll(ctx, conn, statements)
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

	statements := []string{
		fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s",
			pgIdent(oldRole.Schema), pgIdent(oldRole.RoleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s REVOKE ALL ON TABLES FROM %s",
			pgIdent(oldRole.Schema), pgIdent(oldRole.RoleName)),
	}

	if oldRole.Schema != newRole.Schema {
		statements = append(statements,
			fmt.Sprintf("REVOKE USAGE ON SCHEMA %s FROM %s", pgIdent(oldRole.Schema), pgIdent(oldRole.RoleName)),
			fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pgIdent(newRole.Schema)),
			fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", pgIdent(newRole.Schema), pgIdent(newRole.RoleName)),
		)
	}

	statements = append(statements,
		fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA %s TO %s",
			strings.Join(newRole.Grants, ", "), pgIdent(newRole.Schema), pgIdent(newRole.RoleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT %s ON TABLES TO %s",
			pgIdent(newRole.Schema), strings.Join(newRole.Grants, ", "), pgIdent(newRole.RoleName)),
	)

	return execAll(ctx, conn, statements)
}

// ── REMOVE: revoke everything and drop role ─────────────────────────────────

func handleRemove(ctx context.Context, oldImage map[string]events.DynamoDBAttributeValue) error {
	role := extractRoleConfig(oldImage)

	conn, err := connectAdmin(ctx)
	if err != nil {
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
		fmt.Sprintf("DROP OWNED BY %s", pgIdent(role.RoleName)),
		fmt.Sprintf("DROP ROLE IF EXISTS %s", pgIdent(role.RoleName)),
	}

	return execAll(ctx, conn, statements)
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

	connStr := fmt.Sprintf(
		"host=%s port=5432 user=admin password=%s dbname=postgres sslmode=require",
		dsqlEndpoint, token,
	)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("connecting to DSQL: %w", err)
	}

	return conn, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

type roleConfig struct {
	RoleName string
	Schema   string
	Grants   []string
}

func extractRoleConfig(image map[string]events.DynamoDBAttributeValue) roleConfig {
	r := roleConfig{
		RoleName: image["roleName"].String(),
		Schema:   image["schema"].String(),
	}

	grantsStr := image["grants"].String()
	if grantsStr != "" {
		r.Grants = strings.Split(grantsStr, ",")
		for i := range r.Grants {
			r.Grants[i] = strings.TrimSpace(r.Grants[i])
		}
	}

	return r
}

func execAll(ctx context.Context, conn *pgx.Conn, statements []string) error {
	for _, sql := range statements {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("SQL failed [%s]: %w", sql, err)
		}
	}
	return nil
}

// pgIdent quotes a PostgreSQL identifier to prevent injection.
func pgIdent(name string) string {
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}
