package main

// The TypeScript and Python SDKs: gen-sdk → copy overlay → fix-sdk + grants →
// build. See scripts/sdk/README.md and scripts/grants/README.md.

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
