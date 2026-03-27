// scripts/fix-sdk-package.js
// Patches the auto-generated sdk/nodejs after pulumi gen-sdk:
// 1. Patches index.ts to export hand-written classes (App, Block)
// 2. Patches index.ts to re-export Pulumi primitives
// 3. Patches package.json for npm publishing
//
// NOTE: Hand-written files (app.ts, block.ts) are committed directly
// in sdk/nodejs/. The gen-sdk command does NOT overwrite them because
// Pulumi only generates files for resources in the schema.

const fs = require('fs');
const path = require('path');

const sdkDir = path.join(__dirname, '..', 'sdk', 'nodejs');

// ── 1. Patch index.ts ──────────────────────────────────────
const indexPath = path.join(sdkDir, 'index.ts');
if (fs.existsSync(indexPath)) {
  let indexContent = fs.readFileSync(indexPath, 'utf8');
  let changed = false;

  // App class
  const appExport = 'export { App, AppConfig, Context, AwsProviderConfig, GcpProviderConfig, DefaultsConfig } from "./app";';
  if (!indexContent.includes('./app')) {
    indexContent = indexContent.trimEnd() + '\n\n// Hand-written App class\n' + appExport + '\n';
    changed = true;
  }

  // Block class
  const blockExport = 'export { Block, BlockArgs } from "./block";';
  if (!indexContent.includes('./block')) {
    indexContent = indexContent.trimEnd() + '\n\n// Hand-written Block class\n' + blockExport + '\n';
    changed = true;
  }

  // Pulumi primitive re-exports (directly in index.ts to avoid
  // conflict with the generated types/ directory)
  if (!indexContent.includes('Re-exported Pulumi primitives')) {
    const pulumiReExports = [
      '',
      '// Re-exported Pulumi primitives',
      '// Users can import anvil.Output, anvil.ComponentResource, etc. without @pulumi/pulumi',
      'export {',
      '  ComponentResource,',
      '  ComponentResourceOptions,',
      '  CustomResource,',
      '  ResourceOptions,',
      '  ProviderResource,',
      '  Config,',
      '  output,',
      '  all,',
      '  secret,',
      '  interpolate,',
      '  concat,',
      '  getProject,',
      '  getStack,',
      '} from "@pulumi/pulumi";',
      'export type { Output, Input, Inputs } from "@pulumi/pulumi";',
      '',
      '',
      '// Escape hatch — full Pulumi namespace for anything not re-exported',
      'export { pulumi };',
    ].join('\n');
    indexContent = indexContent.trimEnd() + '\n' + pulumiReExports + '\n';
    changed = true;
  }

  if (changed) {
    fs.writeFileSync(indexPath, indexContent);
    console.log('  ✔ Patched sdk/nodejs/index.ts → added App, Block, and Pulumi re-exports');
  }
}

// ── 2. Patch package.json ──────────────────────────────────
const pkgPath = path.join(sdkDir, 'package.json');
const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));

pkg.name = '@anvil-cloud/sdk';
if (!pkg.version || pkg.version.includes('${')) {
  const schemaPath = path.join(__dirname, '..', 'provider', 'base-schema.json');
  const schema = JSON.parse(fs.readFileSync(schemaPath, 'utf8'));
  pkg.version = schema.version || '0.0.1';
}
pkg.description = 'Anvil — secure-by-default cloud infrastructure components';
pkg.main = 'bin/index.js';
pkg.types = 'bin/index.d.ts';
pkg.license = 'Apache-2.0';
pkg.homepage = 'https://github.com/anvil-cloud/anvil';
pkg.repository = {
  type: 'git',
  url: 'https://github.com/anvil-cloud/anvil.git',
  directory: 'sdk/nodejs',
};

pkg.scripts = pkg.scripts || {};
pkg.scripts.build = 'tsc && cp package.json bin/';

fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
console.log('  ✔ Patched sdk/nodejs/package.json → @anvil-cloud/sdk');