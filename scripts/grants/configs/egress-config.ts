// scripts/grants/configs/egress-configs.ts
//
// Config-driven egress grant patches applied after pulumi gen-sdk.
//
// grantEgress is different from IAM grants — it creates SecurityGroupEgressRule
// resources rather than IAM policies. It lives in grants.ts as createEgressGrant
// and is patched onto compute resource classes here.
//
// Adding a new compute resource:
//   1. Ensure the resource has a securityGroupId output in its schema
//   2. Add an entry here — no script logic changes needed

export interface EgressGrantConfig {
  /** Path relative to sdk/nodejs/, e.g. "aws/lambda.ts" */
  tsFile: string;
  /** Path relative to sdk/python/anvil_cloud/, e.g. "aws/lambda_.py" */
  pyFile: string;
  /** The generated class name, e.g. "Lambda" */
  className: string;
  /** TS property name for the security group ID */
  securityGroupIdProperty: string;
  /** Python property name for the security group ID */
  pySecurityGroupIdProperty: string;
}

export const EGRESS_GRANT_CONFIGS: EgressGrantConfig[] = [
  {
    tsFile: 'aws/lambda.ts',
    pyFile: 'aws/lambda_.py',
    className: 'Lambda',
    securityGroupIdProperty: 'securityGroupId',
    pySecurityGroupIdProperty: 'security_group_id',
  },
  // Future:
  // { tsFile: 'aws/ecs.ts', pyFile: 'aws/ecs.py', className: 'Ecs', ... },
  // { tsFile: 'aws/ec2.ts', pyFile: 'aws/ec2.py', className: 'Ec2', ... },
];
