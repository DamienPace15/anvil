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
  EGRESS_GRANT_CONFIGS,
  EgressGrantConfig,
} from './configs/egress-config';

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

/** Infra resources that CAN give access */
const GRANT_CONFIGS: GrantConfig[] = [BUCKET_GRANT_CONFIG, LAMBDA_GRANT_CONFIG];

/** Compute resources that CAN receive access */
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
  if (grantConfig && !hasGrantMethods) {
    patched.push(grantConfig.grants.map((g) => g.method).join(', '));
  }
  if (grantTargetConfig && !hasGrantTarget) {
    patched.push('GrantTarget (grantName, grantRoleArn)');
  }
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
  if (grantConfig && !hasGrantMethods) {
    patched.push(
      grantConfig.grants.map((g) => toSnakeCase(g.method)).join(', ')
    );
  }
  if (grantTargetConfig && !hasGrantTarget) {
    patched.push('GrantTarget (grant_name, grant_role_arn)');
  }
  console.log(`  ✔ Patched ${file} → ${patched.join(', ')}`);
}

// ── Egress grant patching ──────────────────────────────────

function patchEgressTS(config: EgressGrantConfig): void {
  const filePath = path.join(tsDir, config.tsFile);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${config.tsFile} not found — skipping egress patch`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');

  if (content.includes('grantEgress(')) {
    console.log(`  ⏭ ${config.tsFile} grantEgress already patched — skipping`);
    return;
  }

  // Ensure grants import is present.
  content = ensureGrantsImport(content);
  content = ensureNameProperty(content);

  const method = `
  /**
   * Grants internet egress from this ${config.className}'s security group.
   * Opt-in only — a VPC-attached ${config.className} with no grantEgress has
   * zero outbound internet access by default.
   *
   * Defaults to port 443 (HTTPS) if ports is omitted.
   *
   * @example
   * fn.grantEgress({ internet: true })                        // 443 only (default)
   * fn.grantEgress({ internet: true, ports: [80, 443] })      // HTTP + HTTPS
   * fn.grantEgress({ internet: true, allPorts: true })        // unrestricted — code review signal
   */
  public grantEgress(args: grants.EgressArgs): void {
      if (!this.${config.securityGroupIdProperty}) {
          throw new Error(
              \`${config.className} "\${this.__name}" has no VPC — grantEgress requires vpc to be set.\`
          );
      }
      grants.createEgressGrant(this, this.__name, this.${config.securityGroupIdProperty} as pulumi.Output<string>, args);
  }
`;

  const classEnd = findClassEnd(content);
  if (classEnd >= 0) {
    content = content.slice(0, classEnd) + method + content.slice(classEnd);
    fs.writeFileSync(filePath, content);
    console.log(
      `  ✔ Patched ${config.tsFile} → grantEgress on ${config.className}`
    );
  } else {
    console.log(
      `  ⚠ Could not find class end in ${config.tsFile} — skipping egress patch`
    );
  }
}

function patchEgressPython(config: EgressGrantConfig): void {
  const filePath = path.join(pyDir, config.pyFile);
  if (!fs.existsSync(filePath)) {
    console.log(`  ⚠ ${config.pyFile} not found — skipping egress patch`);
    return;
  }

  let content = fs.readFileSync(filePath, 'utf8');

  if (content.includes('def grant_egress(')) {
    console.log(`  ⏭ ${config.pyFile} grant_egress already patched — skipping`);
    return;
  }

  // Ensure Optional is imported.
  if (!content.includes('Optional')) {
    content = content.replace(
      'from typing import',
      'from typing import Optional,'
    );
  }

  const method = [
    '',
    '    def grant_egress(',
    '        self,',
    '        internet: bool,',
    '        ports: Optional[list] = None,',
    '        all_ports: bool = False,',
    '    ) -> None:',
    `        """`,
    `        Grants internet egress from this ${config.className}'s security group.`,
    `        Opt-in only — a VPC-attached ${config.className} with no grant_egress`,
    `        has zero outbound internet access by default.`,
    ``,
    `        Defaults to port 443 (HTTPS) if ports is omitted.`,
    ``,
    `        Examples::`,
    ``,
    `            fn.grant_egress(internet=True)                       # 443 only`,
    `            fn.grant_egress(internet=True, ports=[80, 443])      # HTTP + HTTPS`,
    `            fn.grant_egress(internet=True, all_ports=True)       # unrestricted`,
    `        """`,
    `        from anvil_cloud import grants as _grants`,
    `        if not self.${config.pySecurityGroupIdProperty}:`,
    `            raise ValueError(`,
    `                f'${config.className} "{self.__name}" has no VPC — '`,
    `                'grant_egress requires vpc to be set.'`,
    `            )`,
    `        _grants.create_egress_grant(`,
    `            self, self.__name, self.${config.pySecurityGroupIdProperty},`,
    `            internet, ports, all_ports`,
    `        )`,
    '',
  ].join('\n');

  content = content.trimEnd() + '\n' + method + '\n';
  fs.writeFileSync(filePath, content);
  console.log(
    `  ✔ Patched ${config.pyFile} → grant_egress on ${config.className}`
  );
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

if (doTS) {
  console.log('🔐 Patching TypeScript SDK with grant methods...');
  for (const [file, configs] of Object.entries(tsFileMap)) {
    patchTSFile(file, configs.grantConfig, configs.grantTargetConfig);
  }
  console.log('✔ TypeScript grant patching complete\n');
}

if (doPython) {
  console.log('🔐 Patching Python SDK with grant methods...');
  for (const [file, configs] of Object.entries(pyFileMap)) {
    patchPyFile(file, configs.grantConfig, configs.grantTargetConfig);
  }
  console.log('✔ Python grant patching complete\n');
}

if (doTS) {
  console.log('🌐 Patching TypeScript SDK with egress grant methods...');
  for (const config of EGRESS_GRANT_CONFIGS) {
    patchEgressTS(config);
  }
  console.log('✔ TypeScript egress grant patching complete\n');
}

if (doPython) {
  console.log('🌐 Patching Python SDK with egress grant methods...');
  for (const config of EGRESS_GRANT_CONFIGS) {
    patchEgressPython(config);
  }
  console.log('✔ Python egress grant patching complete\n');
}
