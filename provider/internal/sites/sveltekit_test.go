package sites

import (
	"os"
	"path/filepath"
	"testing"
)

// setupTestProject creates a minimal SvelteKit project structure for testing.
// Returns the temp directory path and a cleanup function.
func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// svelte.config.js
	writeFile(t, filepath.Join(dir, "svelte.config.js"),
		`import adapter from '@sveltejs/adapter-node';
export default { kit: { adapter: adapter() } };`)

	// package.json with adapter-node
	writeFile(t, filepath.Join(dir, "package.json"),
		`{
  "name": "test-sveltekit-app",
  "devDependencies": {
    "@sveltejs/adapter-node": "^2.0.0",
    "@sveltejs/kit": "^2.0.0"
  },
  "scripts": {
    "build": "echo 'mock build'"
  }
}`)

	return dir
}

// setupBuildOutput creates a fake adapter-node build output.
func setupBuildOutput(t *testing.T, siteDir string) {
	t.Helper()
	buildDir := filepath.Join(siteDir, "build")
	mkdirAll(t, filepath.Join(buildDir, "client"))
	mkdirAll(t, filepath.Join(buildDir, "server"))
	writeFile(t, filepath.Join(buildDir, "server", "index.js"), `// server entry`)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

// --- Detection tests ---

func TestFindSvelteConfig_JS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.js"), "export default {};")

	path, err := findSvelteConfig(dir)
	if err != nil {
		t.Fatalf("expected config found, got error: %v", err)
	}
	if filepath.Base(path) != "svelte.config.js" {
		t.Errorf("expected svelte.config.js, got %s", filepath.Base(path))
	}
}

func TestFindSvelteConfig_TS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.ts"), "export default {};")

	path, err := findSvelteConfig(dir)
	if err != nil {
		t.Fatalf("expected config found, got error: %v", err)
	}
	if filepath.Base(path) != "svelte.config.ts" {
		t.Errorf("expected svelte.config.ts, got %s", filepath.Base(path))
	}
}

func TestFindSvelteConfig_MJS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.mjs"), "export default {};")

	path, err := findSvelteConfig(dir)
	if err != nil {
		t.Fatalf("expected config found, got error: %v", err)
	}
	if filepath.Base(path) != "svelte.config.mjs" {
		t.Errorf("expected svelte.config.mjs, got %s", filepath.Base(path))
	}
}

func TestFindSvelteConfig_Missing(t *testing.T) {
	dir := t.TempDir()

	_, err := findSvelteConfig(dir)
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
	if !containsAll(err.Error(), "no svelte.config.js found", "npx sv create") {
		t.Errorf("error should mention missing config and how to create one, got: %s", err.Error())
	}
}

func TestFindSvelteConfig_PrefersJS(t *testing.T) {
	// If both .js and .ts exist, .js should win (first in candidate list).
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.js"), "export default {};")
	writeFile(t, filepath.Join(dir, "svelte.config.ts"), "export default {};")

	path, err := findSvelteConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(path) != "svelte.config.js" {
		t.Errorf("expected svelte.config.js to be preferred, got %s", filepath.Base(path))
	}
}

// --- Adapter detection tests ---

func TestCheckAdapterNode_InDevDependencies(t *testing.T) {
	dir := setupTestProject(t)

	configPath := filepath.Join(dir, "svelte.config.js")
	err := checkAdapterNode(dir, configPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCheckAdapterNode_InDependencies(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.js"),
		`import adapter from '@sveltejs/adapter-node';
export default { kit: { adapter: adapter() } };`)
	writeFile(t, filepath.Join(dir, "package.json"),
		`{"dependencies": {"@sveltejs/adapter-node": "^2.0.0"}}`)

	err := checkAdapterNode(dir, filepath.Join(dir, "svelte.config.js"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCheckAdapterNode_NotConfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.js"),
		`import adapter from '@sveltejs/adapter-auto';
export default { kit: { adapter: adapter() } };`)
	writeFile(t, filepath.Join(dir, "package.json"),
		`{"devDependencies": {"@sveltejs/adapter-auto": "^2.0.0"}}`)

	err := checkAdapterNode(dir, filepath.Join(dir, "svelte.config.js"))
	if err == nil {
		t.Fatal("expected error for missing adapter-node")
	}
	if !containsAll(err.Error(), "adapter-node not detected", "npm install @sveltejs/adapter-node") {
		t.Errorf("error should explain how to fix, got: %s", err.Error())
	}
}

func TestCheckAdapterNode_MissingPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svelte.config.js"), "export default {};")

	err := checkAdapterNode(dir, filepath.Join(dir, "svelte.config.js"))
	if err == nil {
		t.Fatal("expected error for missing package.json")
	}
	if !containsAll(err.Error(), "no package.json found") {
		t.Errorf("error should mention missing package.json, got: %s", err.Error())
	}
}

func TestCheckAdapterNode_DepInstalledButNotInConfig(t *testing.T) {
	dir := t.TempDir()
	// Config doesn't reference adapter-node, but package.json has it.
	writeFile(t, filepath.Join(dir, "svelte.config.js"),
		`import adapter from '@sveltejs/adapter-auto';
export default { kit: { adapter: adapter() } };`)
	writeFile(t, filepath.Join(dir, "package.json"),
		`{"devDependencies": {"@sveltejs/adapter-node": "^2.0.0"}}`)

	// Should succeed (dep found) but print a warning to stderr.
	err := checkAdapterNode(dir, filepath.Join(dir, "svelte.config.js"))
	if err != nil {
		t.Fatalf("expected warning only (not error), got: %v", err)
	}
}

// --- Package dependency tests ---

func TestHasPackageDependency_DevDeps(t *testing.T) {
	data := []byte(`{"devDependencies": {"@sveltejs/adapter-node": "^2.0.0"}}`)
	if !hasPackageDependency(data, "@sveltejs/adapter-node") {
		t.Error("expected to find adapter-node in devDependencies")
	}
}

func TestHasPackageDependency_Deps(t *testing.T) {
	data := []byte(`{"dependencies": {"@sveltejs/adapter-node": "^2.0.0"}}`)
	if !hasPackageDependency(data, "@sveltejs/adapter-node") {
		t.Error("expected to find adapter-node in dependencies")
	}
}

func TestHasPackageDependency_NotPresent(t *testing.T) {
	data := []byte(`{"devDependencies": {"@sveltejs/adapter-auto": "^2.0.0"}}`)
	if hasPackageDependency(data, "@sveltejs/adapter-node") {
		t.Error("should not find adapter-node")
	}
}

func TestHasPackageDependency_InvalidJSON(t *testing.T) {
	data := []byte(`not json`)
	if hasPackageDependency(data, "@sveltejs/adapter-node") {
		t.Error("should return false on invalid JSON")
	}
}

// --- Node.js check tests ---

func TestCheckNodeInstalled(t *testing.T) {
	// This test passes if Node.js is on PATH, fails if not.
	// In CI or dev environments where Node.js is expected, this validates the check.
	err := checkNodeInstalled()
	if err != nil {
		t.Skipf("Node.js not installed, skipping: %v", err)
	}
}

// --- Build output parsing tests ---

func TestParseBuildOutput_Valid(t *testing.T) {
	dir := t.TempDir()
	setupBuildOutput(t, dir)

	result, err := parseBuildOutput(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedClient := filepath.Join(dir, "build", "client")
	expectedServer := filepath.Join(dir, "build", "server")
	expectedEntry := filepath.Join(dir, "build", "server", "index.js")

	if result.StaticDir != expectedClient {
		t.Errorf("StaticDir = %s, want %s", result.StaticDir, expectedClient)
	}
	if result.ServerDir != expectedServer {
		t.Errorf("ServerDir = %s, want %s", result.ServerDir, expectedServer)
	}
	if result.ServerEntry != expectedEntry {
		t.Errorf("ServerEntry = %s, want %s", result.ServerEntry, expectedEntry)
	}
}

func TestParseBuildOutput_NoBuildDir(t *testing.T) {
	dir := t.TempDir()

	_, err := parseBuildOutput(dir)
	if err == nil {
		t.Fatal("expected error for missing build directory")
	}
	if !containsAll(err.Error(), "build output not found", "adapter-node") {
		t.Errorf("error should explain missing build dir, got: %s", err.Error())
	}
}

func TestParseBuildOutput_NoClientDir(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "build", "server"))
	writeFile(t, filepath.Join(dir, "build", "server", "index.js"), "// entry")

	_, err := parseBuildOutput(dir)
	if err == nil {
		t.Fatal("expected error for missing client directory")
	}
	if !containsAll(err.Error(), "static assets directory not found") {
		t.Errorf("error should mention missing client dir, got: %s", err.Error())
	}
}

func TestParseBuildOutput_NoServerDir(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "build", "client"))

	_, err := parseBuildOutput(dir)
	if err == nil {
		t.Fatal("expected error for missing server directory")
	}
	if !containsAll(err.Error(), "server directory not found") {
		t.Errorf("error should mention missing server dir, got: %s", err.Error())
	}
}

func TestParseBuildOutput_NoServerEntry(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "build", "client"))
	mkdirAll(t, filepath.Join(dir, "build", "server"))
	// No index.js

	_, err := parseBuildOutput(dir)
	if err == nil {
		t.Fatal("expected error for missing server entry point")
	}
	if !containsAll(err.Error(), "server entry point not found") {
		t.Errorf("error should mention missing entry point, got: %s", err.Error())
	}
}

// --- Path resolution tests ---

func TestBuildSvelteKit_RelativePath(t *testing.T) {
	root := t.TempDir()
	siteDir := filepath.Join(root, "frontend")
	os.MkdirAll(siteDir, 0755)

	writeFile(t, filepath.Join(siteDir, "svelte.config.js"),
		`import adapter from '@sveltejs/adapter-node';
export default { kit: { adapter: adapter() } };`)
	writeFile(t, filepath.Join(siteDir, "package.json"),
		`{"devDependencies": {"@sveltejs/adapter-node": "^2.0.0"}, "scripts": {"build": "echo mock"}}`)

	// Create node_modules so npm install is skipped.
	mkdirAll(t, filepath.Join(siteDir, "node_modules"))
	// Create build output so the full pipeline succeeds.
	setupBuildOutput(t, siteDir)

	result, err := BuildSvelteKit(BuildOptions{
		Path:        "frontend",
		ProjectRoot: root,
	})

	// If node/npm isn't available, skip gracefully.
	if err != nil {
		if containsAny(err.Error(), "Node.js is not installed", "npm is not installed") {
			t.Skipf("Node.js/npm not available: %v", err)
		}
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StaticDir != filepath.Join(siteDir, "build", "client") {
		t.Errorf("StaticDir resolved incorrectly: %s", result.StaticDir)
	}
}

func TestBuildSvelteKit_SiteDirNotFound(t *testing.T) {
	root := t.TempDir()

	_, err := BuildSvelteKit(BuildOptions{
		Path:        "nonexistent",
		ProjectRoot: root,
	})

	if err == nil {
		t.Fatal("expected error for missing site directory")
	}
	if !containsAll(err.Error(), "site directory not found", "relative to your project root") {
		t.Errorf("error should guide the user, got: %s", err.Error())
	}
}

// --- Helpers ---

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
