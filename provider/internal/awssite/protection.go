package awssite

import "fmt"

// Protection modes for a site's server Lambda Function URL.
//
// The design, and every user-facing message, is recorded in
// docs/design/site-protection.md. Keep its "Messages Anvil emits" table in sync
// with the message text here.
const (
	// ProtectionNone leaves the Function URL public (AuthType NONE). Anyone who
	// learns the URL can invoke the function directly, bypassing CloudFront.
	ProtectionNone = "none"

	// ProtectionOAC locks the Function URL to the site's CloudFront distribution
	// (AuthType AWS_IAM + Lambda OAC). Requests with a body are rejected unless
	// they already carry an x-amz-content-sha256 header.
	ProtectionOAC = "oac"

	// ProtectionEdgeOAC is ProtectionOAC plus a Lambda@Edge function that adds
	// x-amz-content-sha256 at the edge, per AWS's guidance — the app adds
	// nothing. The default while a WAF is attached or origin protection is
	// enabled.
	ProtectionEdgeOAC = "edge-oac"
)

// ProtectionResult is the outcome of ResolveProtection.
type ProtectionResult struct {
	// Mode is the effective protection mode.
	Mode string
	// Notice is non-empty when Mode was applied by default (info).
	Notice string
	// Warning is non-empty when an explicit "none" leaves a WAF or proxy
	// bypassable.
	Warning string
}

// ResolveProtection returns the effective protection mode for a site.
//
// mode is the site's protection input. hasWAF is true when the waf block is
// set; hasOriginProtection when originProtection is enabled.
//
// An explicit mode always wins. When it's unset, the default is ProtectionNone,
// or ProtectionEdgeOAC while a WAF or origin protection is on — a public
// Function URL would let anyone go around them, and edge-oac is the locked mode
// that needs nothing from the app.
func ResolveProtection(mode string, hasWAF, hasOriginProtection bool) (ProtectionResult, error) {
	switch mode {
	case "", ProtectionNone, ProtectionOAC, ProtectionEdgeOAC:
	default:
		return ProtectionResult{}, fmt.Errorf("invalid protection %q: must be %q, %q or %q",
			mode, ProtectionNone, ProtectionOAC, ProtectionEdgeOAC)
	}

	guarded := hasWAF || hasOriginProtection
	reason := "a WAF is attached"
	switch {
	case hasWAF && hasOriginProtection:
		reason = "a WAF is attached and origin protection is enabled"
	case hasOriginProtection:
		reason = "origin protection is enabled"
	}

	switch {
	case mode == "" && !guarded:
		return ProtectionResult{Mode: ProtectionNone}, nil
	case mode == "":
		return ProtectionResult{
			Mode: ProtectionEdgeOAC,
			Notice: fmt.Sprintf("Because %s, protection is set to %q: the server Function URL is locked to CloudFront, "+
				"and a Lambda@Edge function adds the x-amz-content-sha256 header to requests with a body. "+
				"Request bodies over 1 MB are rejected. Set protection explicitly to choose another mode.",
				reason, ProtectionEdgeOAC),
		}, nil
	case mode == ProtectionNone && guarded:
		return ProtectionResult{
			Mode: ProtectionNone,
			Warning: fmt.Sprintf("protection is %q while %s: the server Function URL is public, "+
				"so anyone who learns it can go around CloudFront and bypass it.", ProtectionNone, reason),
		}, nil
	default:
		return ProtectionResult{Mode: mode}, nil
	}
}

// IsIAMProtected reports whether the resolved mode locks the Function URL with AWS_IAM.
func IsIAMProtected(mode string) bool {
	return mode == ProtectionOAC || mode == ProtectionEdgeOAC
}

// UsesEdgeSigner reports whether the resolved mode needs the Lambda@Edge signer.
func UsesEdgeSigner(mode string) bool {
	return mode == ProtectionEdgeOAC
}
