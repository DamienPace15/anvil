// scripts/sdk/enum-patches.ts
//
// Config-driven enum type patches applied after pulumi gen-sdk.
//
// The Pulumi SDK generator types component input properties as plain
// `pulumi.Input<string>` even when the schema defines a $ref to a named
// enum type. This config tells fix-sdk.ts which fields to upgrade to
// their proper enum union types, giving users full IDE intellisense.
//
// Adding a new component:
//   1. Add the named enum types to the component's schema.json types block
//   2. Add an entry here — no script logic changes needed
//
// Enum type format:
//   TypeScript: "enums.{cloud}.{TypeName}"  e.g. "enums.aws.LambdaArchitecture"
//   Python:     "{TypeName}"                e.g. "LambdaArchitecture"
//               (Python uses string literals from the enums module)

export interface EnumFieldPatch {
  /** The field name on the Args interface, e.g. "architecture" */
  field: string;

  /** Whether the field is required (no ?) or optional (with ?) */
  required?: boolean;

  /** TypeScript enum type reference, e.g. "enums.aws.LambdaArchitecture" */
  tsEnumType: string;

  /** Python enum type reference, e.g. "LambdaArchitecture" */
  pyEnumType: string;
}

export interface EnumPatchConfig {
  /** Path relative to sdk/nodejs/, e.g. "aws/lambda.ts" */
  tsFile: string;

  /** Path relative to sdk/python/anvil_cloud/, e.g. "aws/lambda_.py" */
  pyFile: string;

  /** Fields to patch with enum types */
  fields: EnumFieldPatch[];
}

export const ENUM_PATCHES: EnumPatchConfig[] = [
  // ── AWS Lambda ─────────────────────────────────────────
  {
    tsFile: 'aws/lambda.ts',
    pyFile: 'aws/lambda_.py',
    fields: [
      {
        field: 'architecture',
        tsEnumType: 'enums.aws.LambdaArchitecture',
        pyEnumType: 'LambdaArchitecture',
      },
      {
        field: 'runtime',
        required: true,
        tsEnumType: 'enums.aws.LambdaRuntime',
        pyEnumType: 'LambdaRuntime',
      },
      {
        field: 'logRetention',
        tsEnumType: 'enums.aws.LambdaLogRetention',
        pyEnumType: 'LambdaLogRetention',
      },
    ],
  },
  {
    tsFile: 'aws/vpc.ts',
    pyFile: 'aws/vpc.py',
    fields: [
      {
        field: 'natType',
        required: true,
        tsEnumType: 'enums.aws.VpcNatType',
        pyEnumType: 'VpcNatType',
      },
    ],
  },

  // ── AWS Bucket ─────────────────────────────────────────
  // Add bucket enum patches here when storageTransition and
  // versionRetention are added as named types to bucket/schema.json
  // {
  //   tsFile: 'aws/bucket.ts',
  //   pyFile: 'aws/bucket.py',
  //   fields: [
  //     {
  //       field: 'storageTransition',
  //       tsEnumType: 'enums.aws.BucketStorageTransition',
  //       pyEnumType: 'BucketStorageTransition',
  //     },
  //     {
  //       field: 'versionRetention',
  //       tsEnumType: 'enums.aws.BucketVersionRetention',
  //       pyEnumType: 'BucketVersionRetention',
  //     },
  //   ],
  // },

  // ── GCP Function ───────────────────────────────────────
  // Add GCP patches here as components are built
  // {
  //   tsFile: 'gcp/function.ts',
  //   pyFile: 'gcp/function.py',
  //   fields: [...],
  // },
];
