// scripts/fix-sdk-grants.js
//
// Patches the auto-generated SDK classes to add grant methods.
// Runs after pulumi gen-sdk and before build.
//
// This script injects:
// 1. Grant methods on infra resources (e.g. Bucket.grantRead)
// 2. GrantTarget implementation on compute resources (e.g. Lambda.grantName, Lambda.grantRoleArn)
//
// Each resource defines its own grant config (actions per access level).
// The injected methods delegate to the shared createGrant/buildResourceArns
// helpers in grants.ts.

const fs = require("fs");
const path = require("path");

const sdkDir = path.join(__dirname, "..", "sdk", "nodejs");

// ── Grant Definitions ──────────────────────────────────────

const GRANT_CONFIGS = [
    {
        file: "aws/bucket.ts",
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
        file: "aws/lambda.ts",
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
        file: "aws/lambda.ts",
        className: "Lambda",
        roleArnProperty: "roleArn",
    },
];

// ── Code Generation ────────────────────────────────────────

function generateGrantMethod(config, grant) {
    const { className, arnProperty, supportsPaths } = config;
    const { method, actions, isFullAccess } = grant;
    const actionsStr = actions.map((a) => `"${a}"`).join(", ");

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
        const name = \`\${this.__name}-\${target.grantName()}-fullaccess\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, undefined);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
    }

    if (supportsPaths) {
        return `
    /**
     * Grants ${method.replace("grant", "").toLowerCase()} access (${actions.join(", ")}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * @param target - The compute resource to grant access to.
     * @param paths - Optional array of path prefixes to scope access (e.g. ["uploads/*"]).
     * @param opts - Optional grant options (justification for audit trail).
     */
    public ${method}(target: grants.GrantTarget, paths?: string[], opts?: grants.GrantOptions): void {
        const name = \`\${this.__name}-\${target.grantName()}-${method.replace("grant", "").toLowerCase()}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, paths);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
    }

    return `
    /**
     * Grants ${method.replace("grant", "").toLowerCase()} access (${actions.join(", ")}) on this ${className.toLowerCase()}
     * to the target compute resource's execution role.
     *
     * @param target - The compute resource to grant access to.
     * @param opts - Optional grant options (justification for audit trail).
     */
    public ${method}(target: grants.GrantTarget, opts?: grants.GrantOptions): void {
        const name = \`\${this.__name}-\${target.grantName()}-${method.replace("grant", "").toLowerCase()}\`;
        const arns = grants.buildResourceArns(this.${arnProperty}, undefined);
        grants.createGrant(this, name, target, [${actionsStr}], arns, opts);
    }`;
}

function generateGrantTargetMethods(roleArnProperty) {
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

// ── Helpers ────────────────────────────────────────────────

/**
 * Finds the closing brace of the exported class.
 * Looks for "export interface" which always follows the class,
 * then walks backward to find the class closing brace.
 */
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

// ── Main ───────────────────────────────────────────────────
// Single pass per file. Collects all patches, applies them together.

console.log("🔐 Patching SDK with grant methods...");

// Build file → patches map
const fileMap = {};
for (const config of GRANT_CONFIGS) {
    fileMap[config.file] = fileMap[config.file] || {};
    fileMap[config.file].grantConfig = config;
}
for (const config of GRANT_TARGET_CONFIGS) {
    fileMap[config.file] = fileMap[config.file] || {};
    fileMap[config.file].grantTargetConfig = config;
}

for (const [file, configs] of Object.entries(fileMap)) {
    const filePath = path.join(sdkDir, file);

    if (!fs.existsSync(filePath)) {
        console.log(`  ⚠ ${file} not found — skipping`);
        continue;
    }

    let content = fs.readFileSync(filePath, "utf8");

    const hasGrantMethods =
        content.includes("grantRead(") || content.includes("grantInvoke(");
    const hasGrantTarget = content.includes("grantName():");

    if (hasGrantMethods && (!configs.grantTargetConfig || hasGrantTarget)) {
        console.log(`  ⏭ ${file} already patched — skipping`);
        continue;
    }

    // Apply all patches
    content = ensureGrantsImport(content);
    content = ensureNameProperty(content);

    // Collect methods to inject
    let methods = "";

    if (configs.grantConfig && !hasGrantMethods) {
        methods += configs.grantConfig.grants
            .map((grant) => generateGrantMethod(configs.grantConfig, grant))
            .join("\n");
    }

    if (configs.grantTargetConfig && !hasGrantTarget) {
        methods += generateGrantTargetMethods(
            configs.grantTargetConfig.roleArnProperty,
        );
    }

    // Insert before class closing brace
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
    if (configs.grantConfig && !hasGrantMethods) {
        patched.push(configs.grantConfig.grants.map((g) => g.method).join(", "));
    }
    if (configs.grantTargetConfig && !hasGrantTarget) {
        patched.push("GrantTarget (grantName, grantRoleArn)");
    }
    console.log(`  ✔ Patched ${file} → ${patched.join(", ")}`);
}

console.log("✔ Grant patching complete");