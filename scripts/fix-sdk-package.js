// scripts/fix-sdk-package.js
// Patches the auto-generated sdk/nodejs/package.json with fields
// needed for publishing to npm as @anvil-cloud/sdk

const fs = require('fs');
const path = require('path');

const pkgPath = path.join(__dirname, '..', 'sdk', 'nodejs', 'package.json');
const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));

// Override generated values.
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

// Ensure build copies package.json into bin/ so utilities.js can find it.
pkg.scripts = pkg.scripts || {};
pkg.scripts.build = 'tsc && cp package.json bin/';

fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
console.log('  ✔ Patched sdk/nodejs/package.json → @anvil-cloud/sdk');