import * as pulumi from '@pulumi/pulumi';

/**
 * Block is an optional organisational grouping for Anvil resources.
 *
 * Resources created inside a Block constructor are automatically parented
 * to the Block via Pulumi's built-in auto-parenting. Blocks can expose
 * public properties (outputs) for cross-Block references.
 *
 * Blocks are purely optional — flat top-level resources work identically.
 *
 * @example
 * ```ts
 * import * as anvil from "@anvil-cloud/sdk";
 *
 * class Storage extends anvil.Block {
 *   public readonly bucketName: pulumi.Output<string>;
 *
 *   constructor(name: string, args?: anvil.BlockArgs, opts?: pulumi.ComponentResourceOptions) {
 *     super(name, args, opts);
 *
 *     const bucket = new anvil.aws.Bucket("data", { ... });
 *     this.bucketName = bucket.bucketName;
 *
 *     this.registerOutputs({ bucketName: this.bucketName });
 *   }
 * }
 * ```
 */

export interface BlockArgs {
  [key: string]: any;
}

export class Block extends pulumi.ComponentResource {
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
    // Type URN uses "anvil:block:" prefix so Blocks are identifiable in state
    super(`anvil:block:${name}`, name, args ?? {}, opts);
  }
}
