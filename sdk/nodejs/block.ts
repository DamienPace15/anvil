import * as pulumi from '@pulumi/pulumi';

/**
 * Block is an optional organisational grouping for Anvil resources.
 *
 * Resources created inside a Block constructor are automatically parented
 * to the Block via Pulumi's built-in auto-parenting. Blocks can expose
 * public properties (outputs) for cross-Block references.
 *
 * `stage` and `project` are automatically available via `this.stage` and
 * `this.project` — read from the same Pulumi config that App sets.
 *
 * Tagging is automatic — the App's provider injection applies `defaultTags`
 * to all resources, including those inside Blocks.
 *
 * Blocks are purely optional — flat top-level resources work identically.
 *
 * @example
 * ```ts
 * import * as anvil from "@anvil-cloud/sdk";
 *
 * class RulesEngine extends anvil.Block {
 *   public readonly bucketName: pulumi.Output<string>;
 *
 *   constructor(name: string, opts?: pulumi.ComponentResourceOptions) {
 *     super(name, {}, opts);
 *
 *     const bucket = new anvil.aws.Bucket("events", {
 *       dataClassification: "internal",
 *       transform: {
 *         bucket: { bucket: `rules-${this.stage}-events` },
 *       },
 *     });
 *   }
 * }
 * ```
 */

export interface BlockArgs {
  [key: string]: any;
}

export class Block extends pulumi.ComponentResource {
  /** The current deployment stage (e.g. "dev", "staging", "prod", or OS username). */
  public readonly stage: string;

  /** The project name from anvil.yaml. */
  public readonly project: string;

  /**
   * Creates a new Block.
   *
   * @param name  The unique name of this Block within the stack.
   * @param args  Optional arguments (passed through for subclass use).
   * @param opts  Standard Pulumi ComponentResourceOptions (aliases, providers, etc.).
   */
  constructor(
    name: string,
    args?: BlockArgs,
    opts?: pulumi.ComponentResourceOptions
  ) {
    const typeName = new.target?.name ?? name;
    super(`anvil:block:${typeName}`, name, args ?? {}, opts);

    const anvilConfig = new pulumi.Config('anvil');
    this.stage = anvilConfig.require('stage');
    this.project = pulumi.getProject();
  }
}
