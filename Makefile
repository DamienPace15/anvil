# ── Publish ─────────────────────────────────────────────────

publish-npm: build-sdk
	cd sdk/nodejs && npm publish --access public

publish-go: gen-go-sdk
	git add sdk/go/
	git diff --cached --quiet sdk/go/ || git commit -m "chore: update generated go sdk"
	git push origion master
	git tag sdk/go/anvil/$(VERSION)
	git push origion sdk/go/anvil/$(VERSION)

publish-pypi: build-python-sdk
	cd sdk/python && .venv/bin/twine upload dist/*

# make publish-go VERSION=vx.x.x.