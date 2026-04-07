package bundler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// HashDirectory computes a deterministic SHA256 hash of all files
// in a directory tree. Files are sorted by path before hashing so
// the result is stable across platforms and OS directory orderings.
//
// Returns the hex-encoded hash string.
func HashDirectory(dir string) (string, error) {
	h := sha256.New()

	// Collect all file paths first so we can sort them
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip directories — only hash file contents
		if info.IsDir() {
			return nil
		}
		// Skip node_modules and .anvil — not part of source
		rel, _ := filepath.Rel(dir, path)
		if isExcluded(rel) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to walk directory %s: %w", dir, err)
	}

	sort.Strings(paths)

	for _, path := range paths {
		// Hash the relative path so renames are detected
		rel, _ := filepath.Rel(dir, path)
		h.Write([]byte(rel))

		// Hash the file contents
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("failed to open %s: %w", path, err)
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", fmt.Errorf("failed to hash %s: %w", path, err)
		}
		f.Close()
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadCachedHash reads the stored hash for a function from disk.
// Returns empty string if no cache exists.
func ReadCachedHash(functionName string) string {
	hashFile := hashFilePath(functionName)
	data, err := os.ReadFile(hashFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteCachedHash writes the current hash for a function to disk.
func WriteCachedHash(functionName, hash string) error {
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("failed to create build dir: %w", err)
	}
	return os.WriteFile(hashFilePath(functionName), []byte(hash), 0644)
}

// NeedsRebuild returns true if the source directory has changed since
// the last build, or if no cached hash exists.
func NeedsRebuild(functionName, sourceDir string) (bool, error) {
	cached := ReadCachedHash(functionName)
	if cached == "" {
		return true, nil
	}

	current, err := HashDirectory(sourceDir)
	if err != nil {
		return true, err
	}

	return current != cached, nil
}

// ── Helpers ───────────────────────────────────────────────

const buildDir = ".anvil/build"

func hashFilePath(functionName string) string {
	return filepath.Join(buildDir, functionName+".hash")
}

// ZipFilePath returns the expected zip output path for a function.
func ZipFilePath(functionName string) string {
	return filepath.Join(buildDir, functionName+".zip")
}

// isExcluded returns true for paths that should be skipped during hashing.
func isExcluded(rel string) bool {
	excluded := []string{"node_modules", ".anvil", ".git", "dist", "build"}
	for _, ex := range excluded {
		if len(rel) >= len(ex) && rel[:len(ex)] == ex {
			return true
		}
	}
	return false
}
