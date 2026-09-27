// Command build is Anvil's build tool. Run from the repo root:
//
//	go run ./build [target] [KEY=VALUE ...]
//
// Targets: build, binary, generate, gen-site-schemas, merge, registry,
//          gen-go-sdk, gen-nodejs, gen-python-sdk, build-provider, build-sdk,
//          build-python-sdk, install, install-py, build-dsql-lambda, clean,
//          publish-npm, publish-pypi, publish-go
//
// Examples:
//	go run ./build build
//	go run ./build build-sdk
//	go run ./build publish-go VERSION=v0.1.0
//
// The tool is split across files by concern:
//   main.go      — target dispatch + the top-level `build` orchestration
//   schema.go    — schema generation (generate, merge, registry, …)
//   provider.go  — the provider binary + Go SDK + DSQL lambda + install
//   sdk.go       — the TypeScript and Python SDKs
//   deploy.go    — publishing (npm, PyPI, Go) — see also .github/workflows/release.yml
//   helpers.go   — shared helpers (run, copy, version, clean, …)
package main

import (
	"fmt"
	"os"
	"strings"
)

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
		"build":             targetBuild,
		"binary":            targetBinary,
		"generate":          targetGenerate,
		"gen-site-schemas":  targetGenSiteSchemas,
		"merge":             targetMerge,
		"registry":          targetRegistry,
		"gen-go-sdk":        targetGenGoSDK,
		"gen-nodejs":        targetGenNodejs,
		"gen-python-sdk":    targetGenPythonSDK,
		"build-provider":    targetBuildProvider,
		"build-sdk":         targetBuildSDK,
		"install":           targetInstall,
		"build-python-sdk":  targetBuildPythonSDK,
		"install-py":        targetInstallPy,
		"build-dsql-lambda": targetBuildDsqlLambda,
		"publish-npm":       targetPublishNpm,
		"publish-pypi":      targetPublishPypi,
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

// targetBuild runs the full pipeline: schema → provider → all three SDKs.
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
