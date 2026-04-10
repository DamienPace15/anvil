// scripts/grants/configs/compute-configs.ts
//
// Single source of truth for SG-level grant method patching.
//
// Replaces egress-configs.ts and endpoint-configs.ts.
//
// Each ComputeResourceConfig describes one compute resource class and
// which SG grant methods to inject. Grant method definitions (EGRESS_GRANT,
// ENDPOINT_ACCESS_GRANT) are written once and reused across all resources.
//
// Adding a new compute resource (e.g. ECS):
//   1. Add one ComputeResourceConfig entry to COMPUTE_RESOURCES
//   2. Done — no script logic changes, no new config files
//
// Adding a new grant type (e.g. grantDatabaseAccess):
//   1. Add a new SgGrantMethod constant below
//   2. Add it to each compute resource's grants array
//   3. Add the corresponding runtime function to sdk/nodejs/grants.ts
//      and sdk/python/anvil_cloud/grants.py

export interface SgGrantMethod {
  /** TS method name injected onto the class, e.g. "grantEgress" */
  methodName: string;

  /** Python method name, e.g. "grant_egress" */
  pyMethodName: string;

  /** Idempotency check string — skip patch if this exists in file content */
  idempotencyCheck: string;

  /** The full TS method body to inject, receives className and sgProperty as template vars */
  tsMethodTemplate: (className: string, sgProperty: string) => string;

  /** The full Python method body to inject, receives className and sgProperty as template vars */
  pyMethodTemplate: (className: string, sgProperty: string) => string;
}

export interface ComputeResourceConfig {
  /** Path relative to sdk/nodejs/, e.g. "aws/lambda.ts" */
  tsFile: string;

  /** Path relative to sdk/python/anvil_cloud/, e.g. "aws/lambda_.py" */
  pyFile: string;

  /** The generated class name, e.g. "Lambda" */
  className: string;

  /** TS property name for the security group ID output */
  securityGroupIdProperty: string;

  /** Python property name for the security group ID output */
  pySecurityGroupIdProperty: string;

  /** SG grant methods to inject onto this class */
  grants: SgGrantMethod[];
}

// ── Shared grant method definitions ───────────────────────
// Write once, referenced by every compute resource that needs them.

export const EGRESS_GRANT: SgGrantMethod = {
  methodName: 'grantEgress',
  pyMethodName: 'grant_egress',
  idempotencyCheck: 'grantEgress(',

  tsMethodTemplate: (className, sgProperty) => `
    /**
     * Grants internet or CIDR-scoped egress from this ${className}'s security group.
     * Opt-in only — a VPC-attached ${className} with no grantEgress has
     * zero outbound internet access by default.
     *
     * Defaults to port 443 (HTTPS) if ports is omitted.
     *
     * @example
     * resource.grantEgress({ internet: true })                        // 443 only (default)
     * resource.grantEgress({ internet: true, ports: [80, 443] })      // HTTP + HTTPS
     * resource.grantEgress({ internet: true, allPorts: true })        // unrestricted — code review signal
     */
    public grantEgress(args: grants.EgressArgs): void {
        if (!this.${sgProperty}) {
            throw new Error(
                \`${className} "\${this.__name}" has no VPC — grantEgress requires vpc to be set.\`
            );
        }
        grants.createEgressGrant(this, this.__name, this.${sgProperty} as pulumi.Output<string>, args);
    }
  `,

  pyMethodTemplate: (className, sgProperty) =>
    [
      '',
      '    def grant_egress(',
      '        self,',
      '        internet: bool = False,',
      '        ports: Optional[list] = None,',
      '        all_ports: bool = False,',
      '        cidrs: Optional[list] = None,',
      '    ) -> None:',
      `        """`,
      `        Grants internet or CIDR-scoped egress from this ${className}'s security group.`,
      `        Opt-in only — a VPC-attached ${className} with no grant_egress`,
      `        has zero outbound internet access by default.`,
      ``,
      `        Defaults to port 443 (HTTPS) if ports is omitted.`,
      ``,
      `        Examples::`,
      ``,
      `            resource.grant_egress(internet=True)                       # 443 only`,
      `            resource.grant_egress(internet=True, ports=[80, 443])      # HTTP + HTTPS`,
      `            resource.grant_egress(internet=True, all_ports=True)       # unrestricted`,
      `        """`,
      `        from anvil_cloud import grants as _grants`,
      `        if not self.${sgProperty}:`,
      `            raise ValueError(`,
      `                f'${className} "{self.__name}" has no VPC — '`,
      `                'grant_egress requires vpc to be set.'`,
      `            )`,
      `        _grants.create_egress_grant(`,
      `            self, self.__name, self.${sgProperty},`,
      `            internet, ports, all_ports, cidrs`,
      `        )`,
      '',
    ].join('\n'),
};

export const ENDPOINT_ACCESS_GRANT: SgGrantMethod = {
  methodName: 'grantVpcEndpointAccess',
  pyMethodName: 'grant_vpc_endpoint_access',
  idempotencyCheck: 'grantVpcEndpointAccess(',

  tsMethodTemplate: (className, sgProperty) => `
    /**
     * Opens the network path between this ${className} and one or more
     * Interface VPC Endpoints.
     *
     * For each endpoint in the array:
     *   - Egress rule on the ${className} SG  — port 443 → endpoint SG
     *   - Ingress rule on the endpoint SG — port 443 ← ${className} SG
     *
     * Both rules are required — the endpoint SG has zero rules by default.
     * This method is network-only. IAM permissions are managed separately
     * via grantPermissions — both layers are required for full access.
     *
     * Requires this ${className} to be VPC-placed (vpc set in args).
     *
     * @example
     * resource.grantEndpointAccess([ssmEndpoint, ssmMessagesEndpoint, ec2MessagesEndpoint])
     * resource.grantEndpointAccess([secretsManagerEndpoint])
     */
    public grantEndpointAccess(endpoints: grants.VpcEndpointTarget[]): void {
        if (!this.${sgProperty}) {
            throw new Error(
                \`${className} "\${this.__name}" has no VPC — grantEndpointAccess requires vpc to be set.\`
            );
        }
        grants.createEndpointAccessGrant(this, this.__name, this.${sgProperty} as pulumi.Output<string>, endpoints);
    }
  `,

  pyMethodTemplate: (className, sgProperty) =>
    [
      '',
      '    def grant_endpoint_access(',
      '        self,',
      '        endpoints: list,',
      '    ) -> None:',
      `        """`,
      `        Opens the network path between this ${className} and one or`,
      `        more Interface VPC Endpoints.`,
      ``,
      `        For each endpoint in the list:`,
      `          - Egress rule on the ${className} SG  — port 443 out to endpoint SG`,
      `          - Ingress rule on the endpoint SG — port 443 in from ${className} SG`,
      ``,
      `        Both rules are required — the endpoint SG has zero rules by default.`,
      `        This method is network-only. IAM permissions are managed separately`,
      `        via grant_permissions — both layers are required for full access.`,
      ``,
      `        Examples::`,
      ``,
      `            resource.grant_endpoint_access([ssm_endpoint, ssm_messages_endpoint])`,
      `            resource.grant_endpoint_access([secrets_endpoint])`,
      `        """`,
      `        from anvil_cloud import grants as _grants`,
      `        if not self.${sgProperty}:`,
      `            raise ValueError(`,
      `                f'${className} "{self.__name}" has no VPC — '`,
      `                'grant_endpoint_access requires vpc to be set.'`,
      `            )`,
      `        _grants.create_endpoint_access_grant(`,
      `            self, self.__name, self.${sgProperty}, endpoints`,
      `        )`,
      '',
    ].join('\n'),
};

// ── Compute resource registry ──────────────────────────────
// Adding a new compute resource = one new entry here. Nothing else changes.

export const COMPUTE_RESOURCES: ComputeResourceConfig[] = [
  {
    tsFile: 'aws/lambda.ts',
    pyFile: 'aws/lambda_.py',
    className: 'Lambda',
    securityGroupIdProperty: 'securityGroupId',
    pySecurityGroupIdProperty: 'security_group_id',
    grants: [EGRESS_GRANT, ENDPOINT_ACCESS_GRANT],
  },
  // Future compute resources — add entry here only:
  // {
  //   tsFile: 'aws/ecs.ts',
  //   pyFile: 'aws/ecs.py',
  //   className: 'Ecs',
  //   securityGroupIdProperty: 'securityGroupId',
  //   pySecurityGroupIdProperty: 'security_group_id',
  //   grants: [EGRESS_GRANT, ENDPOINT_ACCESS_GRANT],
  // },
  // {
  //   tsFile: 'aws/ec2instance.ts',
  //   pyFile: 'aws/ec2instance.py',
  //   className: 'Ec2Instance',
  //   securityGroupIdProperty: 'securityGroupId',
  //   pySecurityGroupIdProperty: 'security_group_id',
  //   grants: [EGRESS_GRANT, ENDPOINT_ACCESS_GRANT],
  // },
];
