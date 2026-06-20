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
// DynamoDB table schema (composite key):
//
//   PK: roleName (S)   — Postgres role name, e.g. "app_role"
//   SK: sk (S)         — discriminator: "DEFINITION" or an IAM role ARN
//
// Two item types:
//
//   1. Role definition (sk = "DEFINITION"):
//      Written by Pulumi from the roles[] array on the DSQL component.
//      Attributes: schema (S), grants (S, comma-separated)
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
	for _, record := range event.Records {
		if err := processRecord(ctx, record); err != nil {
			return fmt.Errorf("failed to process record %s: %w", record.EventID, err)
		}
	}
	return nil
}

func processRecord(ctx context.Context, record events.DynamoDBEventRecord) error {
	// Dispatch based on sk value: "DEFINITION" → role CRUD, IAM ARN → IAM GRANT/REVOKE
	var image map[string]events.DynamoDBAttributeValue
	if record.Change.NewImage != nil {
		image = record.Change.NewImage
	} else {
		image = record.Change.OldImage
	}
	if isIAMMapping(image) {
		return processIAMMapping(ctx, record)
	}

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

		conn, err := connectAdmin(ctx)
		if err != nil {
			return fmt.Errorf("admin connect failed: %w", err)
		}
		defer conn.Close(ctx)

		sql := fmt.Sprintf("AWS IAM GRANT %s TO '%s'", pgIdent(roleName), iamArn)
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("SQL failed [%s]: %w", sql, err)
		}
		return nil

	case "REMOVE":
		roleName := record.Change.OldImage["roleName"].String()
		iamArn := record.Change.OldImage["sk"].String()

		conn, err := connectAdmin(ctx)
		if err != nil {
			return fmt.Errorf("admin connect failed: %w", err)
		}
		defer conn.Close(ctx)

		sql := fmt.Sprintf("AWS IAM REVOKE %s FROM '%s'", pgIdent(roleName), iamArn)
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("SQL failed [%s]: %w", sql, err)
		}
		return nil

	default:
		return nil
	}
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
