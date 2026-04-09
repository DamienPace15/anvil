// sdk/nodejs/grants.ts
// Hand-written. Backed up/restored during gen-sdk like app.ts and block.ts.
//
// Provides the runtime grant execution for all resource grant methods.
// Each resource's grant methods (injected by fix-sdk-grants.js) delegate here.

import * as pulumi from '@pulumi/pulumi';
import * as aws from '@pulumi/aws';

/**
 * GrantTarget is any Anvil compute resource that can receive IAM permissions.
 * Compute resources (Lambda, SvelteKitSite, etc.) satisfy this interface.
 */
export interface GrantTarget {
  grantName(): string;
  grantRoleArn(): pulumi.Output<string>;
}

/**
 * Optional metadata for grant methods.
 */
export interface GrantOptions {
  justification?: string;
}

/**
 * Creates a scoped IAM RolePolicy granting the specified actions on the
 * specified resource ARNs to the target's execution role.
 *
 * @internal
 */
export function createGrant(
  parent: pulumi.Resource,
  name: string,
  target: GrantTarget,
  actions: string[],
  resourceArns: pulumi.Output<string>[],
  opts?: GrantOptions
): void {
  const policyDocument = pulumi.all(resourceArns).apply((arns) =>
    JSON.stringify({
      Version: '2012-10-17',
      Statement: [
        {
          Effect: 'Allow',
          Action: actions,
          Resource: arns,
        },
      ],
    })
  );

  const roleName = target.grantRoleArn().apply((arn) => {
    const idx = arn.lastIndexOf('/');
    return idx >= 0 ? arn.substring(idx + 1) : arn;
  });

  const policyName = opts?.justification
    ? `${name}-${sanitize(opts.justification)}`
    : name;

  new aws.iam.RolePolicy(
    policyName,
    {
      role: roleName,
      policy: policyDocument,
    },
    { parent }
  );
}

/** @internal */
function sanitize(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .slice(0, 40);
}

/**
 * Builds the list of ARNs for a grant based on a base ARN and optional path scoping.
 * @internal
 */
export function buildResourceArns(
  baseArn: pulumi.Output<string>,
  paths?: string[]
): pulumi.Output<string>[] {
  const arns: pulumi.Output<string>[] = [baseArn];

  if (!paths || paths.length === 0) {
    arns.push(pulumi.interpolate`${baseArn}/*`);
  } else {
    for (const p of paths) {
      arns.push(pulumi.interpolate`${baseArn}/${p}`);
    }
  }

  return arns;
}

/**
 * A single CIDR-scoped egress rule.
 * One SecurityGroupEgressRule is created per port.
 */
export interface CIDREgressRule {
  /** IPv4 CIDR block, e.g. "10.0.0.0/8" */
  range: string;
  /** TCP ports to allow to this CIDR. Required — be explicit. */
  ports: number[];
}

/**
 * Arguments for grantEgress.
 * Must specify either internet or cidrs — not both, not neither.
 */
export interface EgressArgs {
  /**
   * Allow outbound to 0.0.0.0/0.
   * Defaults to port 443 if ports is omitted.
   */
  internet?: boolean;

  /**
   * TCP ports to allow when internet is true.
   * Defaults to [443] if omitted.
   */
  ports?: number[];

  /**
   * Allow all outbound TCP ports (0-65535) when internet is true.
   * Deliberate unrestricted egress — visible in code review.
   */
  allPorts?: boolean;

  /**
   * Structured CIDR-scoped egress rules.
   * One rule per { range, port } pair.
   * Ports required per entry.
   */
  cidrs?: CIDREgressRule[];
}

/**
 * Creates internet or CIDR-scoped egress SecurityGroupEgressRule(s) on a
 * compute resource's dedicated security group.
 *
 * @internal Called by the patched grantEgress method on compute resources.
 */
export function createEgressGrant(
  parent: pulumi.Resource,
  name: string,
  securityGroupId: pulumi.Output<string>,
  args: EgressArgs
): void {
  // Validate: internet or cidrs, not both, not neither.
  if (args.internet && args.cidrs && args.cidrs.length > 0) {
    throw new Error(
      `${name}: grantEgress cannot specify both internet and cidrs — choose one.`
    );
  }
  if (!args.internet && (!args.cidrs || args.cidrs.length === 0)) {
    throw new Error(
      `${name}: grantEgress requires either internet: true or cidrs.`
    );
  }

  // Validate: allPorts only with internet.
  if (args.allPorts && !args.internet) {
    throw new Error(
      `${name}: grantEgress allPorts is only valid with internet: true.`
    );
  }

  // ── Internet egress ────────────────────────────────────────────────────────
  if (args.internet) {
    if (args.allPorts) {
      new aws.vpc.SecurityGroupEgressRule(
        `${name}-egress-all`,
        {
          securityGroupId,
          cidrIpv4: '0.0.0.0/0',
          fromPort: 0,
          toPort: 65535,
          ipProtocol: 'tcp',
          tags: { ManagedBy: 'anvil' },
        },
        { parent }
      );
      return;
    }

    const ports = args.ports && args.ports.length > 0 ? args.ports : [443];
    for (const port of ports) {
      new aws.vpc.SecurityGroupEgressRule(
        `${name}-egress-${port}`,
        {
          securityGroupId,
          cidrIpv4: '0.0.0.0/0',
          fromPort: port,
          toPort: port,
          ipProtocol: 'tcp',
          tags: { ManagedBy: 'anvil' },
        },
        { parent }
      );
    }
    return;
  }

  // ── CIDR egress ────────────────────────────────────────────────────────────
  for (const cidrRule of args.cidrs!) {
    if (!cidrRule.ports || cidrRule.ports.length === 0) {
      throw new Error(
        `${name}: grantEgress cidr "${cidrRule.range}" requires ports — be explicit about which ports this range needs.`
      );
    }
    for (const port of cidrRule.ports) {
      const sanitized = cidrRule.range.replace(/[^0-9a-z]/gi, '-');
      new aws.vpc.SecurityGroupEgressRule(
        `${name}-egress-${sanitized}-${port}`,
        {
          securityGroupId,
          cidrIpv4: cidrRule.range,
          fromPort: port,
          toPort: port,
          ipProtocol: 'tcp',
          tags: { ManagedBy: 'anvil' },
        },
        { parent }
      );
    }
  }
}
