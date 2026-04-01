export interface Grant {
  /** TS method name, e.g. "grantRead" — Python derived automatically via camelCase → snake_case */
  method: string;
  /** IAM actions, e.g. ["s3:GetObject", "s3:ListBucket"] */
  actions: string[];
  /** Marks as escape hatch — logs warning if no justification provided */
  isFullAccess?: boolean;
}

export interface GrantConfig {
  /** Path relative to sdk/nodejs/, e.g. "aws/bucket.ts" */
  tsFile: string;
  /** Path relative to sdk/python/anvil_cloud/, e.g. "aws/bucket.py" */
  pyFile: string;
  /** Generated class name to patch, e.g. "Bucket" */
  className: string;
  /** Property on class that holds the resource ARN, e.g. "arn" */
  arnProperty: string;
  /** Whether grant methods accept path scoping, e.g. ["uploads/*"] */
  supportsPaths: boolean;
  /** Grant methods to inject on this resource */
  grants: Grant[];
}

export interface GrantTargetConfig {
  /** Path relative to sdk/nodejs/ */
  tsFile: string;
  /** Path relative to sdk/python/anvil_cloud/ */
  pyFile: string;
  /** Generated class name to patch */
  className: string;
  /** TS property name for IAM role ARN, e.g. "roleArn" */
  roleArnProperty: string;
  /** Python property name for IAM role ARN, e.g. "role_arn" */
  pyRoleArnProperty: string;
}
