.PHONY: build clean generate merge registry gen-go-sdk gen-nodejs gen-python-sdk build-provider build-sdk publish-npm publish-pypi

binary:
	go build -o anvil ./cmd/anvil

# ── Full Build ──────────────────────────────────────────────

build: generate merge registry gen-go-sdk build-provider gen-nodejs build-sdk gen-python-sdk
	@echo "✅ Build complete"

# ── Schema Pipeline ─────────────────────────────────────────
# GOWORK=off because go.work references sdk/go/anvil which may not
# have a go.mod yet (gen-sdk wipes it). These scripts don't need the SDK module.

generate:
	cd provider && GOWORK=off go run ../scripts/generate/generate_schemas.go

merge: generate
	cd provider && GOWORK=off go run ../scripts/merge/merge_schemas.go

registry: merge
	cd provider && GOWORK=off go run ../scripts/registry/generate_registry.go

# ── Go SDK ──────────────────────────────────────────────────
# pulumi gen-sdk wipes sdk/go/. Back up hand-written + module files, then restore.

gen-go-sdk: merge
	@mkdir -p /tmp/anvil-sdk-backup/go
	@cp sdk/go/anvil/go.mod sdk/go/anvil/go.sum /tmp/anvil-sdk-backup/go/ 2>/dev/null || true
	@for f in app.go block.go; do \
		cp sdk/go/anvil/$$f /tmp/anvil-sdk-backup/go/ 2>/dev/null || true; \
	done
	cd provider && GOWORK=off pulumi package gen-sdk schema.json --language go --out ../sdk
	@cp /tmp/anvil-sdk-backup/go/go.mod sdk/go/anvil/ 2>/dev/null || \
		(cd sdk/go/anvil && go mod init github.com/DamienPace15/anvil/sdk/go/anvil)
	@cp /tmp/anvil-sdk-backup/go/go.sum sdk/go/anvil/ 2>/dev/null || true
	@for f in app.go block.go; do \
		cp /tmp/anvil-sdk-backup/go/$$f sdk/go/anvil/ 2>/dev/null || true; \
	done
	@rm -rf /tmp/anvil-sdk-backup/go
	cd sdk/go/anvil && GOWORK=off go mod tidy

# ── Provider Binary ─────────────────────────────────────────
# This one CAN use go.work — by this point gen-go-sdk has restored go.mod

build-provider: gen-go-sdk registry
	cd provider && go build -o ../bin/pulumi-resource-anvil ./cmd/anvil/

# ── Node SDK ────────────────────────────────────────────────

gen-nodejs: merge
	@mkdir -p /tmp/anvil-sdk-backup/nodejs
	@for f in app.ts block.ts; do \
		cp sdk/nodejs/$$f /tmp/anvil-sdk-backup/nodejs/ 2>/dev/null || true; \
	done
	cd provider && pulumi package gen-sdk schema.json --language nodejs --out ../sdk
	@for f in app.ts block.ts; do \
		cp /tmp/anvil-sdk-backup/nodejs/$$f sdk/nodejs/ 2>/dev/null || true; \
	done
	@rm -rf /tmp/anvil-sdk-backup/nodejs
	node scripts/fix-sdk-package.js

build-sdk: gen-nodejs
	cd sdk/nodejs && npm install && npm run build
	cp docs/nodejs/README.md sdk/nodejs/README.md

# ── Python SDK ──────────────────────────────────────────────

gen-python-sdk: merge
	@mkdir -p /tmp/anvil-sdk-backup/python
	@for f in app.py block.py; do \
		cp sdk/python/anvil_cloud/$$f /tmp/anvil-sdk-backup/python/ 2>/dev/null || true; \
	done
	cd provider && pulumi package gen-sdk schema.json --language python --out ../sdk
	@for f in app.py block.py; do \
		cp /tmp/anvil-sdk-backup/python/$$f sdk/python/anvil_cloud/ 2>/dev/null || true; \
	done
	@rm -rf /tmp/anvil-sdk-backup/python
	node scripts/fix-sdk-python.js

build-python-sdk: gen-python-sdk
	python3 -m venv sdk/python/.venv
	sdk/python/.venv/bin/pip install build twine
	cd sdk/python && .venv/bin/python -m build

# ── Publish ─────────────────────────────────────────────────

publish-npm: build-sdk
	cd sdk/nodejs && npm publish --access public

publish-go: gen-go-sdk
	git add sdk/go/
	git commit -m "chore: update generated go sdk"
	git push origion master
	git tag sdk/go/anvil/$(VERSION)
	git push origion sdk/go/anvil/$(VERSION)

publish-pypi: build-python-sdk
	cd sdk/python && .venv/bin/twine upload dist/*

# make publish-go VERSION=vx.x.x.

# ── Clean ───────────────────────────────────────────────────

clean:
	rm -rf bin/pulumi-resource-anvil
	rm -rf sdk/nodejs/bin sdk/nodejs/node_modules
	rm -rf sdk/python/dist sdk/python/build sdk/python/*.egg-info sdk/python/.venv