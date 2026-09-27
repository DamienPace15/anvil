package main

// Schema generation pipeline (stages 1–4 of scripts/README.md):
// per-component schemas → merged provider schema → generated provider main.go.

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
