// Hand-written exports merged into the generated index.ts barrel.
//
// gen-sdk regenerates index.ts on every build with no knowledge of the
// hand-written modules in this overlay. fix-sdk.ts appends a single
// `export * from "./_extras";` to the generated index — everything those
// extra exports resolve to lives here, as real code (not strings in a script).
import * as pulumi from "@pulumi/pulumi";

// Hand-written App class.
// NOTE: ComplianceFramework is intentionally NOT re-exported here — it is now a
// schema-driven enum emitted into ./types/enums (identical string union, plus a
// const object) and re-exported by the generated index. Re-exporting app.ts's
// duplicate via this barrel would create an ambiguous `export *` collision.
export {
  App,
  AppConfig,
  Context,
  AwsProviderConfig,
  GcpProviderConfig,
  DefaultsConfig,
} from "./app";

// Hand-written Block class
export { Block, BlockArgs } from "./block";

// Grant helpers
export * from "./grants";

// Re-exported Pulumi primitives
// Users can import anvil.Output, anvil.ComponentResource, etc. without @pulumi/pulumi
export {
  ComponentResource,
  ComponentResourceOptions,
  CustomResource,
  ResourceOptions,
  ProviderResource,
  Config,
  output,
  all,
  secret,
  interpolate,
  concat,
  getProject,
  getStack,
} from "@pulumi/pulumi";
export type { Output, Input, Inputs } from "@pulumi/pulumi";

// Escape hatch — full Pulumi namespace for anything not re-exported
export { pulumi };
