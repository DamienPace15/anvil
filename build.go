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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
		"build":            targetBuild,
		"binary":           targetBinary,
		"generate":         targetGenerate,
		"gen-site-schemas": targetGenSiteSchemas,
		"merge":            targetMerge,
		"registry":         targetRegistry,
		"gen-go-sdk":       targetGenGoSDK,
		"gen-nodejs":       targetGenNodejs,
		"gen-python-sdk":   targetGenPythonSDK,
		"build-provider":   targetBuildProvider,
		"build-sdk":        targetBuildSDK,
		"install":          targetInstall,
		"build-python-sdk": targetBuildPythonSDK,
		"install-py":       targetInstallPy,
		"publish-npm":      targetPublishNpm,
		"publish-pypi":     targetPublishPypi,
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
	run("provider", env("GOWORK", "off"), "go", "run", "../scripts/registry/generate_registry.go")
}

func targetGenGoSDK() {
	targetMerge()

	backupDir := filepath.Join(os.TempDir(), "anvil-sdk-backup", "go")
	must(os.MkdirAll(backupDir, 0o755))
	defer os.RemoveAll(backupDir)

	backupFiles("sdk/go/anvil", backupDir, "go.mod", "go.sum", "app.go", "block.go", "grants.go")

	run("provider", env("GOWORK", "off"),
		"pulumi", "package", "gen-sdk", "schema.json", "--language", "go", "--out", "../sdk",
	)

	if err := copyFile(filepath.Join(backupDir, "go.mod"), "sdk/go/anvil/go.mod"); err != nil {
		run("sdk/go/anvil", env("GOWORK", "off"),
			"go", "mod", "init", "github.com/DamienPace15/anvil/sdk/go/anvil",
		)
	}

	restoreFiles(backupDir, "sdk/go/anvil", "go.sum", "app.go", "block.go", "grants.go")

	must(copyFile("docs/go/README.md", "sdk/go/anvil/README.md"))

	run("sdk/go/anvil", env("GOWORK", "off"), "go", "mod", "tidy")
}

func targetBuildProvider() {
	targetGenGoSDK()
	targetRegistry()
	must(os.MkdirAll("bin", 0o755))
	run("provider", nil, "go", "build", "-o", "../bin/pulumi-resource-anvil", "./cmd/anvil/")
}

func targetGenNodejs() {
	targetMerge()

	backupDir := filepath.Join(os.TempDir(), "anvil-sdk-backup", "nodejs")
	must(os.MkdirAll(backupDir, 0o755))
	defer os.RemoveAll(backupDir)

	backupFiles("sdk/nodejs", backupDir, "app.ts", "block.ts", "grants.ts")

	run("provider", nil,
		"pulumi", "package", "gen-sdk", "schema.json", "--language", "nodejs", "--out", "../sdk",
	)

	restoreFiles(backupDir, "sdk/nodejs", "app.ts", "block.ts", "grants.ts")

	run(".", nil, "npx", "ts-node", "scripts/sdk/fix-sdk.ts", "--ts")
	run(".", nil, "npx", "ts-node", "scripts/grants/fix-sdk-grants.ts", "--ts")
}

func targetBuildSDK() {
	targetGenNodejs()
	run("sdk/nodejs", nil, "npm", "install")
	run("sdk/nodejs", nil, "npm", "run", "build")
	must(copyFile("docs/nodejs/README.md", "sdk/nodejs/README.md"))
}

func targetGenPythonSDK() {
	targetMerge()

	backupDir := filepath.Join(os.TempDir(), "anvil-sdk-backup", "python")
	must(os.MkdirAll(backupDir, 0o755))
	defer os.RemoveAll(backupDir)

	backupFiles("sdk/python/anvil_cloud", backupDir, "app.py", "block.py", "types.py", "grants.py")

	run("provider", nil,
		"pulumi", "package", "gen-sdk", "schema.json", "--language", "python", "--out", "../sdk",
	)

	restoreFiles(backupDir, "sdk/python/anvil_cloud", "app.py", "block.py", "types.py", "grants.py")

	run(".", nil, "npx", "ts-node", "scripts/sdk/fix-sdk.ts", "--python")
	run(".", nil, "npx", "ts-node", "scripts/grants/fix-sdk-grants.ts", "--python")
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

func backupFiles(srcDir, dstDir string, files ...string) {
	for _, f := range files {
		src := filepath.Join(srcDir, f)
		dst := filepath.Join(dstDir, f)
		if err := copyFile(src, dst); err != nil && !os.IsNotExist(err) {
			fatal("backup failed for %s: %v", f, err)
		}
	}
}

func restoreFiles(srcDir, dstDir string, files ...string) {
	for _, f := range files {
		src := filepath.Join(srcDir, f)
		dst := filepath.Join(dstDir, f)
		if err := copyFile(src, dst); err != nil && !os.IsNotExist(err) {
			fatal("restore failed for %s: %v", f, err)
		}
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
