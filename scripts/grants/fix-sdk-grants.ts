// scripts/grants/fix-sdk-grants.ts
//
// Patches the auto-generated SDK classes to add grant methods.
// Runs after pulumi gen-sdk and before build.
//
// Handles BOTH TypeScript and Python SDKs from a single config.
//
// Usage:
//   npx ts-node scripts/grants/fix-sdk-grants.ts           # patches both
//   npx ts-node scripts/grants/fix-sdk-grants.ts --ts      # TypeScript only
//   npx ts-node scripts/grants/fix-sdk-grants.ts --python  # Python only
//
// Adding a new compute resource with SG grants (e.g. ECS):
//   → Add one entry to scripts/grants/configs/compute-configs.ts
//   → Done. No changes here.
//
// Adding a new SG grant type (e.g. grantDatabaseAccess):
//   → Add SgGrantMethod constant to compute-configs.ts
//   → Add to each compute resource's grants array in compute-configs.ts
//   → Add runtime function to sdk/nodejs/grants.ts + sdk/python/anvil_cloud/grants.py
//   → Done. No changes here.
//
// Adding a new endpoint-like resource (implements VpcEndpointTarget):
//   → Add a patchEndpointClassTS/Python call in the "Endpoint class patching" section below

import * as fs from 'fs';
import * as path from 'path';
import { GrantConfig, GrantTargetConfig } from './types';
import {
  BUCKET_GRANT_CONFIG,
  LAMBDA_GRANT_CONFIG,
  LAMBDA_GRANT_TARGET_CONFIG,
} from './configs';
import {
  generateTSGrantMethod,
  generateTSGrantTargetMethods,
  generatePyGrantMethod,
  generatePyGrantTargetMethods,
  toSnakeCase,
} from './generators';
import {
  COMPUTE_RESOURCES,
  ComputeResourceConfig,
} from './configs/compute-config';

// ── Paths ──────────────────────────────────────────────────

const tsDir = path.join(__dirname, '..', '..', 'sdk', 'nodejs');
const pyDir = path.join(__dirname, '..', '..', 'sdk', 'python', 'anvil_cloud');

// ── CLI flags ──────────────────────────────────────────────

const cliArgs = process.argv.slice(2);
const tsOnly = cliArgs.includes('--ts');
const pyOnly = cliArgs.includes('--python');
const doTS = !pyOnly;
const doPython = !tsOnly;

// ── Grant registries ───────────────────────────────────────

const GRANT_CONFIGS: GrantConfig[] = [BUCKET_GRANT_CONFIG, LAMBDA_GRANT_CONFIG];
const GRANT_TARGET_CONFIGS: GrantTargetConfig[] = [LAMBDA_GRANT_TARGET_CONFIG];

// ── TS file helpers ────────────────────────────────────────

function findClassEnd(content: string): number {
  const interfaceIdx = content.indexOf('\nexport interface ');
  if (interfaceIdx >= 0) {
    const beforeInterface = content.substring(0, interfaceIdx);
    const lastBrace = beforeInterface.lastIndexOf('\n}');
    if (lastBrace >= 0) return lastBrace;
  }
  return content.lastIndexOf('\n}');
}

function ensureGrantsImport(content: string): string {
  if (content.includes('from "../grants"')) return content;
  const lastImportIdx = content.lastIndexOf('import ');
  const nextNewline = content.indexOf('\n', lastImportIdx);
  return (
    content.slice(0, nextNewline) +
    '\nimport * as grants from "../grants";' +
    content.slice(nextNewline)
  );
}

function ensureNameProperty(content: string): string {
  if (content.includes('__name')) return content;

  const pulumiTypePattern = /public static readonly __pulumiType = '[^']+';/;
  const match = content.match(pulumiTypePattern);
  if (match) {
    const idx = content.indexOf(match[0]) + match[0].length;
    content =
      content.slice(0, idx) +
      '\n\n    /** @internal Logical resource name for grant policy naming. */\n    private __name: string;' +
      content.slice(idx);
  }

  const superPattern = /super\([^)]*\);/;
  const superMatch = content.match(superPattern);
  if (superMatch) {
    const idx = content.indexOf(superMatch[0]) + superMatch[0].length;
    content =
      content.slice(0, idx) +
      '\n        this.__name = name;' +
      content.slice(idx);
  }

  return content;
}

// ── Python file helpers ────────────────────────────────────

function ensurePyGrantsImport(content: string): string {
  if (content.includes('from anvil_cloud import grants')) return content;

  const lines = content.split('\n');
  let lastImportIdx = 0;
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith('import ') || lines[i].startsWith('from ')) {
      lastImportIdx = i;
    }
  }
  lines.splice(
    lastImportIdx + 1,
    0,
    'from typing import Optional',
    'from anvil_cloud import grants',
    ''
  );
  return lines.join('\n');
}

function ensurePyOptionalImport(content: string): string {
  if (content.includes('Optional')) return content;
  return content.replace('from typing import', 'from typing import Optional,');
}

// ── IAM grant patching ─────────────────────────────────────

function patchTSFile(
  file: string,
  grantConfig?: GrantConfig,
  grantTargetConfig?: GrantTargetConfig
): void {
  const filePath = path.join(tsDir, file);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${file} not found — skipping`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');
  const hasGrantMethods =
    content.includes('grantRead(') || content.includes('grantInvoke(');
  const hasGrantTarget = content.includes('grantName():');

  if (hasGrantMethods && (!grantTargetConfig || hasGrantTarget)) {
    console.log(`  ⏭ ${file} already patched — skipping`);
    return;
  }

  content = ensureGrantsImport(content);
  content = ensureNameProperty(content);

  let methods = '';
  if (grantConfig && !hasGrantMethods) {
    methods += grantConfig.grants
      .map((grant) => generateTSGrantMethod(grantConfig, grant))
      .join('\n');
  }
  if (grantTargetConfig && !hasGrantTarget) {
    methods += generateTSGrantTargetMethods(grantTargetConfig.roleArnProperty);
  }
  if (methods) {
    const classEnd = findClassEnd(content);
    if (classEnd >= 0) {
      content =
        content.slice(0, classEnd) +
        '\n' +
        methods +
        '\n' +
        content.slice(classEnd);
    }
  }

  fs.writeFileSync(filePath, content);
  const patched: string[] = [];
  if (grantConfig && !hasGrantMethods)
    patched.push(grantConfig.grants.map((g) => g.method).join(', '));
  if (grantTargetConfig && !hasGrantTarget)
    patched.push('GrantTarget (grantName, grantRoleArn)');
  console.log(`  ✔ Patched ${file} → ${patched.join(', ')}`);
}

function patchPyFile(
  file: string,
  grantConfig?: GrantConfig,
  grantTargetConfig?: GrantTargetConfig
): void {
  const filePath = path.join(pyDir, file);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${file} not found — skipping`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');
  const hasGrantMethods =
    content.includes('grant_read') || content.includes('grant_invoke');
  const hasGrantTarget = content.includes('grant_name');

  if (hasGrantMethods && (!grantTargetConfig || hasGrantTarget)) {
    console.log(`  ⏭ ${file} already patched — skipping`);
    return;
  }
  if (!hasGrantMethods && !hasGrantTarget) {
    content = ensurePyGrantsImport(content);
  }

  let methods = '';
  if (grantConfig && !hasGrantMethods) {
    methods += grantConfig.grants
      .map((grant) => generatePyGrantMethod(grantConfig, grant))
      .join('');
  }
  if (grantTargetConfig && !hasGrantTarget) {
    methods += generatePyGrantTargetMethods(
      grantTargetConfig.pyRoleArnProperty
    );
  }
  if (methods) {
    content = content.trimEnd() + '\n' + methods + '\n';
  }

  fs.writeFileSync(filePath, content);
  const patched: string[] = [];
  if (grantConfig && !hasGrantMethods)
    patched.push(
      grantConfig.grants.map((g) => toSnakeCase(g.method)).join(', ')
    );
  if (grantTargetConfig && !hasGrantTarget)
    patched.push('GrantTarget (grant_name, grant_role_arn)');
  console.log(`  ✔ Patched ${file} → ${patched.join(', ')}`);
}

// ── SG grant patching — generic, config-driven ─────────────

function patchComputeResourceTS(config: ComputeResourceConfig): void {
  const filePath = path.join(tsDir, config.tsFile);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${config.tsFile} not found — skipping`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');
  content = ensureGrantsImport(content);
  content = ensureNameProperty(content);

  let anyPatched = false;
  for (const grant of config.grants) {
    if (content.includes(grant.idempotencyCheck)) {
      console.log(
        `  ⏭ ${config.tsFile} ${grant.methodName} already patched — skipping`
      );
      continue;
    }
    const method = grant.tsMethodTemplate(
      config.className,
      config.securityGroupIdProperty
    );
    const classEnd = findClassEnd(content);
    if (classEnd < 0) {
      console.log(
        `  ⚠ Could not find class end in ${config.tsFile} — skipping ${grant.methodName}`
      );
      continue;
    }
    content = content.slice(0, classEnd) + method + content.slice(classEnd);
    anyPatched = true;
    console.log(
      `  ✔ Patched ${config.tsFile} → ${grant.methodName} on ${config.className}`
    );
  }
  if (anyPatched) fs.writeFileSync(filePath, content);
}

function patchComputeResourcePython(config: ComputeResourceConfig): void {
  const filePath = path.join(pyDir, config.pyFile);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${config.pyFile} not found — skipping`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');
  content = ensurePyOptionalImport(content);

  let anyPatched = false;
  for (const grant of config.grants) {
    const pyIdempotencyCheck = `def ${grant.pyMethodName}(`;
    if (content.includes(pyIdempotencyCheck)) {
      console.log(
        `  ⏭ ${config.pyFile} ${grant.pyMethodName} already patched — skipping`
      );
      continue;
    }
    const method = grant.pyMethodTemplate(
      config.className,
      config.pySecurityGroupIdProperty
    );
    content = content.trimEnd() + '\n' + method + '\n';
    anyPatched = true;
    console.log(
      `  ✔ Patched ${config.pyFile} → ${grant.pyMethodName} on ${config.className}`
    );
  }
  if (anyPatched) fs.writeFileSync(filePath, content);
}

// ── Endpoint class patching ────────────────────────────────
//
// Injects endpointName() / endpoint_name() onto endpoint resources so they
// satisfy VpcEndpointTarget. Adding a new endpoint resource = add a call below.

function patchEndpointClassTS(tsFile: string, className: string): void {
  const filePath = path.join(tsDir, tsFile);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${tsFile} not found — skipping endpoint class patch`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');
  if (content.includes('endpointName()')) {
    console.log(`  ⏭ ${tsFile} endpointName already patched — skipping`);
    return;
  }

  content = ensureNameProperty(content);

  const method = `
  /**
   * Returns the logical name of this endpoint.
   * Implements VpcEndpointTarget — used for SG rule naming in grantEndpointAccess.
   */
  public endpointName(): string {
      return this.__name;
  }
`;

  const classEnd = findClassEnd(content);
  if (classEnd >= 0) {
    content = content.slice(0, classEnd) + method + content.slice(classEnd);
    fs.writeFileSync(filePath, content);
    console.log(`  ✔ Patched ${tsFile} → endpointName() on ${className}`);
  } else {
    console.log(
      `  ⚠ Could not find class end in ${tsFile} — skipping endpoint class patch`
    );
  }
}

function patchEndpointClassPython(pyFile: string, className: string): void {
  const filePath = path.join(pyDir, pyFile);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${pyFile} not found — skipping endpoint class patch`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');
  if (content.includes('def endpoint_name(')) {
    console.log(`  ⏭ ${pyFile} endpoint_name already patched — skipping`);
    return;
  }

  const method = [
    '',
    '    def endpoint_name(self) -> str:',
    '        """',
    '        Returns the logical name of this endpoint.',
    '        Implements VpcEndpointTarget — used for SG rule naming in grant_endpoint_access.',
    '        """',
    '        return self.__name',
    '',
  ].join('\n');

  content = content.trimEnd() + '\n' + method + '\n';
  fs.writeFileSync(filePath, content);
  console.log(`  ✔ Patched ${pyFile} → endpoint_name() on ${className}`);
}

// ── Main ───────────────────────────────────────────────────

interface FilePatches {
  grantConfig?: GrantConfig;
  grantTargetConfig?: GrantTargetConfig;
}

const tsFileMap: Record<string, FilePatches> = {};
const pyFileMap: Record<string, FilePatches> = {};

for (const config of GRANT_CONFIGS) {
  if (doTS) {
    tsFileMap[config.tsFile] = tsFileMap[config.tsFile] || {};
    tsFileMap[config.tsFile].grantConfig = config;
  }
  if (doPython) {
    pyFileMap[config.pyFile] = pyFileMap[config.pyFile] || {};
    pyFileMap[config.pyFile].grantConfig = config;
  }
}

for (const config of GRANT_TARGET_CONFIGS) {
  if (doTS) {
    tsFileMap[config.tsFile] = tsFileMap[config.tsFile] || {};
    tsFileMap[config.tsFile].grantTargetConfig = config;
  }
  if (doPython) {
    pyFileMap[config.pyFile] = pyFileMap[config.pyFile] || {};
    pyFileMap[config.pyFile].grantTargetConfig = config;
  }
}

// IAM grants
if (doTS) {
  console.log('🔐 Patching TypeScript SDK with IAM grant methods...');
  for (const [file, configs] of Object.entries(tsFileMap)) {
    patchTSFile(file, configs.grantConfig, configs.grantTargetConfig);
  }
  console.log('✔ TypeScript IAM grant patching complete\n');
}
if (doPython) {
  console.log('🔐 Patching Python SDK with IAM grant methods...');
  for (const [file, configs] of Object.entries(pyFileMap)) {
    patchPyFile(file, configs.grantConfig, configs.grantTargetConfig);
  }
  console.log('✔ Python IAM grant patching complete\n');
}

// SG grants — generic, config-driven, never changes for new resources/types
if (doTS) {
  console.log('🌐 Patching TypeScript SDK with SG grant methods...');
  for (const config of COMPUTE_RESOURCES) {
    patchComputeResourceTS(config);
  }
  console.log('✔ TypeScript SG grant patching complete\n');
}
if (doPython) {
  console.log('🌐 Patching Python SDK with SG grant methods...');
  for (const config of COMPUTE_RESOURCES) {
    patchComputeResourcePython(config);
  }
  console.log('✔ Python SG grant patching complete\n');
}

// Endpoint class patching — injects endpointName() so endpoints satisfy VpcEndpointTarget
if (doTS) {
  console.log('🔌 Patching endpoint classes...');
  patchEndpointClassTS('aws/vpcendpoint.ts', 'VpcEndpoint');
  console.log('✔ TypeScript endpoint class patching complete\n');
}
if (doPython) {
  console.log('🔌 Patching endpoint classes...');
  patchEndpointClassPython('aws/vpcendpoint.py', 'VpcEndpoint');
  console.log('✔ Python endpoint class patching complete\n');
}
