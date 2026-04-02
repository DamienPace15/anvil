// Package sites defines the shared types used across all framework build pipelines.
// Framework-specific build logic lives in subdirectories (sveltekit/, astro/, etc).
// Provider-specific infra wiring lives in provider/internal/awssite, provider/internal/gcpsite, etc.
package sites

// BuildResult is the structured output of a framework build.
// Provider-specific components consume this to deploy static assets
// to object storage (S3/Cloud Storage) and server code to compute (Lambda/Cloud Run).
type BuildResult struct {
	// StaticDir is the absolute path to the directory containing static/client assets.
	// These get uploaded to object storage and served via CDN.
	StaticDir string

	// ServerDir is the absolute path to the directory containing the Node.js server bundle.
	// This gets packaged and deployed to a compute service.
	ServerDir string

	// ServerEntry is the absolute path to the Node.js server entry point (e.g. index.js).
	// The compute service uses this as its handler.
	ServerEntry string

	// Framework identifies which framework produced this build result.
	// Used by the infra layer to make framework-specific decisions
	// (e.g. CloudFront cache path patterns).
	// Values: "sveltekit" | "astro" | "nuxt" | "remix"
	Framework string
}
