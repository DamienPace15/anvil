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
  /**
   * The logical resource name passed to the constructor.
   */
  grantName(): string;

  /**
   * The ARN of the IAM execution role attached to this compute resource.
   */
  grantRoleArn(): pulumi.Output<string>;
}

/**
 * Optional metadata for grant methods.
 */
export interface GrantOptions {
  /**
   * Documents why this grant is needed.
   * Stored as a tag on the generated IAM policy resource for audit purposes.
   */
  justification?: string;
}

/**
 * Creates a scoped IAM RolePolicy granting the specified actions on the
 * specified resource ARNs to the target's execution role.
 *
 * This is the core engine that all resource-specific grant methods delegate to.
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

  // Extract role name from ARN (everything after the last "/")
  const roleName = target.grantRoleArn().apply((arn) => {
    const idx = arn.lastIndexOf('/');
    return idx >= 0 ? arn.substring(idx + 1) : arn;
  });

  // Justification is stored in the resource name suffix for audit trail.
  // Future: compliance audit trail (Pro tier) will capture this metadata separately.
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

/** @internal Sanitize a string for use in resource names. */
function sanitize(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .slice(0, 40);
}

/**
 * Builds the list of ARNs for a grant based on a base ARN and optional path scoping.
 *
 * - No paths: grants access to the entire resource (baseArn + baseArn/*)
 * - With paths: grants access to baseArn (for list operations) + each scoped path
 *
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
