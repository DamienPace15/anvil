package main

import (
	"os"
	"path/filepath"
)

// The provider binary (pulumi-resource-anvil), the generated Go SDK, the DSQL
// bootstrap Lambda, and local install of the anvil CLI + provider.

func targetBinary() {
	run(".", nil, "go", "build", "-o", "anvil", "./cmd/anvil")
}

func targetGenGoSDK() {
	targetMerge()

	// gen-sdk wipes sdk/go/anvil and does NOT emit a go.mod, but go.work lists
	// that module so go.mod/go.sum must exist there at all times. They can't live
	// in the overlay (the workspace needs them at rest), so preserve just these
	// two module artifacts across the regen. The hand-written *.go sources are
	// safe in sdk/overlays/go and restored via copyDir below — no filename list.
	tmp := filepath.Join(os.TempDir(), "anvil-go-mod")
	must(os.MkdirAll(tmp, 0o755))
	defer os.RemoveAll(tmp)
	preserve(tmp, "sdk/go/anvil", "go.mod", "go.sum")

	run("provider", env("GOWORK", "off"),
		"pulumi", "package", "gen-sdk", "schema.json", "--language", "go", "--out", "../sdk",
	)

	preserve("sdk/go/anvil", tmp, "go.mod", "go.sum")

	// Copy the hand-written overlay (app.go, block.go, grants.go) into the
	// freshly generated package.
	copyDir("sdk/overlays/go", "sdk/go/anvil")

	must(copyFile("docs/go/README.md", "sdk/go/anvil/README.md"))

	run("sdk/go/anvil", env("GOWORK", "off"), "go", "mod", "tidy")
}

func targetBuildProvider() {
	targetGenGoSDK()
	targetRegistry()
	targetBuildDsqlLambda()
	must(os.MkdirAll("bin", 0o755))
	run("provider", nil, "go", "build", "-o", "../bin/pulumi-resource-anvil", "./cmd/anvil/")
	installProviderToPluginCache()
}

// installProviderToPluginCache copies the freshly built provider into the
// Pulumi plugin cache (~/.pulumi/plugins/resource-anvil-v{version}/).
// Pulumi resolves versioned providers from this cache BEFORE checking PATH —
// without this, a rebuilt provider is ignored and the stale cached copy is used.
// The version comes from provider/base-schema.json (readVersion) — the same
// source the registry generator bakes into the provider binary — so the cache
// dir always matches the version the provider reports to Pulumi.
func installProviderToPluginCache() {
	home, err := os.UserHomeDir()
	if err != nil {
		log("⚠ could not resolve home dir, skipping plugin cache install: %v", err)
		return
	}
	cacheDir := filepath.Join(home, ".pulumi", "plugins", "resource-anvil-v"+readVersion())
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		log("⚠ could not create plugin cache dir: %v", err)
		return
	}
	dst := filepath.Join(cacheDir, "pulumi-resource-anvil")
	if err := copyFile("bin/pulumi-resource-anvil", dst); err != nil {
		log("⚠ could not install provider to plugin cache: %v", err)
		return
	}
	os.Chmod(dst, 0o755)
	log("✅ Installed provider to plugin cache → %s", dst)
}

// ── DSQL Lambda ─────────────────────────────────────────────────────────────

func targetBuildDsqlLambda() {
	embedDir := filepath.Join("provider", "aws", "dsql", "bootstrap")
	must(os.MkdirAll(embedDir, 0o755))

	binaryPath := filepath.Join(embedDir, "bootstrap")

	// Cross-compile for Lambda (linux/arm64, custom runtime)
	// GOWORK=off because dsql-lambda is a separate Go module.
	run("cmd/anvil/dsql-lambda",
		[]string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOWORK=off"},
		"go", "build", "-tags", "lambda.norpc",
		"-o", filepath.Join("..", "..", "..", binaryPath), ".",
	)

	// Zip the binary as "bootstrap" (required name for provided.al2023 runtime)
	run(embedDir, nil, "zip", "-j", "dsql-lambda.zip", "bootstrap")

	// Clean up the raw binary
	remove(binaryPath)

	log("✅ DSQL bootstrap Lambda built → %s/dsql-lambda.zip", embedDir)
}

func targetInstall() {
	targetBuildProvider()
	targetBuildDsqlLambda()
	run(".", nil, "go", "build", "-o", "anvil", "./cmd/anvil")

	installDir := os.Getenv("ANVIL_INSTALL_DIR")
	if installDir == "" {
		installDir = "/usr/local/bin"
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal("could not determine working directory: %v", err)
	}

	for _, binary := range []struct{ src, name string }{
		{filepath.Join(cwd, "anvil"), "anvil"},
		{filepath.Join(cwd, "bin", "pulumi-resource-anvil"), "pulumi-resource-anvil"},
	} {
		dst := filepath.Join(installDir, binary.name)
		log("▶ installing %s → %s", binary.src, dst)
		if err := copyFile(binary.src, dst); err != nil {
			fatal("failed to install %s: %v\n\nTry: sudo go run ./build install", binary.name, err)
		}
		os.Chmod(dst, 0755)
	}

	log("✅ Installed anvil + pulumi-resource-anvil to %s", installDir)
}
