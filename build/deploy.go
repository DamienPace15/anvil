package main

import "os/exec"

// Deployment / publishing targets.
//
// PRIMARY path is CI on a published GitHub release — see
// .github/workflows/release.yml, which builds and publishes the npm, PyPI, and
// Go SDKs (npm + PyPI via OIDC trusted publishing, no stored tokens).
//
// The targets below remain as a MANUAL fallback (emergency / local publish).
// They are interactive where credentials are needed.
//
//	go run ./build publish-npm
//	go run ./build publish-pypi
//	go run ./build publish-go VERSION=v0.1.0

// targetPublishNpm builds the nodejs SDK and publishes it to npm.
// Manual fallback — CI publishes via OIDC on release.
func targetPublishNpm() {
	targetBuildSDK()
	runInteractive("sdk/nodejs", nil, "npm", "publish", "--access", "public")
}

// targetPublishPypi builds the Python SDK and uploads it to PyPI via twine.
// Manual fallback — CI publishes via OIDC trusted publishing on release.
func targetPublishPypi() {
	targetBuildPythonSDK()
	runInteractive("sdk/python", nil, ".venv/bin/twine", "upload", "dist/*")
}

// targetPublishGo regenerates the Go SDK, commits any change to it, and pushes
// the module tag (`sdk/go/anvil/<version>`) — which is how a Go module is
// "published". Manual fallback — CI tags the Go SDK on release.
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
