import { GrantConfig, Grant } from '../types';
import { grantSuffix, toSnakeCase } from './helper';

export function generatePyGrantMethod(
  config: GrantConfig,
  grant: Grant
): string {
  const { arnProperty, supportsPaths, supportsIndexes } = config;
  const { method, actions, isFullAccess } = grant;

  const pyMethod = toSnakeCase(method);
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
        arns = grants.build_resource_arns(self.${arnProperty}, None)
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
          arns = grants.build_resource_arns(self.${arnProperty}, index_paths)
          grants.create_grant(self, name, target, [${actionsStr}], arns, grants.GrantOptions(justification=justification) if justification else None)
  `;
  }

  if (supportsPaths) {
    return `
    def ${pyMethod}(self, target: "grants.GrantTarget", paths: Optional[list] = None, opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${arnProperty}, paths)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
  }

  return `
    def ${pyMethod}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${arnProperty}, None)
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
