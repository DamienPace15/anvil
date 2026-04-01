// scripts/fix-sdk-grants.js
//
// Patches the auto-generated SDK classes to add grant methods.
// Runs after pulumi gen-sdk and before build.
//
// Handles BOTH TypeScript and Python SDKs from a single config.
//
// This script injects:
// 1. Grant methods on infra resources (e.g. Bucket.grantRead / bucket.grant_read)
// 2. GrantTarget implementation on compute resources (e.g. Lambda.grantName / lambda.grant_name)
//
// Usage:
//   node fix-sdk-grants.js           # patches both TS and Python
//   node fix-sdk-grants.js --ts      # patches only TypeScript
//   node fix-sdk-grants.js --python  # patches only Python

const fs = require("fs");
const path = require("path");

const tsDir = path.join(__dirname, "..", "sdk", "nodejs");
const pyDir = path.join(__dirname, "..", "sdk", "python", "anvil_cloud");

const args = process.argv.slice(2);
const tsOnly = args.includes("--ts");
const pyOnly = args.includes("--python");
const doTS = !pyOnly;
const doPython = !tsOnly;

// ── Shared Grant Definitions ───────────────────────────────
//
// Language-neutral config. TS method names are canonical;
// Python names are derived automatically (camelCase → snake_case).

const GRANT_CONFIGS = [
    {
        tsFile: "aws/bucket.ts",
        pyFile: "aws/bucket.py",
        className: "Bucket",
        arnProperty: "arn",
        supportsPaths: true,
        grants: [
            { method: "grantRead", actions: ["s3:GetObject", "s3:ListBucket"] },
            { method: "grantWrite", actions: ["s3:PutObject"] },
            { method: "grantReadWrite", actions: ["s3:GetObject", "s3:ListBucket", "s3:PutObject"] },
            { method: "grantDelete", actions: ["s3:DeleteObject"] },
            {
                method: "grantFullAccess",
                actions: ["s3:GetObject", "s3:ListBucket", "s3:PutObject", "s3:DeleteObject"],
                isFullAccess: true,
            },
        ],
    },
    {
        tsFile: "aws/lambda.ts",
        pyFile: "aws/lambda_.py",
        className: "Lambda",
        arnProperty: "arn",
        supportsPaths: false,
        grants: [
            { method: "grantInvoke", actions: ["lambda:InvokeFunction"] },
        ],
    },
];

const GRANT_TARGET_CONFIGS = [
    {
        tsFile: "aws/lambda.ts",
        pyFile: "aws/lambda_.py",
        className: "Lambda",
        roleArnProperty: "roleArn",         // TS property name
        pyRoleArnProperty: "role_arn",       // Python property name
    },
];

// ── Naming Helpers ─────────────────────────────────────────

/** camelCase → snake_case */
function toSnakeCase(str) {
    return str.replace(/([a-z])([A-Z])/g, "$1_$2").toLowerCase();
}

/** "grantRead" → "read", "grantFullAccess" → "fullaccess" */
function grantSuffix(method) {
    return method.replace("grant", "").toLowerCase();
}

// ════════════════════════════════════════════════════════════
// TypeScript Patching
// ════════════════════════════════════════════════════════════

function generateTSGrantMethod(config, grant) {
    const { className, arnProperty, supportsPaths } = config;
    const { method, actions, isFullAccess } = grant;
    const actionsStr = actions.map((a) => `"${a}"`).join(", ");
    const suffix = grantSuffix(method);

    if (isFullAccess) {
        return `
    /**
     * Grants full access (${actions.join(", ")}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * This is an escape hatch — prefer scoped grants (grantRead, grantWrite, etc.).
     * A warning is logged if no justification is provided.
     */
    public ${method}(target: grants.GrantTarget, opts?: grants.GrantOptions): void {
        if (!opts?.justification) {
            pulumi.log.warn(
                \`⚠ \${this.__name} → \${target.grantName()}: full access granted with no justification. \` +
                \`Consider scoping with grantRead, grantWrite, or grantDelete, \` +
                \`or add a justification.\`,
                this,
            );
        } else {
            pulumi.log.info(
                \`ℹ \${this.__name} → \${target.grantName()}: full access granted. Justification: "\${opts.justification}"\`,
                this,
            );
        }
        const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, undefined);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
    }

    if (supportsPaths) {
        return `
    /**
     * Grants ${suffix} access (${actions.join(", ")}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * @param target - The compute resource to grant access to.
     * @param paths - Optional array of path prefixes to scope access (e.g. ["uploads/*"]).
     * @param opts - Optional grant options (justification for audit trail).
     */
    public ${method}(target: grants.GrantTarget, paths?: string[], opts?: grants.GrantOptions): void {
        const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, paths);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
    }

    return `
    /**
     * Grants ${suffix} access (${actions.join(", ")}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * @param target - The compute resource to grant access to.
     * @param opts - Optional grant options (justification for audit trail).
     */
    public ${method}(target: grants.GrantTarget, opts?: grants.GrantOptions): void {
        const name = \`\${this.__name}-\${target.grantName()}-${suffix}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, undefined);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
}

function generateTSGrantTargetMethods(roleArnProperty) {
    return `
    /** Implements GrantTarget — returns the logical resource name. */
    public grantName(): string {
        return this.__name;
    }

    /** Implements GrantTarget — returns the IAM execution role ARN. */
    public grantRoleArn(): pulumi.Output<string> {
        return this.${roleArnProperty};
    }`;
}

// ── TS Helpers ─────────────────────────────────────────────

function findClassEnd(content) {
    const interfaceIdx = content.indexOf("\nexport interface ");
    if (interfaceIdx >= 0) {
        const beforeInterface = content.substring(0, interfaceIdx);
        const lastBrace = beforeInterface.lastIndexOf("\n}");
        if (lastBrace >= 0) return lastBrace;
    }
    return content.lastIndexOf("\n}");
}

function ensureGrantsImport(content) {
    if (content.includes('from "../grants"')) return content;
    const lastImportIdx = content.lastIndexOf("import ");
    const nextNewline = content.indexOf("\n", lastImportIdx);
    return (
        content.slice(0, nextNewline) +
        '\nimport * as grants from "../grants";' +
        content.slice(nextNewline)
    );
}

function ensureNameProperty(content) {
    if (content.includes("__name")) return content;

    const pulumiTypePattern = /public static readonly __pulumiType = '[^']+';/;
    const match = content.match(pulumiTypePattern);
    if (match) {
        const idx = content.indexOf(match[0]) + match[0].length;
        content =
            content.slice(0, idx) +
            "\n\n    /** @internal Logical resource name for grant policy naming. */\n    private __name: string;" +
            content.slice(idx);
    }

    const superPattern = /super\([^)]*\);/;
    const superMatch = content.match(superPattern);
    if (superMatch) {
        const idx = content.indexOf(superMatch[0]) + superMatch[0].length;
        content =
            content.slice(0, idx) +
            "\n        this.__name = name;" +
            content.slice(idx);
    }

    return content;
}

function patchTSFile(file, grantConfig, grantTargetConfig) {
    const filePath = path.join(tsDir, file);

    if (!fs.existsSync(filePath)) {
        console.log(`  ⚠ ${file} not found — skipping`);
        return;
    }

    let content = fs.readFileSync(filePath, "utf8");

    const hasGrantMethods =
        content.includes("grantRead(") || content.includes("grantInvoke(");
    const hasGrantTarget = content.includes("grantName():");

    if (hasGrantMethods && (!grantTargetConfig || hasGrantTarget)) {
        console.log(`  ⏭ ${file} already patched — skipping`);
        return;
    }

    content = ensureGrantsImport(content);
    content = ensureNameProperty(content);

    let methods = "";

    if (grantConfig && !hasGrantMethods) {
        methods += grantConfig.grants
            .map((grant) => generateTSGrantMethod(grantConfig, grant))
            .join("\n");
    }

    if (grantTargetConfig && !hasGrantTarget) {
        methods += generateTSGrantTargetMethods(grantTargetConfig.roleArnProperty);
    }

    if (methods) {
        const classEnd = findClassEnd(content);
        if (classEnd >= 0) {
            content =
                content.slice(0, classEnd) +
                "\n" +
                methods +
                "\n" +
                content.slice(classEnd);
        }
    }

    fs.writeFileSync(filePath, content);

    const patched = [];
    if (grantConfig && !hasGrantMethods) {
        patched.push(grantConfig.grants.map((g) => g.method).join(", "));
    }
    if (grantTargetConfig && !hasGrantTarget) {
        patched.push("GrantTarget (grantName, grantRoleArn)");
    }
    console.log(`  ✔ Patched ${file} → ${patched.join(", ")}`);
}

// ════════════════════════════════════════════════════════════
// Python Patching
// ════════════════════════════════════════════════════════════

function generatePyGrantMethod(config, grant) {
    const { arnProperty, supportsPaths } = config;
    const { method, actions, isFullAccess } = grant;

    const pyMethod = toSnakeCase(method);
    const suffix = grantSuffix(method);
    const actionsStr = actions.map((a) => `"${a}"`).join(", ");

    if (isFullAccess) {
        return `
    def ${pyMethod}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants full access on this resource. Prefer scoped grants."""
        if not opts or not opts.justification:
            pulumi.log.warn(
                f"⚠ {self._name} → {target.grant_name()}: full access granted with no justification.",
                self,
            )
        else:
            pulumi.log.info(
                f"ℹ {self._name} → {target.grant_name()}: full access granted. Justification: \\"{opts.justification}\\"",
                self,
            )
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${arnProperty}, None)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
    }

    if (supportsPaths) {
        return `
    def ${pyMethod}(self, target: "grants.GrantTarget", paths: Optional[list] = None, opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${arnProperty}, paths)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
    }

    return `
    def ${pyMethod}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${suffix} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${suffix}"
        arns = grants.build_resource_arns(self.${arnProperty}, None)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
}

function generatePyGrantTargetMethods(pyRoleArnProperty) {
    return `
    def grant_name(self) -> str:
        """Implements GrantTarget — returns the logical resource name."""
        return self._name

    def grant_role_arn(self):
        """Implements GrantTarget — returns the IAM execution role ARN."""
        return self.${pyRoleArnProperty}
`;
}

function ensurePyGrantsImport(content) {
    if (content.includes("from anvil_cloud import grants")) return content;

    const lines = content.split("\n");
    let lastImportIdx = 0;
    for (let i = 0; i < lines.length; i++) {
        if (lines[i].startsWith("import ") || lines[i].startsWith("from ")) {
            lastImportIdx = i;
        }
    }
    lines.splice(lastImportIdx + 1, 0, "from typing import Optional", "from anvil_cloud import grants", "");
    return lines.join("\n");
}

function patchPyFile(file, grantConfig, grantTargetConfig) {
    const filePath = path.join(pyDir, file);

    if (!fs.existsSync(filePath)) {
        console.log(`  ⚠ ${file} not found — skipping`);
        return;
    }

    let content = fs.readFileSync(filePath, "utf8");

    const hasGrantMethods =
        content.includes("grant_read") || content.includes("grant_invoke");
    const hasGrantTarget = content.includes("grant_name");

    if (hasGrantMethods && (!grantTargetConfig || hasGrantTarget)) {
        console.log(`  ⏭ ${file} already patched — skipping`);
        return;
    }

    if (!hasGrantMethods && !hasGrantTarget) {
        content = ensurePyGrantsImport(content);
    }

    let methods = "";

    if (grantConfig && !hasGrantMethods) {
        methods += grantConfig.grants
            .map((grant) => generatePyGrantMethod(grantConfig, grant))
            .join("");
    }

    if (grantTargetConfig && !hasGrantTarget) {
        methods += generatePyGrantTargetMethods(grantTargetConfig.pyRoleArnProperty);
    }

    if (methods) {
        content = content.trimEnd() + "\n" + methods + "\n";
    }

    fs.writeFileSync(filePath, content);

    const patched = [];
    if (grantConfig && !hasGrantMethods) {
        patched.push(grantConfig.grants.map((g) => toSnakeCase(g.method)).join(", "));
    }
    if (grantTargetConfig && !hasGrantTarget) {
        patched.push("GrantTarget (grant_name, grant_role_arn)");
    }
    console.log(`  ✔ Patched ${file} → ${patched.join(", ")}`);
}

// ════════════════════════════════════════════════════════════
// Main
// ════════════════════════════════════════════════════════════

// Build per-file patch maps
const tsFileMap = {};
const pyFileMap = {};

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
    console.log("🔐 Patching TypeScript SDK with grant methods...");
    for (const [file, configs] of Object.entries(tsFileMap)) {
        patchTSFile(file, configs.grantConfig, configs.grantTargetConfig);
    }
    console.log("✔ TypeScript grant patching complete\n");
}

if (doPython) {
    console.log("🔐 Patching Python SDK with grant methods...");
    for (const [file, configs] of Object.entries(pyFileMap)) {
        patchPyFile(file, configs.grantConfig, configs.grantTargetConfig);
    }
    console.log("✔ Python grant patching complete");
}
