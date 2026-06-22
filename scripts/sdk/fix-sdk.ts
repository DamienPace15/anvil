// scripts/sdk/fix-sdk.ts
//
// Patches the auto-generated SDKs after pulumi gen-sdk:
//
// TypeScript (sdk/nodejs/):
//   1. Patches index.ts to export hand-written classes (App, Block, grants)
//   2. Patches index.ts to re-export Pulumi primitives
//   3. Patches package.json for npm publishing
//   4. Patches component files with correct enum types (config-driven)
//   5. Patches component files with boolean | ObjectType shorthands (config-driven)
//   6. Patches component files with static fromId() methods (config-driven)
//
// Python (sdk/python/):
//   1. Replaces setup.py with pyproject.toml for PyPI publishing
//   2. Patches __init__.py with App, Block, types, grants, and export imports
//   3. Patches _utilities.py for correct package name lookup
//   4. Copies/creates README.md
//   5. Patches component files with correct enum types (config-driven)
//   6. Patches component files with boolean | ObjectType shorthands (config-driven)
//   7. Patches component files with static from_id() methods (config-driven)
//
// Usage:
//   npx ts-node scripts/sdk/fix-sdk.ts           # patches both TS and Python
//   npx ts-node scripts/sdk/fix-sdk.ts --ts      # patches only TypeScript
//   npx ts-node scripts/sdk/fix-sdk.ts --python  # patches only Python

import * as fs from 'fs';
import * as path from 'path';
import { ENUM_PATCHES } from './enum-patches';
import { BOOLEAN_SHORTHAND_PATCHES } from './boolean-shorthand-patches';
import { FROMID_PATCHES } from './fromid-patches';

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

  // ── 0. Patch tsconfig.json ─────────────────────────────
  // gen-sdk emits moduleResolution: "node" (a.k.a. node10), which newer
  // TypeScript flags as deprecated and exits non-zero on — breaking the
  // `tsc && cp package.json bin/` build chain. Silence the deprecation so
  // the build completes and package.json is copied into bin/ (required at
  // runtime by utilities.getVersion()).
  const tsconfigPath = path.join(sdkDir, 'tsconfig.json');
  if (fs.existsSync(tsconfigPath)) {
    const tsconfig = JSON.parse(fs.readFileSync(tsconfigPath, 'utf8'));
    tsconfig.compilerOptions = tsconfig.compilerOptions || {};
    if (tsconfig.compilerOptions.ignoreDeprecations !== '6.0') {
      tsconfig.compilerOptions.ignoreDeprecations = '6.0';
      fs.writeFileSync(tsconfigPath, JSON.stringify(tsconfig, null, 4) + '\n');
      console.log('  ✔ Patched tsconfig.json → ignoreDeprecations: "6.0"');
    }
  }

  // ── 1. Patch index.ts ──────────────────────────────────
  const indexPath = path.join(sdkDir, 'index.ts');
  if (fs.existsSync(indexPath)) {
    let indexContent = fs.readFileSync(indexPath, 'utf8');
    let changed = false;

    // App class
    const appExport =
      'export { App, AppConfig, Context, AwsProviderConfig, GcpProviderConfig, DefaultsConfig, ComplianceFramework } from "./app";';

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
      url: 'github.com/DamienPace15/anvil',
      directory: 'sdk/nodejs',
    };

    pkg.scripts = pkg.scripts || {};
    pkg.scripts.build = 'tsc && cp package.json bin/';

    fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
    console.log('  ✔ Patched package.json → @anvil-cloud/sdk');
  }

  // ── 3. Patch enum types ────────────────────────────────
  for (const patch of ENUM_PATCHES) {
    const filePath = path.join(sdkDir, patch.tsFile);
    if (!fs.existsSync(filePath)) {
      console.log(`  ⚠ ${patch.tsFile} not found — skipping enum patches`);
      continue;
    }

    let content = fs.readFileSync(filePath, 'utf8');
    let changed = false;

    if (!content.includes('import * as enums from "../types/enums"')) {
      content = content.replace(
        'import * as utilities from "../utilities";',
        'import * as utilities from "../utilities";\nimport * as enums from "../types/enums";'
      );
      changed = true;
    }

    for (const field of patch.fields) {
      const optional = field.required ? '' : '?';
      const plainType = `${field.field}${optional}: pulumi.Input<string>`;
      const enumType = `${field.field}${optional}: pulumi.Input<${field.tsEnumType} | string>`;

      if (content.includes(plainType) && !content.includes(enumType)) {
        content = content.replace(plainType, enumType);
        changed = true;
      }
    }

    if (changed) {
      fs.writeFileSync(filePath, content);
      const fields = patch.fields.map((f) => f.field).join(', ');
      console.log(`  ✔ Patched ${patch.tsFile} → enum types for ${fields}`);
    } else {
      console.log(`  ⏭ ${patch.tsFile} enum types already patched — skipping`);
    }
  }

  // ── 4. Patch boolean shorthands ────────────────────────
  // Upgrades field?: ObjectType to field?: boolean | ObjectType so users
  // can write bastion: true instead of bastion: {} for defaults.
  //
  // NOTE: Pulumi's SDK generator appends "Args" to nested object type names,
  // so VpcBastionArgs in the schema becomes VpcBastionArgsArgs in the
  // generated TypeScript. The patch uses tsObjectType + "Args" to match.
  for (const patch of BOOLEAN_SHORTHAND_PATCHES) {
    const filePath = path.join(sdkDir, patch.tsFile);
    if (!fs.existsSync(filePath)) {
      console.log(
        `  ⚠ ${patch.tsFile} not found — skipping boolean shorthand patches`
      );
      continue;
    }

    let content = fs.readFileSync(filePath, 'utf8');
    let changed = false;

    for (const field of patch.fields) {
      // Pulumi generates VpcBastionArgsArgs (double Args) for nested types.
      // tsObjectType is "VpcBastionArgs" so generated name is tsObjectType + "Args".
      const generatedType = `inputs.aws.${field.tsObjectType}Args`;

      // Patch the pulumi.Input wrapped form (VpcArgs interface)
      const plainInputForm = `${field.field}?: pulumi.Input<${generatedType}>;`;
      const unionInputForm = `${field.field}?: pulumi.Input<boolean | ${generatedType}>;`;
      if (
        content.includes(plainInputForm) &&
        !content.includes(unionInputForm)
      ) {
        content = content.replace(plainInputForm, unionInputForm);
        changed = true;
      }

      // Patch the plain object form (other interfaces)
      const plainForm = `${field.field}?: ${generatedType};`;
      const unionForm = `${field.field}?: boolean | ${generatedType};`;
      if (content.includes(plainForm) && !content.includes(unionForm)) {
        content = content.replace(plainForm, unionForm);
        changed = true;
      }

      // Inject normalise helper once per field if not already present.
      // Uses the generated type name (with double Args) throughout so
      // TypeScript resolves it correctly.
      const normName = `normalise${field.field
        .charAt(0)
        .toUpperCase()}${field.field.slice(1)}`;
      if (!content.includes(normName)) {
        const helper = [
          '',
          `/**`,
          ` * Normalises the \`${field.field}\` shorthand so the Pulumi provider`,
          ` * always receives an object, never a raw boolean.`,
          ` *`,
          ` *   ${field.field}: true          // enable with all defaults`,
          ` *   ${field.field}: {}            // identical to true`,
          ` *   ${field.field}: { ... }       // enable with custom config`,
          ` */`,
          `export function ${normName}(`,
          `  val: boolean | ${generatedType} | undefined`,
          `): ${generatedType} | undefined {`,
          `  if (val === undefined || val === false) return undefined;`,
          `  if (val === true) return {};`,
          `  return val;`,
          `}`,
          '',
        ].join('\n');
        content = content.trimEnd() + '\n' + helper;
        changed = true;
      }
    }

    if (changed) {
      fs.writeFileSync(filePath, content);
      const fields = patch.fields.map((f) => f.field).join(', ');
      console.log(
        `  ✔ Patched ${patch.tsFile} → boolean shorthand for ${fields}`
      );
    } else {
      console.log(
        `  ⏭ ${patch.tsFile} boolean shorthands already patched — skipping`
      );
    }
  }

  // ── 5. Patch fromId static methods ────────────────────
  for (const patch of FROMID_PATCHES) {
    const filePath = path.join(sdkDir, patch.tsFile);
    if (!fs.existsSync(filePath)) {
      console.log(`  ⚠ ${patch.tsFile} not found — skipping fromId patch`);
      continue;
    }

    let content = fs.readFileSync(filePath, 'utf8');

    if (content.includes('static fromId(')) {
      console.log(`  ⏭ ${patch.tsFile} fromId already patched — skipping`);
      continue;
    }

    const fromIdMethod = [
      '',
      '    /**',
      `     * Imports an existing ${patch.className} into Anvil without managing or modifying it.`,
      `     * Returns an identical output shape to \`new ${patch.className}()\`.`,
      `     *`,
      `     * Flow logs, NAT, and bastion are not available on an imported VPC.`,
      `     *`,
      `     * If subnet IDs are omitted, Anvil auto-discovers them by inspecting`,
      `     * route tables. Provide IDs explicitly if auto-discovery fails.`,
      `     *`,
      `     * @example`,
      `     * const network = ${patch.className}.fromId("existing", {`,
      `     *   vpcId: "vpc-0abc123def456",`,
      `     * });`,
      `     */`,
      `    static fromId(`,
      `      name: string,`,
      `      args: {`,
      `        vpcId: string;`,
      `        privateSubnetIds?: string[];`,
      `        publicSubnetIds?: string[];`,
      `      },`,
      `      opts?: pulumi.ComponentResourceOptions`,
      `    ): ${patch.className} {`,
      `      return new ${patch.className}(name, args as any, {`,
      `        ...opts,`,
      `        id: args.vpcId,`,
      `      });`,
      `    }`,
      '',
    ].join('\n');

    // Insert before the closing brace of the class, which sits just before
    // the first exported interface that follows it.
    const interfaceIdx = content.indexOf('\nexport interface ');
    const insertAt =
      interfaceIdx >= 0
        ? content.lastIndexOf('\n}', interfaceIdx)
        : content.lastIndexOf('\n}');

    if (insertAt >= 0) {
      content =
        content.slice(0, insertAt) + fromIdMethod + content.slice(insertAt);
      fs.writeFileSync(filePath, content);
      console.log(
        `  ✔ Patched ${patch.tsFile} → static fromId() on ${patch.className}`
      );
    } else {
      console.log(
        `  ⚠ Could not find class end in ${patch.tsFile} — skipping fromId patch`
      );
    }
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

  // ── 5. Patch enum types ────────────────────────────────
  const pyCloudDir = path.join(sdkDir, 'anvil_cloud');
  for (const patch of ENUM_PATCHES) {
    const filePath = path.join(pyCloudDir, patch.pyFile);
    if (!fs.existsSync(filePath)) {
      console.log(`  ⚠ ${patch.pyFile} not found — skipping enum patches`);
      continue;
    }

    let content = fs.readFileSync(filePath, 'utf8');
    let changed = false;

    if (!content.includes('from .. import _enums as enums')) {
      const lines = content.split('\n');
      let lastImportIdx = 0;
      for (let i = 0; i < lines.length; i++) {
        if (lines[i].startsWith('import ') || lines[i].startsWith('from ')) {
          lastImportIdx = i;
        }
      }
      lines.splice(lastImportIdx + 1, 0, 'from .. import _enums as enums');
      content = lines.join('\n');
      changed = true;
    }

    for (const field of patch.fields) {
      const pyField = toSnakeCase(field.field);
      const enumType = `Optional[Union['enums.${field.pyEnumType}', str]]`;
      const fieldPattern = new RegExp(`(${pyField}\\s*:\\s*)Optional\\[str\\]`);
      if (fieldPattern.test(content) && !content.includes(enumType)) {
        if (!content.includes('Union')) {
          content = content.replace(
            'from typing import',
            'from typing import Union,'
          );
        }
        content = content.replace(fieldPattern, `$1${enumType}`);
        changed = true;
      }
    }

    if (changed) {
      fs.writeFileSync(filePath, content);
      const fields = patch.fields.map((f) => toSnakeCase(f.field)).join(', ');
      console.log(`  ✔ Patched ${patch.pyFile} → enum types for ${fields}`);
    } else {
      console.log(`  ⏭ ${patch.pyFile} enum types already patched — skipping`);
    }
  }

  // ── 6. Patch boolean shorthands ────────────────────────
  // Upgrades field: Optional[ObjectType] to Optional[Union[bool, ObjectType]]
  // so users can write bastion=True instead of bastion={} for defaults.
  for (const patch of BOOLEAN_SHORTHAND_PATCHES) {
    const filePath = path.join(pyCloudDir, patch.pyFile);
    if (!fs.existsSync(filePath)) {
      console.log(
        `  ⚠ ${patch.pyFile} not found — skipping boolean shorthand patches`
      );
      continue;
    }

    let content = fs.readFileSync(filePath, 'utf8');
    let changed = false;

    for (const field of patch.fields) {
      const pyField = toSnakeCase(field.field);
      const plainType = `Optional['${field.pyObjectType}']`;
      const unionType = `Optional[Union[bool, '${field.pyObjectType}']]`;

      const fieldPattern = new RegExp(
        `(${pyField}\\s*:\\s*)Optional\\['${field.pyObjectType}'\\]`
      );

      if (fieldPattern.test(content) && !content.includes(unionType)) {
        if (!content.includes('Union')) {
          content = content.replace(
            'from typing import',
            'from typing import Union,'
          );
        }
        content = content.replace(fieldPattern, `$1${unionType}`);
        changed = true;
      }
    }

    if (changed) {
      fs.writeFileSync(filePath, content);
      const fields = patch.fields.map((f) => toSnakeCase(f.field)).join(', ');
      console.log(
        `  ✔ Patched ${patch.pyFile} → boolean shorthand for ${fields}`
      );
    } else {
      console.log(
        `  ⏭ ${patch.pyFile} boolean shorthands already patched — skipping`
      );
    }
  }

  // ── 7. Patch fromId static methods ────────────────────
  for (const patch of FROMID_PATCHES) {
    const filePath = path.join(pyCloudDir, patch.pyFile);
    if (!fs.existsSync(filePath)) {
      console.log(`  ⚠ ${patch.pyFile} not found — skipping fromId patch`);
      continue;
    }

    let content = fs.readFileSync(filePath, 'utf8');

    if (content.includes('def from_id(')) {
      console.log(`  ⏭ ${patch.pyFile} from_id already patched — skipping`);
      continue;
    }

    // Ensure Optional is imported — from_id uses it for opts.
    if (!content.includes('from typing import')) {
      content = 'from typing import Optional\n' + content;
    } else if (!content.includes('Optional')) {
      content = content.replace(
        'from typing import',
        'from typing import Optional,'
      );
    }

    const fromIdMethod = [
      '',
      '    @staticmethod',
      `    def from_id(`,
      `        name: str,`,
      `        args: '${patch.pyArgsType}',`,
      `        opts: Optional[pulumi.ComponentResourceOptions] = None`,
      `    ) -> '${patch.className}':`,
      `        """`,
      `        Imports an existing ${patch.className} into Anvil without managing or modifying it.`,
      `        Returns an identical output shape to constructing a new ${patch.className}.`,
      ``,
      `        Flow logs, NAT, and bastion are not available on an imported VPC.`,
      ``,
      `        If subnet IDs are omitted, Anvil auto-discovers them by inspecting`,
      `        route tables. Provide IDs explicitly if auto-discovery fails.`,
      `        """`,
      `        return ${patch.className}(name, args, opts)  # type: ignore`,
      '',
    ].join('\n');

    content = content.trimEnd() + '\n' + fromIdMethod + '\n';
    fs.writeFileSync(filePath, content);
    console.log(
      `  ✔ Patched ${patch.pyFile} → static from_id() on ${patch.className}`
    );
  }

  console.log(`✔ Python SDK patched → anvil-cloud v${version}`);
}

// ── Helpers ────────────────────────────────────────────────

function toSnakeCase(str: string): string {
  return str.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`);
}

// ════════════════════════════════════════════════════════════
// Main
// ════════════════════════════════════════════════════════════

if (doTS) patchTypeScript();
if (doPython) patchPython();
