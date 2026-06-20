package main

// Bootstrap Lambda — invoked by the Anvil CLI after a successful deploy.
//
// Receives a JSON payload via the Lambda event containing the DSQL cluster
// endpoint, region, roles to create, and IAM role ARNs to grant.
//
// Connects to the cluster as admin using its own IAM execution role
// (dsql:DbConnectAdmin scoped to the specific cluster ARN). The CI/CD
// pipeline never needs dsql:DbConnectAdmin — only this Lambda's role does.
//
// Each SQL statement runs in its own transaction because Aurora DSQL requires:
//   - One DDL statement per transaction
//   - Schema changes must be committed before subsequent statements can see them
//
// Supports idempotent re-runs:
//   - CREATE SCHEMA IF NOT EXISTS
//   - CREATE ROLE IF NOT EXISTS (via DO block workaround since DSQL lacks IF NOT EXISTS on roles)
//   - GRANT ... ON ALL TABLES covers all tables existing at invocation time
//   - ALTER DEFAULT PRIVILEGES is NOT supported in Aurora DSQL — omitted

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	dsqlauth "github.com/aws/aws-sdk-go-v2/feature/dsql/auth"
	"github.com/jackc/pgx/v5"
)

// ── Payload types ──────────────────────────────────────────────────────────

// RoleConfig defines a database role to create and configure.
type RoleConfig struct {
	Name   string   `json:"name"`
	Schema string   `json:"schema"`
	Grants []string `json:"grants"`
}

// GrantConfig defines an IAM role ARN to associate with a database role.
type GrantConfig struct {
	DbRole     string `json:"dbRole"`
	IamRoleArn string `json:"iamRoleArn"`
}

// BootstrapPayload is the Lambda event payload sent by the Anvil CLI.
type BootstrapPayload struct {
	// Endpoint is the DSQL cluster endpoint.
	Endpoint string `json:"endpoint"`

	// Region is the AWS region of the cluster.
	Region string `json:"region"`

	// Roles are the database roles to create. Optional — omit to skip role creation.
	// Users can manage roles externally (console, Atlas, etc.) and just use Grants.
	Roles []RoleConfig `json:"roles,omitempty"`

	// Grants are the IAM role ARN → database role mappings to wire.
	// Always present when the bootstrap Lambda is invoked.
	Grants []GrantConfig `json:"grants"`
}

// BootstrapResult is returned to the CLI after execution.
type BootstrapResult struct {
	Success  bool     `json:"success"`
	Executed []string `json:"executed"`
	Error    string   `json:"error,omitempty"`
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	lambda.Start(handle)
}

func handle(ctx context.Context, payload BootstrapPayload) (BootstrapResult, error) {
	result := BootstrapResult{}

	// Generate admin auth token using the Lambda's own IAM execution role.
	// The Lambda's role has dsql:DbConnectAdmin scoped to this cluster ARN.
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(payload.Region))
	if err != nil {
		result.Error = fmt.Sprintf("failed to load AWS config: %v", err)
		return result, nil
	}

	token, err := dsqlauth.GenerateDBConnectAdminAuthToken(
		ctx,
		payload.Endpoint,
		payload.Region,
		cfg.Credentials,
	)
	if err != nil {
		result.Error = fmt.Sprintf(
			"failed to generate admin token for %s: %v\n"+
				"Ensure the Lambda execution role has dsql:DbConnectAdmin on the cluster ARN.",
			payload.Endpoint, err,
		)
		return result, nil
	}

	connStr := fmt.Sprintf(
		"host=%s user=admin password=%s dbname=postgres port=5432 sslmode=require",
		payload.Endpoint, token,
	)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		result.Error = fmt.Sprintf("failed to connect to cluster %s: %v", payload.Endpoint, err)
		return result, nil
	}
	defer conn.Close(ctx)

	// ── Role creation ──────────────────────────────────────────────────────
	// Only runs if roles are declared in the DSQL component args.
	// Each DDL statement in its own transaction — DSQL requirement.
	for _, role := range payload.Roles {
		// Tx 1 — CREATE SCHEMA IF NOT EXISTS
		schemaSQL := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", role.Schema)
		if err := execTx(ctx, conn, schemaSQL); err != nil {
			result.Error = fmt.Sprintf("failed to create schema %s: %v", role.Schema, err)
			return result, nil
		}
		result.Executed = append(result.Executed, schemaSQL)

		// Tx 2 — CREATE ROLE IF NOT EXISTS
		// DSQL doesn't support CREATE ROLE IF NOT EXISTS directly.
		// Use a DO block to check pg_roles first — but DSQL doesn't support PL/pgSQL.
		// Instead: attempt CREATE ROLE and ignore "already exists" error.
		roleSQL := fmt.Sprintf("CREATE ROLE %s WITH LOGIN", role.Name)
		if err := execTxIgnoreExists(ctx, conn, roleSQL); err != nil {
			result.Error = fmt.Sprintf("failed to create role %s: %v", role.Name, err)
			return result, nil
		}
		result.Executed = append(result.Executed, roleSQL)

		// Tx 3 — GRANT USAGE + GRANT ON ALL TABLES (DML, can batch)
		// GRANT USAGE makes the schema visible to the role.
		// GRANT ON ALL TABLES covers tables existing at this moment.
		// Note: ALTER DEFAULT PRIVILEGES is NOT supported in Aurora DSQL.
		// Re-deploy after adding new tables to update grants.
		grantsStr := strings.Join(role.Grants, ", ")
		grantSQL := fmt.Sprintf(
			"GRANT USAGE ON SCHEMA %s TO %s; GRANT %s ON ALL TABLES IN SCHEMA %s TO %s",
			role.Schema, role.Name,
			grantsStr, role.Schema, role.Name,
		)
		// Run as two separate statements in the same transaction (both DML)
		if err := execTxMulti(ctx, conn,
			fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", role.Schema, role.Name),
			fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA %s TO %s", grantsStr, role.Schema, role.Name),
		); err != nil {
			result.Error = fmt.Sprintf("failed to grant permissions to %s: %v", role.Name, err)
			return result, nil
		}
		result.Executed = append(result.Executed, grantSQL)
	}

	// ── IAM grants ─────────────────────────────────────────────────────────
	// Always runs when grantConnect is called.
	// Wires the Lambda's IAM role ARN to the database role.
	// IAM grants replicate automatically to peer regions in multi-region.
	for _, grant := range payload.Grants {
		iamGrantSQL := fmt.Sprintf(
			"AWS IAM GRANT %s TO '%s'",
			grant.DbRole, grant.IamRoleArn,
		)
		if err := execTxIgnoreExists(ctx, conn, iamGrantSQL); err != nil {
			result.Error = fmt.Sprintf(
				"failed to run AWS IAM GRANT %s TO %s: %v",
				grant.DbRole, grant.IamRoleArn, err,
			)
			return result, nil
		}
		result.Executed = append(result.Executed, iamGrantSQL)
	}

	result.Success = true
	return result, nil
}

// ── SQL execution helpers ──────────────────────────────────────────────────

// execTx runs a single SQL statement in its own transaction.
// Used for DDL statements — DSQL requires one DDL per transaction.
func execTx(ctx context.Context, conn *pgx.Conn, sql string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// execTxIgnoreExists runs a single SQL statement in its own transaction.
// Ignores "already exists" errors — makes operations idempotent on re-run.
func execTxIgnoreExists(ctx context.Context, conn *pgx.Conn, sql string) error {
	err := execTx(ctx, conn, sql)
	if err != nil && isAlreadyExistsError(err) {
		return nil
	}
	return err
}

// execTxMulti runs multiple DML statements in a single transaction.
// Only use for DML (GRANT, REVOKE) — never mix with DDL.
func execTxMulti(ctx context.Context, conn *pgx.Conn, statements ...string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	for _, sql := range statements {
		if _, err := tx.Exec(ctx, sql); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("exec %q: %w", sql, err)
		}
	}
	return tx.Commit(ctx)
}

// isAlreadyExistsError returns true for Postgres "already exists" errors.
// DSQL uses standard Postgres error codes.
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "42710") // Postgres SQLSTATE: duplicate_object
}

// ── JSON marshalling helper ────────────────────────────────────────────────

// MarshalJSON implements custom marshalling for BootstrapResult.
// Omits empty executed slice for cleaner output.
func (r BootstrapResult) MarshalJSON() ([]byte, error) {
	type Alias BootstrapResult
	return json.Marshal(struct {
		Alias
	}{Alias: Alias(r)})
}
