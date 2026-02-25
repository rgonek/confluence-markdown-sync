package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve working directory: %v\n", err)
		os.Exit(1)
	}

	var unformatted []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			switch d.Name() {
			case ".git", ".github", "workspace", "test-output", "vendor":
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		formatted, fmtErr := format.Source(raw)
		if fmtErr != nil {
			return fmt.Errorf("format %s: %w", path, fmtErr)
		}

		if !bytes.Equal(raw, formatted) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			unformatted = append(unformatted, filepath.ToSlash(rel))
		}

		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gofmt check failed: %v\n", err)
		os.Exit(1)
	}

	if len(unformatted) > 0 {
		fmt.Fprintln(os.Stderr, "The following files are not gofmt-formatted:")
		fmt.Fprintln(os.Stderr, strings.Join(unformatted, "\n"))
		os.Exit(1)
	}
}
