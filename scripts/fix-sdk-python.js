// scripts/fix-sdk-python.js
// Patches the auto-generated sdk/python/ with fields needed for
// publishing to PyPI as `anvil-cloud`.
//
// Pulumi's Python codegen produces a setup.py with the internal
// package name (pulumi_anvil). This script replaces it with a
// pyproject.toml that uses the correct PyPI name, version, deps,
// and README.

const fs = require('fs');
const path = require('path');

const sdkDir = path.join(__dirname, '..', 'sdk', 'python');
const schemaPath = path.join(__dirname, '..', 'provider', 'base-schema.json');
const readmeSrc = path.join(__dirname, '..', 'docs', 'python', 'README.md');
const readmeDst = path.join(sdkDir, 'README.md');

// ── Read version from schema ────────────────────────────────

function getVersion() {
  if (fs.existsSync(schemaPath)) {
    const schema = JSON.parse(fs.readFileSync(schemaPath, 'utf8'));
    return schema.version || '0.0.1';
  }
  return '0.0.1';
}

// ── Convert caret (^) deps to pip-compatible ranges ─────────

function caretToPip(spec) {
  const m = spec.match(/^\^(\d+)\.(\d+)\.(\d+)$/);
  if (!m) return spec;
  const [, maj, min, patch] = m.map(Number);
  if (maj === 0) return `>=${maj}.${min}.${patch},<${maj}.${min + 1}.0`;
  return `>=${maj}.${min}.${patch},<${maj + 1}.0.0`;
}

// ── Main ────────────────────────────────────────────────────

const version = getVersion();

const dependencies = [
  'pulumi>=3.0.0,<4.0.0',
  `pulumi-aws${caretToPip('^7.21.0')}`,
  `pulumi-gcp${caretToPip('^9.0.0')}`,
];

// Remove the generated setup.py (we use pyproject.toml instead)
const setupPy = path.join(sdkDir, 'setup.py');
if (fs.existsSync(setupPy)) {
  fs.unlinkSync(setupPy);
  console.log('  ✔ Removed generated setup.py');
}

// Write pyproject.toml
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
${dependencies.map(d => `    "${d}",`).join('\n')}
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
console.log('  ✔ Wrote sdk/python/pyproject.toml');

// Copy README (or create a minimal one if docs/python/README.md doesn't exist yet)
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
import pulumi
import pulumi_anvil as anvil

bucket = anvil.aws.Bucket("my-data",
    data_classification="sensitive",
    lifecycle=90,
)

pulumi.export("bucket_name", bucket.bucket_name)
\`\`\`

## Links

- [GitHub](https://github.com/anvil-cloud/anvil)
- [npm (Node SDK)](https://www.npmjs.com/package/@anvil-cloud/sdk)
- [Go SDK](https://pkg.go.dev/github.com/DamienPace15/anvil/sdk/go/anvil)

## License

Apache-2.0
`;
  fs.writeFileSync(readmeDst, fallback);
  console.log('  ✔ Created fallback README.md in sdk/python/');
}

// Patch _utilities.py so importlib.metadata.version() looks up "anvil-cloud"
// instead of the generated "pulumi_anvil" (which doesn't match our PyPI name).
const utilitiesPath = path.join(sdkDir, 'anvil_cloud', '_utilities.py');
if (fs.existsSync(utilitiesPath)) {
  let src = fs.readFileSync(utilitiesPath, 'utf8');
  const before = src;
  // The generated code calls: importlib.metadata.version(root_package)
  // where root_package = "pulumi_anvil". We replace that call to use our PyPI name.
  src = src.replace(
    'importlib.metadata.version(root_package)',
    'importlib.metadata.version("anvil-cloud")'
  );
  if (src !== before) {
    fs.writeFileSync(utilitiesPath, src);
    console.log('  ✔ Patched _utilities.py → importlib.metadata.version("anvil-cloud")');
  } else {
    console.log('  ⚠ Could not find importlib.metadata.version(root_package) in _utilities.py — check manually');
  }
}

// Append pulumi re-exports to __init__.py so users can call anvil.export()
const initPath = path.join(sdkDir, 'anvil_cloud', '__init__.py');
if (fs.existsSync(initPath)) {
  let init = fs.readFileSync(initPath, 'utf8');
  if (!init.includes('from pulumi import export')) {
    init += '\n# Re-export core Pulumi functions so users never need to import pulumi directly.\nfrom pulumi import export\n';
    fs.writeFileSync(initPath, init);
    console.log('  ✔ Patched __init__.py → re-exported pulumi.export');
  }
}

console.log(`  ✔ Python SDK patched → anvil-cloud v${version}`);