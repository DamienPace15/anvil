import { GrantConfig, Grant } from '../types';
import { grantSuffix } from './helper';

export function generateTSGrantMethod(
  config: GrantConfig,
  grant: Grant
): string {
  const { className, arnProperty, supportsPaths, supportsIndexes } = config;
  const { method, actions, isFullAccess } = grant;
  const actionsStr = actions.map((a) => `"${a}"`).join(', ');
  const suffix = grantSuffix(method);

  if (isFullAccess) {
    return `
    /**
     * Grants full access (${actions.join(
       ', '
     )}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * This is an escape hatch — prefer scoped grants (grantRead, grantWrite, etc.).
     * A warning is logged if no justification is provided.
     */
    public ${method}(target: grants.GrantTarget, opts?: grants.GrantOptions): void {
        if (!opts?.justification) {
            pulumi.log.warn(
                \`⚠ \${this.__name} → \${target.grantName()}: full access granted with no justification. \` +
                \`Consider scoping with grantRead, grantWrite, or grantDelete, \` +
                \`or add a justification.\`,
                this,
            );
        } else {
            pulumi.log.info(
                \`ℹ \${this.__name} → \${target.grantName()}: full access granted. Justification: "\${opts.justification}"\`,
                this,
            );
        }
        const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, undefined);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
  }

  if (supportsIndexes) {
    return `
      /**
       * Grants ${suffix} access (${actions.join(
      ', '
    )}) on this ${className.toLowerCase()}
       * to the target compute resource's execution role.
       *
       * @param target - The compute resource to grant access to.
       * @param opts - Optional. indexes: scope to specific GSI names only.
       *               If omitted, grants table access only — no index access.
       * @param opts.justification - Optional audit trail note.
       */
      public ${method}(target: grants.GrantTarget, opts?: { indexes?: string[]; justification?: string }): void {
          const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
          const indexPaths = opts?.indexes?.map(i => \`index/\${i}\`) ?? null;
          const arns = grants.buildResourceArns(this.${arnProperty}, indexPaths);
          grants.createGrant(this, name, target, [${actionsStr}], arns, { justification: opts?.justification });
      }`;
  }

  if (supportsPaths) {
    return `
    /**
     * Grants ${suffix} access (${actions.join(
      ', '
    )}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * @param target - The compute resource to grant access to.
     * @param paths - Optional array of path prefixes to scope access (e.g. ["uploads/*"]).
     * @param opts - Optional grant options (justification for audit trail).
     */
    public ${method}(target: grants.GrantTarget, paths?: string[], opts?: grants.GrantOptions): void {
        const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, paths);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
  }

  return `
    /**
     * Grants ${suffix} access (${actions.join(
    ', '
  )}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * @param target - The compute resource to grant access to.
     * @param opts - Optional grant options (justification for audit trail).
     */
    public ${method}(target: grants.GrantTarget, opts?: grants.GrantOptions): void {
        const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, undefined);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
}

export function generateTSGrantTargetMethods(roleArnProperty: string): string {
  return `
    /** Implements GrantTarget — returns the logical resource name. */
    public grantName(): string {
        return this.__name;
    }

    /** Implements GrantTarget — returns the IAM execution role ARN. */
    public grantRoleArn(): pulumi.Output<string> {
        return this.${roleArnProperty};
    }`;
}
