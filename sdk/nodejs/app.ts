import * as pulumi from '@pulumi/pulumi';
import * as aws from '@pulumi/aws';
import * as gcp from '@pulumi/gcp';

// ── Types ──────────────────────────────────────────────────

/**
 * Context passed to the App's run callback.
 * This is the user's only interface to Anvil runtime information.
 */
export interface Context {
  /** The current deployment stage (e.g. "dev", "staging", "prod", or OS username). */
  readonly stage: string;

  /** The project name from anvil.yaml. */
  readonly project: string;

  /** Named providers, keyed by their config name (e.g. "aws", "aws.us", "gcp.eu"). */
  readonly providers: Record<string, pulumi.ProviderResource>;

  /** Export a stack output value without importing Pulumi. */
  export(name: string, value: pulumi.Input<any>): void;
}

/** AWS provider configuration. */
export interface AwsProviderConfig {
  region?: string;
  profile?: string;
  assumeRole?: {
    roleArn: string;
    sessionName?: string;
    externalId?: string;
  };
}

/** GCP provider configuration. */
export interface GcpProviderConfig {
  project?: string;
  region?: string;
  zone?: string;
  credentials?: string;
}

/** Default options applied to all resources. */
export interface DefaultsConfig {
  /**
   * Tags merged into every taggable resource via the cloud provider's
   * native defaultTags (AWS) or defaultLabels (GCP).
   *
   * `stage` and `project` are auto-injected. User tags override auto-injected ones.
   * Per-resource tags override default tags.
   */
  tags?: Record<string, string>;
}

/**
 * Configuration for the App class.
 */
export interface AppConfig {
  /** The infrastructure definition callback. Required. */
  run: (ctx: Context) => void;

  /** Default resource options applied to all resources. */
  defaults?: DefaultsConfig;

  /**
   * Named provider configurations.
   * Keys follow the pattern "cloud" or "cloud.name":
   *   "aws"    → default AWS provider
   *   "aws.us" → named AWS provider for US region
   *   "gcp"    → default GCP provider
   *   "gcp.eu" → named GCP provider for EU
   */
  providers?: Record<string, AwsProviderConfig | GcpProviderConfig>;

  /**
   * Called before the infrastructure program runs.
   * @future Not yet implemented — reserved for forward compatibility.
   */
  beforeDeploy?: (ctx: Context) => void;

  /**
   * Called after the infrastructure program completes successfully.
   * @future CLI-side hook — not yet implemented.
   */
  afterDeploy?: (ctx: Context) => void;

  /**
   * Called if the infrastructure program throws an error.
   * @future CLI-side hook — not yet implemented.
   */
  onError?: (ctx: Context, error: Error) => void;
}

// ── Helpers ────────────────────────────────────────────────

/**
 * Determine which cloud a provider key belongs to.
 * "aws" → "aws", "aws.us" → "aws", "gcp.eu" → "gcp"
 */
function getCloud(key: string): string {
  const dot = key.indexOf('.');
  return dot === -1 ? key : key.substring(0, dot);
}

// ── App ────────────────────────────────────────────────────

/**
 * App is the entry point for an Anvil infrastructure program.
 *
 * It wraps Pulumi's runtime so users never import `@pulumi/pulumi` directly.
 * The constructor reads stage/project from Pulumi config, creates providers
 * with default tags, and invokes the user's `run` callback with a Context.
 *
 * @example
 * ```typescript
 * import { App } from "@anvil-cloud/sdk";
 * import * as anvil from "@anvil-cloud/sdk";
 *
 * export default new App({
 *   defaults: {
 *     tags: { team: "platform" },
 *   },
 *   providers: {
 *     "aws": { region: "ap-southeast-2" },
 *     "aws.us": { region: "us-east-1" },
 *   },
 *   run(ctx) {
 *     const bucket = new anvil.aws.Bucket("my-data", {
 *       dataClassification: "sensitive",
 *     });
 *     ctx.export("bucketName", bucket.bucketName);
 *   },
 * });
 * ```
 */
export class App {
  /** Stack outputs collected via ctx.export(). */
  [key: string]: any;

  constructor(config: AppConfig) {
    // ── Read config from Pulumi ────────────────────────
    const anvilConfig = new pulumi.Config('anvil');
    const stage = anvilConfig.require('stage');
    const project = pulumi.getProject();

    // ── Build default tags ─────────────────────────────
    const autoTags: Record<string, string> = { stage, project };
    const userTags = config.defaults?.tags ?? {};
    const mergedTags: Record<string, string> = { ...autoTags, ...userTags };

    // ── Create providers ───────────────────────────────
    const providers: Record<string, pulumi.ProviderResource> = {};
    const defaultProviders: Record<string, pulumi.ProviderResource> = {};

    if (config.providers) {
      for (const [key, providerConfig] of Object.entries(config.providers)) {
        const cloud = getCloud(key);
        const isDefault = !key.includes('.');
        const providerName = `anvil-provider-${key}`;

        let provider: pulumi.ProviderResource;

        switch (cloud) {
          case 'aws': {
            const awsConfig = providerConfig as AwsProviderConfig;
            const awsArgs: Record<string, any> = {
              defaultTags: { tags: mergedTags },
            };

            if (awsConfig.region) awsArgs.region = awsConfig.region;
            if (awsConfig.profile) awsArgs.profile = awsConfig.profile;
            if (awsConfig.assumeRole) {
              awsArgs.assumeRoles = [
                {
                  roleArn: awsConfig.assumeRole.roleArn,
                  sessionName: awsConfig.assumeRole.sessionName,
                  externalId: awsConfig.assumeRole.externalId,
                },
              ];
            }

            provider = new aws.Provider(providerName, awsArgs);
            break;
          }

          case 'gcp': {
            const gcpConfig = providerConfig as GcpProviderConfig;
            const gcpArgs: Record<string, any> = {
              defaultLabels: mergedTags,
            };

            if (gcpConfig.project) gcpArgs.project = gcpConfig.project;
            if (gcpConfig.region) gcpArgs.region = gcpConfig.region;
            if (gcpConfig.zone) gcpArgs.zone = gcpConfig.zone;
            if (gcpConfig.credentials)
              gcpArgs.credentials = gcpConfig.credentials;

            provider = new gcp.Provider(providerName, gcpArgs);
            break;
          }

          default:
            throw new Error(
              `Unknown cloud provider "${cloud}" in providers config key "${key}".\n` +
                `  Supported prefixes: "aws", "gcp".`
            );
        }

        providers[key] = provider;
        if (isDefault) {
          defaultProviders[cloud] = provider;
        }
      }
    }

    // If no providers configured but defaults.tags is set,
    // create implicit default providers to carry the tags.
    if (!config.providers && config.defaults?.tags) {
      const awsProvider = new aws.Provider('anvil-provider-aws', {
        defaultTags: { tags: mergedTags },
      });
      providers['aws'] = awsProvider;
      defaultProviders['aws'] = awsProvider;

      const gcpProvider = new gcp.Provider('anvil-provider-gcp', {
        defaultLabels: mergedTags,
      });
      providers['gcp'] = gcpProvider;
      defaultProviders['gcp'] = gcpProvider;
    }

    // ── Register default provider injection ────────────
    if (Object.keys(defaultProviders).length > 0) {
      pulumi.runtime.registerStackTransformation(
        (
          args: pulumi.ResourceTransformationArgs
        ): pulumi.ResourceTransformationResult | undefined => {
          const typeParts = args.type.split(':');
          let cloud: string | undefined;

          if (typeParts[0] === 'anvil' && typeParts.length >= 3) {
            cloud = typeParts[1];
          } else if (typeParts.length >= 2) {
            cloud = typeParts[0];
          }

          if (cloud && defaultProviders[cloud] && !args.opts.provider) {
            return {
              props: args.props,
              opts: pulumi.mergeOptions(args.opts, {
                provider: defaultProviders[cloud],
              }),
            };
          }

          return undefined;
        }
      );
    }

    // ── Collect exports reference ──────────────────────
    // We store exports on the App instance itself.
    // Since the entry point does `export default new App(...)`,
    // Pulumi picks up all enumerable properties as stack outputs.
    const self = this;

    // ── Create Context ─────────────────────────────────
    const ctx: Context = {
      stage,
      project,
      providers,
      export(name: string, value: pulumi.Input<any>) {
        self[name] = value;
      },
    };

    // ── Execute ────────────────────────────────────────
    try {
      config.run(ctx);
    } catch (error) {
      throw error;
    }
  }
}
