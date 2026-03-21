.PHONY: build clean generate merge registry gen-go-sdk gen-nodejs build-provider build-sdk publish-npm

# ── Full Build ──────────────────────────────────────────────

build: generate merge registry gen-go-sdk build-provider gen-nodejs build-sdk
	@echo "✅ Build complete"

# ── Schema Pipeline ─────────────────────────────────────────

generate:
	cd provider && go run ../scripts/generate/generate_schemas.go

merge: generate
	cd provider && go run ../scripts/merge/merge_schemas.go

registry: merge
	cd provider && go run ../scripts/registry/generate_registry.go

# ── Go SDK ──────────────────────────────────────────────────

gen-go-sdk: merge
	cd provider && pulumi package gen-sdk schema.json --language go --out ../sdk
	cd sdk/go/anvil && go mod init github.com/DamienPace15/anvil/sdk/go/anvil 2>/dev/null || true
	cd sdk/go/anvil && GOWORK=off go mod tidy

# ── Provider Binary ─────────────────────────────────────────

build-provider: gen-go-sdk registry
	cd provider && go build -o ../bin/pulumi-resource-anvil ./cmd/anvil/

# ── Node SDK ────────────────────────────────────────────────

gen-nodejs: merge
	cd provider && pulumi package gen-sdk schema.json --language nodejs --out ../sdk
	node scripts/fix-sdk-package.js

build-sdk: gen-nodejs
	cd sdk/nodejs && npm install && npm run build
	cp docs/nodejs/README.md sdk/nodejs/README.md

# ── Publish ─────────────────────────────────────────────────

publish-npm: build-sdk
	cd sdk/nodejs && npm publish --access public

# ── Clean ───────────────────────────────────────────────────

clean:
	rm -rf bin/pulumi-resource-anvil
	rm -rf sdk/nodejs/bin sdk/nodejs/node_modules
	rm -rf sdk/go