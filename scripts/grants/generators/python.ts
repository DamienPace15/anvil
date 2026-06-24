import { GrantConfig, Grant, GrantMapConfig } from '../types';
import { grantSuffix, toSnakeCase } from './helper';

export function generatePyGrantMethod(
  config: GrantConfig,
  grant: Grant
): string {
  const { arnProperty, supportsPaths, supportsIndexes } = config;
  const { method, actions, isFullAccess } = grant;

  const pyMethod = toSnakeCase(method);
  // arnProperty is the TypeScript (camelCase) output name; Python exposes the
  // snake_case form (e.g. tableArn -> table_arn).
  const pyArn = toSnakeCase(arnProperty);
  const suffix = grantSuffix(method);
  const actionsStr = actions.map((a) => `"${a}"`).join(', ');

  if (isFullAccess) {
    return `
    def ${pyMethod}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants full access on this resource. Prefer scoped grants."""
        if not opts or not opts.justification:
            pulumi.log.warn(
                f"⚠ {self._name} → {target.grant_name()}: full access granted with no justification.",
                self,
            )
        else:
            pulumi.log.info(
                f"ℹ {self._name} → {target.grant_name()}: full access granted. Justification: \\"{opts.justification}\\"",
                self,
            )
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${pyArn}, None)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
  }

  if (supportsIndexes) {
    return `
    def ${pyMethod}(self, target: "grants.GrantTarget", indexes: Optional[list] = None, justification: Optional[str] = None) -> None:
        """Grants ${suffix} access on this resource.

        Args:
            target: The compute resource to grant access to.
            indexes: Optional list of GSI names to scope access (e.g. ["by_status"]).
                     If omitted, grants table access only — no index access.
            justification: Optional audit trail note.
        """
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        index_paths = [f"index/{i}" for i in indexes] if indexes else None
        arns = grants.build_resource_arns(self.${pyArn}, index_paths)
        grants.create_grant(self, name, target, [${actionsStr}], arns, grants.GrantOptions(justification=justification) if justification else None)
`;
  }

  if (supportsPaths) {
    return `
    def ${pyMethod}(self, target: "grants.GrantTarget", paths: Optional[list] = None, opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${pyArn}, paths)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
  }

  return `
    def ${pyMethod}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${pyArn}, None)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
}

export function generatePyGrantTargetMethods(
  pyRoleArnProperty: string
): string {
  return `
    def grant_name(self) -> str:
        """Implements GrantTarget — returns the logical resource name."""
        return self._name

    def grant_role_arn(self):
        """Implements GrantTarget — returns the IAM execution role ARN."""
        return self.${pyRoleArnProperty}
`;
}

export function generatePyGrantMapMethod(
  config: GrantMapConfig,
  grant: Grant
): string {
  const { className, pyArnMapProperty } = config;
  const { method, actions } = grant;
  const pyMethod = toSnakeCase(method);
  const suffix = grantSuffix(method);
  const actionsStr = actions.map((a) => `"${a}"`).join(', ');

  return `
    def ${pyMethod}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this ${className.toLowerCase()} to the target's execution role.
        Scoped to all ARNs in ${pyArnMapProperty} — one per region.
        """
        import json
        import pulumi_aws as aws
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        policy_document = self.${pyArnMapProperty}.apply(
            lambda arns: json.dumps({
                "Version": "2012-10-17",
                "Statement": [{"Effect": "Allow", "Action": [${actionsStr}], "Resource": list(arns.values())}],
            })
        )
        role_name = target.grant_role_arn().apply(
            lambda arn: arn[arn.rfind("/") + 1:] if "/" in arn else arn
        )
        aws.iam.RolePolicy(name, role=role_name, policy=policy_document, opts=pulumi.ResourceOptions(parent=self))
`;
}
