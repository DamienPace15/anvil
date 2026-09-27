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

// targetClean removes build artifacts (provider binary, SDK build output).
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
