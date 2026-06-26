//go:build ignore

// Run with: go run build.go [target]
// Targets: build, binary, generate, merge, registry, gen-go-sdk, gen-nodejs,
//          gen-python-sdk, build-provider, build-sdk, build-python-sdk,
//          install-py, publish-npm, publish-pypi, publish-go, clean
//
// Example: go run build.go build
//          go run build.go publish-go VERSION=v0.1.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// readVersion returns the single source-of-truth version from
// provider/base-schema.json. Every other version (provider binary, registry,
// nodejs/python/go SDK packages) is derived from this — bump it in one place.
func readVersion() string {
	data, err := os.ReadFile("provider/base-schema.json")
	if err != nil {
		fatal("could not read provider/base-schema.json for version: %v", err)
	}
	var schema struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		fatal("could not parse provider/base-schema.json: %v", err)
	}
	if schema.Version == "" {
		fatal("provider/base-schema.json has no \"version\" field")
	}
	return schema.Version
}

// ── Entry point ──────────────────────────────────────────────────────────────

func main() {
	target := "build"
	extra := map[string]string{}

	for _, arg := range os.Args[1:] {
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			extra[parts[0]] = parts[1]
		} else {
			target = arg
		}
	}

	targets := map[string]func(){
		"build":              targetBuild,
		"binary":             targetBinary,
		"generate":           targetGenerate,
		"gen-site-schemas":   targetGenSiteSchemas,
		"merge":              targetMerge,
		"registry":           targetRegistry,
		"gen-go-sdk":         targetGenGoSDK,
		"gen-nodejs":         targetGenNodejs,
		"gen-python-sdk":     targetGenPythonSDK,
		"build-provider":     targetBuildProvider,
		"build-sdk":          targetBuildSDK,
		"install":            targetInstall,
		"build-python-sdk":   targetBuildPythonSDK,
		"install-py":         targetInstallPy,
		"build-dsql-lambda":  targetBuildDsqlLambda,
		"publish-npm":        targetPublishNpm,
		"publish-pypi":       targetPublishPypi,
		"publish-go": func() {
			version, ok := extra["VERSION"]
			if !ok || version == "" {
				fatal("publish-go requires VERSION=vx.x.x")
			}
			targetPublishGo(version)
		},
		"clean": targetClean,
	}

	fn, ok := targets[target]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown target: %q\n\nAvailable targets:\n", target)
		for k := range targets {
			fmt.Fprintf(os.Stderr, "  %s\n", k)
		}
		os.Exit(1)
	}

	fn()
}

// ── Targets ──────────────────────────────────────────────────────────────────

func targetBuild() {
	targetGenerate()
	targetMerge()
	targetRegistry()
	targetGenGoSDK()
	targetBuildProvider()
	targetGenNodejs()
	targetBuildSDK()
	targetGenPythonSDK()
	log("✅ Build complete")
}

func targetBinary() {
	run(".", nil, "go", "build", "-o", "anvil", "./cmd/anvil")
}

func targetGenerate() {
	run("provider", env("GOWORK", "off"), "go", "run", "../scripts/generate/generate_schemas.go")
}

func targetGenSiteSchemas() {
	run("provider", nil, "go", "run", "../scripts/generate-site-schemas/main.go")
}

func targetMerge() {
	targetGenerate()
	targetGenSiteSchemas()
	run("provider", env("GOWORK", "off"), "go", "run", "../scripts/merge/merge_schemas.go")
}

func targetRegistry() {
	targetMerge()
	// Pass the single source-of-truth version so the generator bakes it into the
	// generated provider main.go (which reports it to Pulumi).
	run("provider", env("GOWORK", "off"), "go", "run", "../scripts/registry/generate_registry.go", readVersion())
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

func targetGenNodejs() {
	targetMerge()

	run("provider", nil,
		"pulumi", "package", "gen-sdk", "schema.json", "--language", "nodejs", "--out", "../sdk",
	)

	// Copy the hand-written overlay (app.ts, block.ts, grants.ts, stack.ts,
	// _extras.ts) into the freshly generated SDK before patching. Sources live
	// outside the gen-sdk output dir — no backup/restore needed.
	copyDir("sdk/overlays/nodejs", "sdk/nodejs")

	run(".", env("ANVIL_VERSION", readVersion()), "npx", "ts-node", "scripts/sdk/fix-sdk.ts", "--ts")
	run(".", nil, "npx", "ts-node", "scripts/grants/generate-grants.ts", "--ts")
}

func targetBuildSDK() {
	targetGenNodejs()
	run("sdk/nodejs", nil, "npm", "install")
	run("sdk/nodejs", nil, "npm", "run", "build")
	must(copyFile("docs/nodejs/README.md", "sdk/nodejs/README.md"))
}

func targetGenPythonSDK() {
	targetMerge()

	run("provider", nil,
		"pulumi", "package", "gen-sdk", "schema.json", "--language", "python", "--out", "../sdk",
	)

	// Copy the hand-written overlay (app.py, block.py, types.py, grants.py,
	// stack.py, _extras.py) into the freshly generated package before patching.
	// Sources live outside the gen-sdk output dir — no backup/restore needed.
	copyDir("sdk/overlays/python", "sdk/python/anvil_cloud")

	run(".", env("ANVIL_VERSION", readVersion()), "npx", "ts-node", "scripts/sdk/fix-sdk.ts", "--python")
	run(".", nil, "npx", "ts-node", "scripts/grants/generate-grants.ts", "--python")
}

func targetBuildPythonSDK() {
	targetGenPythonSDK()
	must(copyFile("docs/python/README.md", "sdk/python/README.md"))
	run(".", nil, "python3", "-m", "venv", "sdk/python/.venv")
	run(".", nil, "sdk/python/.venv/bin/pip", "install", "build", "twine")
	run("sdk/python", nil, ".venv/bin/python", "-m", "build")
}

func targetInstallPy() {
	run("test-app-python", nil, "pip", "install", "-e", "../../anvil-core.nosync/sdk/python/")
}

func targetPublishNpm() {
	targetBuildSDK()
	runInteractive("sdk/nodejs", nil, "npm", "publish", "--access", "public")
}

func targetPublishPypi() {
	targetBuildPythonSDK()
	runInteractive("sdk/python", nil, ".venv/bin/twine", "upload", "dist/*")
}

func targetPublishGo(version string) {
	targetGenGoSDK()
	run(".", nil, "git", "add", "sdk/go/")
	diffOut, _ := exec.Command("git", "diff", "--cached", "--quiet", "sdk/go/").CombinedOutput()
	if len(diffOut) > 0 {
		run(".", nil, "git", "commit", "-m", "chore: update generated go sdk")
	}
	run(".", nil, "git", "push", "origin", "master")
	run(".", nil, "git", "tag", "sdk/go/anvil/"+version)
	run(".", nil, "git", "push", "origin", "sdk/go/anvil/"+version)
}

func targetClean() {
	remove("bin/pulumi-resource-anvil")
	removeAll("sdk/nodejs/bin", "sdk/nodejs/node_modules")
	removeAll("sdk/python/dist", "sdk/python/build", "sdk/python/.venv")
	matches, _ := filepath.Glob("sdk/python/*.egg-info")
	for _, m := range matches {
		must(os.RemoveAll(m))
	}
	log("🧹 Clean complete")
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

// ── Helpers ──────────────────────────────────────────────────────────────────

// run executes a command piping stdout/stderr but NOT stdin.
// Use for all non-interactive commands.
func run(dir string, extraEnv []string, name string, args ...string) {
	log("▶ %s %s  (in %s)", name, strings.Join(args, " "), dir)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if err := cmd.Run(); err != nil {
		fatal("command failed: %v", err)
	}
}

// runInteractive is like run but also wires stdin so the terminal can handle
// interactive prompts (e.g. npm OTP, twine credentials).
func runInteractive(dir string, extraEnv []string, name string, args ...string) {
	log("▶ %s %s  (in %s)", name, strings.Join(args, " "), dir)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if err := cmd.Run(); err != nil {
		fatal("command failed: %v", err)
	}
}

func env(key, value string) []string { return []string{key + "=" + value} }

// preserve copies the named files from srcDir to dstDir, tolerating any that
// don't exist yet (e.g. go.sum before the first `go mod tidy`). Used only to
// carry the Go module artifacts across gen-sdk, which wipes its output dir.
func preserve(dstDir, srcDir string, files ...string) {
	for _, f := range files {
		if err := copyFile(filepath.Join(srcDir, f), filepath.Join(dstDir, f)); err != nil && !os.IsNotExist(err) {
			fatal("preserve %s: %v", f, err)
		}
	}
}

// copyDir copies every file under srcDir into dstDir, preserving the relative
// sub-path of each file. Used to overlay the hand-written SDK sources onto the
// freshly generated SDK. It copies whatever is in srcDir — there is no filename
// list to keep in sync, so a new overlay file is picked up automatically.
func copyDir(srcDir, dstDir string) {
	err := filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		return copyFile(p, filepath.Join(dstDir, rel))
	})
	if err != nil {
		fatal("copyDir %s → %s: %v", srcDir, dstDir, err)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	must(os.MkdirAll(filepath.Dir(dst), 0o755))
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func remove(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fatal("remove %s: %v", path, err)
	}
}

func removeAll(paths ...string) {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			fatal("removeAll %s: %v", p, err)
		}
	}
}

func must(err error) {
	if err != nil {
		fatal("%v", err)
	}
}

func log(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
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
			fatal("failed to install %s: %v\n\nTry: sudo go run build.go install", binary.name, err)
		}
		os.Chmod(dst, 0755)
	}

	log("✅ Installed anvil + pulumi-resource-anvil to %s", installDir)
}
