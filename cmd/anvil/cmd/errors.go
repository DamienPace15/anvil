package cmd

import (
	"strings"
)

// mapError rewrites common Pulumi/cloud errors into Anvil-branded messages.
// Returns the rewritten message, or the original if no mapping applies.
func mapError(msg string) string {
	lower := strings.ToLower(msg)

	// AWS credentials
	if containsAny(lower, "no valid credential", "failed to load credentials",
		"unable to locate credentials", "security token included in the request is invalid",
		"invalidclienttokenid", "expired token", "signaturedoesnotmatch") {
		return "AWS credentials not found or expired.\n    Run `aws configure` or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY."
	}

	// AWS permissions
	if containsAny(lower, "accessdenied", "access denied", "not authorized",
		"unauthorized", "forbidden") {
		return rewriteAccessDenied(msg)
	}

	// Stack locked / concurrent update
	if containsAny(lower, "stack is locked", "conflict: another update",
		"concurrent update", "already being updated") {
		return "This stage is locked by another operation.\n    If this is stale, run `anvil unlock --yes --stage <name>` to release it."
	}

	// Backend unreachable
	if containsAny(lower, "failed to create backend", "backend unreachable",
		"no such file or directory", "failed to open bucket") {
		return "Could not reach state storage.\n    Check that your backend bucket exists and ANVIL_BACKEND_URL is correct."
	}

	// Plugin / provider not found
	if containsAny(lower, "no resource plugin", "failed to load plugin",
		"plugin not found", "pulumi-resource-") && containsAny(lower, "not found", "no such") {
		return "A required provider plugin could not be found.\n    Make sure pulumi-resource-anvil is on your PATH."
	}

	// Project not found
	if containsAny(lower, "pulumi.yaml", "no pulumi project found", "could not find") &&
		containsAny(lower, "project", "pulumi.yaml") {
		return "Could not find an Anvil project in this directory.\n    Make sure you're in a directory with a Pulumi.yaml file."
	}

	return msg
}

// rewriteAccessDenied tries to extract the useful part of an access denied error
// and strips Pulumi terminology.
func rewriteAccessDenied(msg string) string {
	// Keep the original message but prefix with a clearer explanation
	cleaned := strings.ReplaceAll(msg, "pulumi:", "")
	cleaned = strings.TrimSpace(cleaned)
	return "Permission denied by your cloud provider.\n    " + cleaned
}

func containsAny(s string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
