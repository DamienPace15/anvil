// scripts/grants/configs/endpoint-configs.ts
//
// Config-driven endpoint access grant patches applied after pulumi gen-sdk.
//
// grantEndpointAccess opens the network path between a compute resource and
// one or more Interface VPC Endpoints. It creates:
//   - A SecurityGroupEgressRule on the compute resource SG (port 443 out)
//   - A SecurityGroupIngressRule on the endpoint SG (port 443 in)
//
// Both rules are required — the endpoint SG has zero rules by default.
//
// Lives in grants.ts as createEndpointAccessGrant and is patched onto
// compute resource classes here.
//
// Adding a new compute resource:
//   1. Ensure the resource has a securityGroupId output in its schema
//   2. Add an entry here — no script logic changes needed

export interface EndpointGrantConfig {
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

export const ENDPOINT_GRANT_CONFIGS: EndpointGrantConfig[] = [
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
