// scripts/sdk/fromid-patches.ts
//
// Config-driven fromId static method patches applied after pulumi gen-sdk.
//
// The Pulumi SDK generator only produces constructors for component resources.
// This config tells fix-sdk.ts which components need a static fromId() method
// injected, giving users a Vpc.fromId(...) API for importing existing resources.
//
// Adding a new component:
//   1. Implement the Go lookup function (e.g. VpcFromId in lookup.go)
//   2. Add an entry here — no script logic changes needed

export interface FromIdPatchConfig {
  /** Path relative to sdk/nodejs/, e.g. "aws/vpc.ts" */
  tsFile: string;

  /** Path relative to sdk/python/anvil_cloud/, e.g. "aws/vpc.py" */
  pyFile: string;

  /** The generated class name, e.g. "Vpc" */
  className: string;

  /** The args interface name for the lookup, e.g. "VpcLookupArgs" */
  tsArgsType: string;

  /** The Python args class name, e.g. "VpcLookupArgs" */
  pyArgsType: string;

  /** The Pulumi resource type token, e.g. "anvil:aws:Vpc" */
  resourceType: string;
}

export const FROMID_PATCHES: FromIdPatchConfig[] = [
  {
    tsFile: 'aws/vpc.ts',
    pyFile: 'aws/vpc.py',
    className: 'Vpc',
    tsArgsType: 'VpcLookupArgs',
    pyArgsType: 'VpcLookupArgs',
    resourceType: 'anvil:aws:Vpc',
  },
];
