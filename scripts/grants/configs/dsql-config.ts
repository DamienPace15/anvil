import { GrantMapConfig, CustomGrantConfig } from '../types';

export const DSQL_GRANT_CONFIG: GrantMapConfig = {
  tsFile: 'aws/dsql.ts',
  pyFile: 'aws/dsql.py',
  className: 'DSQL',
  arnMapProperty: 'clusterArns',
  pyArnMapProperty: 'cluster_arns',
  grants: [],
};

export const DSQL_CUSTOM_GRANT_CONFIG: CustomGrantConfig = {
  tsFile: 'aws/dsql.ts',
  pyFile: 'aws/dsql.py',
  detectMethod: 'grantConnect',
  tsImports: [
    'import { DSQLConnect } from "./dsqlconnect";',
  ],
  tsPropertyDeclarations: [
    '    declare public /*out*/ readonly rolesTableName: pulumi.Output<string | undefined>;',
  ],
  tsResourceInputs: [
    '            resourceInputs["rolesTableName"] = undefined /*out*/;',
  ],
  tsMethod: `
    /**
     * Grants connect access (dsql:DbConnect) on this DSQL cluster
     * to the target compute resource's execution role.
     *
     * Creates an anvil:aws:DSQLConnect component that handles:
     * 1. IAM policy (dsql:DbConnect) on all cluster ARNs
     * 2. DynamoDB IAM mapping item (when roles are configured) that triggers
     *    the bootstrap Lambda to run: AWS IAM GRANT "dbRole" TO 'arn:...'
     *
     * @param target - The compute resource to grant access to (Lambda, ECS, etc.).
     * @param dbRole - The database role name to map this target to (e.g. "app_role").
     */
    public grantConnect(target: grants.GrantTarget, dbRole?: string): void {
        const args: any = {
            clusterArns: this.clusterArns,
            targetRoleArn: target.grantRoleArn(),
            targetName: target.grantName(),
            dbRole: dbRole,
        };
        if (this.rolesTableName) {
            args.rolesTableName = this.rolesTableName;
        }
        new DSQLConnect(\`\${this.__name}-\${target.grantName()}-connect\`, args, { parent: this });
    }`,
  pyMethod: `
    def grant_connect(self, target: "grants.GrantTarget", db_role: str = None) -> None:
        """Grants connect access on this DSQL cluster to the target's execution role.

        Creates an anvil:aws:DSQLConnect component that handles the IAM policy
        and optional DynamoDB IAM mapping for role bootstrap.

        Args:
            target: The compute resource to grant access to (Lambda, ECS, etc.).
            db_role: The database role name to map this target to (e.g. "app_role").
        """
        DSQLConnect(f"{self._name}-{target.grant_name()}-connect",
            cluster_arns=self.cluster_arns,
            target_role_arn=target.grant_role_arn(),
            target_name=target.grant_name(),
            db_role=db_role,
            roles_table_name=self.roles_table_name,
            opts=pulumi.ResourceOptions(parent=self),
        )
`,
};
