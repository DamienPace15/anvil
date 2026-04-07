package bundler

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ZipFile creates a zip archive at destPath containing a single file
// with the given name and contents.
//
// Used for single-file esbuild output (index.js) — the entire bundle
// is one file so no directory walking is needed.
func ZipFile(destPath, fileName string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	entry, err := w.Create(fileName)
	if err != nil {
		return fmt.Errorf("failed to create zip entry: %w", err)
	}

	if _, err := entry.Write(contents); err != nil {
		return fmt.Errorf("failed to write zip entry: %w", err)
	}

	return nil
}

// ZipDirectory creates a zip archive at destPath containing all files
// in sourceDir, preserving relative paths.
//
// Used when bundling produces multiple output files.
func ZipDirectory(destPath, sourceDir string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		entry, err := w.Create(rel)
		if err != nil {
			return fmt.Errorf("failed to create zip entry for %s: %w", rel, err)
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open %s: %w", path, err)
		}
		defer src.Close()

		if _, err := io.Copy(entry, src); err != nil {
			return fmt.Errorf("failed to write %s to zip: %w", rel, err)
		}

		return nil
	})
}
