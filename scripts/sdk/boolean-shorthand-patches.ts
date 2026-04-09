// ── Boolean Shorthand Patches ──────────────────────────────────────────────
//
// Some optional feature fields accept either:
//   - true  — enable with all defaults (shorthand)
//   - {}    — enable with defaults (explicit empty object)
//   - { ... } — enable with custom config
//
// This patch upgrades generated "field?: ObjectType" to
// "field?: boolean | ObjectType" in the TypeScript SDK, and adds a
// normalisation helper that converts `true` to `{}` before the value
// reaches the Pulumi provider.
//
// Adding a new boolean shorthand field:
//   1. Add an entry to BOOLEAN_SHORTHAND_PATCHES below
//   2. No other changes needed — fix-sdk.ts handles everything
//
// Examples of fields suited to this pattern:
//   - bastion: true  →  bastion: {}  (VpcBastionArgs)
//   - flowLogs: true →  flowLogs: {} (VpcFlowLogsArgs)  [future]
//   - encryption: true → encryption: {} (EncryptionArgs) [future]

export interface BooleanShorthandPatch {
  /** Path relative to sdk/nodejs/, e.g. "aws/vpc.ts" */
  tsFile: string;

  /** Path relative to sdk/python/anvil_cloud/, e.g. "aws/vpc.py" */
  pyFile: string;

  /** Fields to patch with boolean | ObjectType union */
  fields: BooleanShorthandField[];
}

export interface BooleanShorthandField {
  /** The field name on the Args interface, e.g. "bastion" */
  field: string;

  /**
   * The TypeScript object type name as defined in schema.json, e.g. "VpcBastionArgs".
   *
   * IMPORTANT: Pulumi's SDK generator appends "Args" to nested object types,
   * so "VpcBastionArgs" in the schema becomes "VpcBastionArgsArgs" in the
   * generated TypeScript. The patch script handles this automatically by
   * appending "Args" when building the generated type name. You should use
   * the schema name here (without the extra Args).
   *
   * The patch generates: field?: boolean | inputs.aws.VpcBastionArgsArgs
   */
  tsObjectType: string;

  /**
   * The Python type name as defined in schema.json, e.g. "VpcBastionArgs".
   * The patch generates: Optional[Union[bool, VpcBastionArgs]]
   */
  pyObjectType: string;
}

export const BOOLEAN_SHORTHAND_PATCHES: BooleanShorthandPatch[] = [
  // ── AWS VPC ────────────────────────────────────────────
  {
    tsFile: 'aws/vpc.ts',
    pyFile: 'aws/vpc.py',
    fields: [
      {
        field: 'bastion',
        tsObjectType: 'VpcBastionArgs',
        pyObjectType: 'VpcBastionArgs',
      },
    ],
  },

  // ── Future examples (uncomment when components are built) ──
  // {
  //   tsFile: 'aws/vpc.ts',
  //   pyFile: 'aws/vpc.py',
  //   fields: [
  //     {
  //       field: 'flowLogs',
  //       tsObjectType: 'VpcFlowLogsArgs',
  //       pyObjectType: 'VpcFlowLogsArgs',
  //     },
  //   ],
  // },
];
