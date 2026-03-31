// scripts/fix-sdk-grants-python.js
//
// Patches the auto-generated Python SDK classes to add grant methods.
// Runs after pulumi gen-sdk and before build.
//
// Same concept as fix-sdk-grants.js but for Python.

const fs = require("fs");
const path = require("path");

const sdkDir = path.join(__dirname, "..", "sdk", "python", "anvil_cloud");

// ── Grant Definitions ──────────────────────────────────────

const GRANT_CONFIGS = [
    {
        file: "aws/bucket.py",
        className: "Bucket",
        arnProperty: "arn",
        supportsPaths: true,
        grants: [
            { method: "grant_read", actions: ["s3:GetObject", "s3:ListBucket"] },
            { method: "grant_write", actions: ["s3:PutObject"] },
            { method: "grant_read_write", actions: ["s3:GetObject", "s3:ListBucket", "s3:PutObject"] },
            { method: "grant_delete", actions: ["s3:DeleteObject"] },
            {
                method: "grant_full_access",
                actions: ["s3:GetObject", "s3:ListBucket", "s3:PutObject", "s3:DeleteObject"],
                isFullAccess: true,
            },
        ],
    },
    {
        file: "aws/lambda_.py",
        className: "Lambda",
        arnProperty: "arn",
        supportsPaths: false,
        grants: [
            { method: "grant_invoke", actions: ["lambda:InvokeFunction"] },
        ],
    },
];

const GRANT_TARGET_CONFIGS = [
    {
        file: "aws/lambda_.py",
        className: "Lambda",
        roleArnProperty: "role_arn",
    },
];

// ── Code Generation ────────────────────────────────────────

function generatePythonGrantMethod(config, grant) {
    const { arnProperty, supportsPaths } = config;
    const { method, actions, isFullAccess } = grant;

    const actionsStr = actions.map((a) => `"${a}"`).join(", ");

    if (isFullAccess) {
        return `
    def ${method}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
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
        name = f"{self._name}-{target.grant_name()}-fullaccess"
        arns = grants.build_resource_arns(self.${arnProperty}, None)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
    }

    if (supportsPaths) {
        return `
    def ${method}(self, target: "grants.GrantTarget", paths: Optional[list] = None, opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${method.replace("grant_", "")} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${method.replace("grant_", "")}"
        arns = grants.build_resource_arns(self.${arnProperty}, paths)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
    }

    return `
    def ${method}(self, target: "grants.GrantTarget", opts: Optional["grants.GrantOptions"] = None) -> None:
        """Grants ${method.replace("grant_", "")} access on this resource."""
        name = f"{self._name}-{target.grant_name()}-${method.replace("grant_", "")}"
        arns = grants.build_resource_arns(self.${arnProperty}, None)
        grants.create_grant(self, name, target, [${actionsStr}], arns, opts)
`;
}

// ── Patching Logic ─────────────────────────────────────────

function patchPythonFile(config) {
    const filePath = path.join(sdkDir, config.file);

    if (!fs.existsSync(filePath)) {
        console.log(`  ⚠ ${config.file} not found — skipping grants`);
        return;
    }

    let content = fs.readFileSync(filePath, "utf8");

    if (content.includes("grant_read") || content.includes("grant_invoke")) {
        console.log(`  ⏭ ${config.file} already has grant methods — skipping`);
        return;
    }

    // Add imports at the top
    const importLine = `from typing import Optional\nfrom anvil_cloud import grants\n`;
    if (!content.includes("from anvil_cloud import grants")) {
        // Insert after the last existing import block
        const lines = content.split("\n");
        let lastImportIdx = 0;
        for (let i = 0; i < lines.length; i++) {
            if (lines[i].startsWith("import ") || lines[i].startsWith("from ")) {
                lastImportIdx = i;
            }
        }
        lines.splice(lastImportIdx + 1, 0, importLine);
        content = lines.join("\n");
    }

    // Add grant methods before the last line of the class
    // Python classes end with the last method at the deepest indentation
    // We'll append methods at the end of the file (inside the class)
    const methods = config.grants
        .map((grant) => generatePythonGrantMethod(config, grant))
        .join("");

    // Find the class and append methods before the end
    // Simple approach: append before the very end of the file
    content = content.trimEnd() + "\n" + methods + "\n";

    fs.writeFileSync(filePath, content);
    console.log(
        `  ✔ Patched ${config.file} → added ${config.grants.map((g) => g.method).join(", ")}`,
    );
}

function patchPythonGrantTarget(config) {
    const filePath = path.join(sdkDir, config.file);

    if (!fs.existsSync(filePath)) {
        console.log(`  ⚠ ${config.file} not found — skipping GrantTarget`);
        return;
    }

    let content = fs.readFileSync(filePath, "utf8");

    if (content.includes("grant_name")) {
        console.log(`  ⏭ ${config.file} already implements GrantTarget — skipping`);
        return;
    }

    const targetMethods = `
    def grant_name(self) -> str:
        """Implements GrantTarget — returns the logical resource name."""
        return self._name

    def grant_role_arn(self):
        """Implements GrantTarget — returns the IAM execution role ARN."""
        return self.${config.roleArnProperty}
`;

    content = content.trimEnd() + "\n" + targetMethods + "\n";

    fs.writeFileSync(filePath, content);
    console.log(`  ✔ Patched ${config.file} → implements GrantTarget`);
}

// ── Main ───────────────────────────────────────────────────

console.log("🔐 Patching Python SDK with grant methods...");

for (const config of GRANT_CONFIGS) {
    patchPythonFile(config);
}

for (const config of GRANT_TARGET_CONFIGS) {
    patchPythonGrantTarget(config);
}

console.log("✔ Python grant patching complete");