// scripts/sdk/fix-sdk.ts
//
// Patches the auto-generated SDKs after pulumi gen-sdk:
//
// TypeScript (sdk/nodejs/):
//   1. Patches index.ts to export hand-written classes (App, Block, grants)
//   2. Patches index.ts to re-export Pulumi primitives
//   3. Patches package.json for npm publishing
//
// Python (sdk/python/):
//   1. Replaces setup.py with pyproject.toml for PyPI publishing
//   2. Patches __init__.py with App, Block, types, grants, and export imports
//   3. Patches _utilities.py for correct package name lookup
//   4. Copies/creates README.md
//
// Usage:
//   npx ts-node scripts/sdk/fix-sdk.ts           # patches both TS and Python
//   npx ts-node scripts/sdk/fix-sdk.ts --ts      # patches only TypeScript
//   npx ts-node scripts/sdk/fix-sdk.ts --python  # patches only Python

import * as fs from 'fs';
import * as path from 'path';

const cliArgs = process.argv.slice(2);
const tsOnly = cliArgs.includes('--ts');
const pyOnly = cliArgs.includes('--python');
const doTS = !pyOnly;
const doPython = !tsOnly;

const schemaPath = path.join(
  __dirname,
  '..',
  '..',
  'provider',
  'base-schema.json'
);

function getVersion(): string {
  if (fs.existsSync(schemaPath)) {
    const schema = JSON.parse(fs.readFileSync(schemaPath, 'utf8'));
    return (schema.version as string) || '0.0.1';
  }
  return '0.0.1';
}

// ════════════════════════════════════════════════════════════
// TypeScript
// ════════════════════════════════════════════════════════════

function patchTypeScript(): void {
  const sdkDir = path.join(__dirname, '..', '..', 'sdk', 'nodejs');

  console.log('📦 Patching TypeScript SDK...');

  // ── 1. Patch index.ts ──────────────────────────────────
  const indexPath = path.join(sdkDir, 'index.ts');
  if (fs.existsSync(indexPath)) {
    let indexContent = fs.readFileSync(indexPath, 'utf8');
    let changed = false;

    // App class
    const appExport =
      'export { App, AppConfig, Context, AwsProviderConfig, GcpProviderConfig, DefaultsConfig } from "./app";';
    if (!indexContent.includes('./app')) {
      indexContent =
        indexContent.trimEnd() +
        '\n\n// Hand-written App class\n' +
        appExport +
        '\n';
      changed = true;
    }

    // Block class
    const blockExport = 'export { Block, BlockArgs } from "./block";';
    if (!indexContent.includes('./block')) {
      indexContent =
        indexContent.trimEnd() +
        '\n\n// Hand-written Block class\n' +
        blockExport +
        '\n';
      changed = true;
    }

    // Grants
    if (!indexContent.includes('./grants')) {
      indexContent =
        indexContent.trimEnd() +
        '\n\n// Grant helpers\nexport * from "./grants";\n';
      changed = true;
    }

    // Pulumi primitive re-exports
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
      console.log(
        '  ✔ Patched index.ts → added App, Block, grants, and Pulumi re-exports'
      );
    }
  }

  // ── 2. Patch package.json ──────────────────────────────
  const pkgPath = path.join(sdkDir, 'package.json');
  if (fs.existsSync(pkgPath)) {
    const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));

    pkg.name = '@anvil-cloud/sdk';
    if (!pkg.version || pkg.version.includes('${')) {
      pkg.version = getVersion();
    }
    pkg.description =
      'Anvil — secure-by-default cloud infrastructure components';
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
    console.log('  ✔ Patched package.json → @anvil-cloud/sdk');
  }

  console.log('✔ TypeScript SDK patching complete\n');
}

// ════════════════════════════════════════════════════════════
// Python
// ════════════════════════════════════════════════════════════

function caretToPip(spec: string): string {
  const m = spec.match(/^\^(\d+)\.(\d+)\.(\d+)$/);
  if (!m) return spec;
  const [, majStr, minStr, patchStr] = m;
  const maj = Number(majStr);
  const min = Number(minStr);
  const patch = Number(patchStr);
  if (maj === 0) return `>=${maj}.${min}.${patch},<${maj}.${min + 1}.0`;
  return `>=${maj}.${min}.${patch},<${maj + 1}.0.0`;
}

function patchPython(): void {
  const sdkDir = path.join(__dirname, '..', '..', 'sdk', 'python');
  const readmeSrc = path.join(
    __dirname,
    '..',
    '..',
    'docs',
    'python',
    'README.md'
  );
  const readmeDst = path.join(sdkDir, 'README.md');
  const version = getVersion();

  console.log('📦 Patching Python SDK...');

  const dependencies = [
    'pulumi>=3.0.0,<4.0.0',
    `pulumi-aws${caretToPip('^7.21.0')}`,
    `pulumi-gcp${caretToPip('^9.0.0')}`,
  ];

  // ── 1. Remove setup.py, write pyproject.toml ───────────
  const setupPy = path.join(sdkDir, 'setup.py');
  if (fs.existsSync(setupPy)) {
    fs.unlinkSync(setupPy);
    console.log('  ✔ Removed generated setup.py');
  }

  const pyproject = `[build-system]
requires = ["setuptools>=68.0", "wheel"]
build-backend = "setuptools.build_meta"

[project]
name = "anvil-cloud"
version = "${version}"
description = "Anvil — secure-by-default cloud infrastructure components"
readme = "README.md"
license = "Apache-2.0"
requires-python = ">=3.8"
authors = [
    { name = "Damien Pace" },
]
keywords = ["pulumi", "anvil", "aws", "gcp", "cloud", "infrastructure"]
classifiers = [
    "Development Status :: 3 - Alpha",
    "Intended Audience :: Developers",
    "Programming Language :: Python :: 3",
    "Programming Language :: Python :: 3.8",
    "Programming Language :: Python :: 3.9",
    "Programming Language :: Python :: 3.10",
    "Programming Language :: Python :: 3.11",
    "Programming Language :: Python :: 3.12",
    "Programming Language :: Python :: 3.13",
    "Topic :: Software Development :: Libraries",
]
dependencies = [
${dependencies.map((d) => `    "${d}",`).join('\n')}
]

[project.urls]
Homepage = "https://github.com/anvil-cloud/anvil"
Repository = "https://github.com/anvil-cloud/anvil"
Documentation = "https://github.com/anvil-cloud/anvil#readme"

[tool.setuptools.packages.find]
where = ["."]
include = ["anvil_cloud*"]
`;

  fs.writeFileSync(path.join(sdkDir, 'pyproject.toml'), pyproject);
  console.log('  ✔ Wrote pyproject.toml');

  // ── 2. Copy/create README ──────────────────────────────
  if (fs.existsSync(readmeSrc)) {
    fs.copyFileSync(readmeSrc, readmeDst);
    console.log('  ✔ Copied README.md into sdk/python/');
  } else {
    const fallback = `# Anvil — Python SDK

Secure-by-default cloud infrastructure components for [Pulumi](https://www.pulumi.com/).

## Install

\`\`\`bash
pip install anvil-cloud
\`\`\`

## Quick start

\`\`\`python
import anvil_cloud as anvil

def main(ctx):
    bucket = anvil.aws.Bucket("my-data", data_classification="sensitive")
    ctx.export("bucket_name", bucket.bucket_name)

anvil.run(anvil.AppConfig(run=main))
\`\`\`

## Links

- [GitHub](https://github.com/anvil-cloud/anvil)
- [npm (Node SDK)](https://www.npmjs.com/package/@anvil-cloud/sdk)
- [Go SDK](https://pkg.go.dev/github.com/DamienPace15/anvil/sdk/go/anvil)

## License

Apache-2.0
`;
    fs.writeFileSync(readmeDst, fallback);
    console.log('  ✔ Created fallback README.md');
  }

  // ── 3. Patch _utilities.py ─────────────────────────────
  const utilitiesPath = path.join(sdkDir, 'anvil_cloud', '_utilities.py');
  if (fs.existsSync(utilitiesPath)) {
    let src = fs.readFileSync(utilitiesPath, 'utf8');
    const before = src;
    src = src.replace(
      'importlib.metadata.version(root_package)',
      'importlib.metadata.version("anvil-cloud")'
    );
    if (src !== before) {
      fs.writeFileSync(utilitiesPath, src);
      console.log(
        '  ✔ Patched _utilities.py → importlib.metadata.version("anvil-cloud")'
      );
    } else {
      console.log(
        '  ⚠ Could not find importlib.metadata.version(root_package) in _utilities.py — check manually'
      );
    }
  }

  // ── 4. Patch __init__.py ───────────────────────────────
  const initPath = path.join(sdkDir, 'anvil_cloud', '__init__.py');
  if (fs.existsSync(initPath)) {
    let init = fs.readFileSync(initPath, 'utf8');
    let changed = false;

    if (!init.includes('from .app import run')) {
      init += '\n# Typed entry point\nfrom .app import run\n';
      changed = true;
    }

    if (!init.includes('from .app import App')) {
      init += '\n# Hand-written App class\nfrom .app import App, Context\n';
      changed = true;
    }

    if (!init.includes('from .block import')) {
      init += '\n# Hand-written Block class\nfrom .block import Block\n';
      changed = true;
    }

    if (!init.includes('from .types import AppConfig')) {
      init +=
        '\n# Config classes\nfrom .types import AppConfig, DefaultsConfig, AwsProviderConfig, GcpProviderConfig, AssumeRoleConfig\n';
      changed = true;
    }

    if (!init.includes('from pulumi import export')) {
      init +=
        '\n# Re-export core Pulumi functions so users never need to import pulumi directly.\nfrom pulumi import export\n';
      changed = true;
    }

    if (!init.includes('from .grants import')) {
      init +=
        '\n# Grant helpers\nfrom .grants import GrantTarget, GrantOptions, create_grant, build_resource_arns\n';
      changed = true;
    }

    if (changed) {
      fs.writeFileSync(initPath, init);
      console.log(
        '  ✔ Patched __init__.py → added run, App, Block, types, grants, and export imports'
      );
    }
  }

  console.log(`✔ Python SDK patched → anvil-cloud v${version}`);
}

// ════════════════════════════════════════════════════════════
// Main
// ════════════════════════════════════════════════════════════

if (doTS) patchTypeScript();
if (doPython) patchPython();
