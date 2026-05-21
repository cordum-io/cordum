package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	scaffoldDirPerm  = 0o750
	scaffoldFilePerm = 0o600
)

func ensureDir(path string) error {
	if path == "" {
		return fmt.Errorf("directory path required")
	}
	return os.MkdirAll(path, scaffoldDirPerm)
}

func writeFile(path, content string, force bool) error {
	if path == "" {
		return fmt.Errorf("file path required")
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("file exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), scaffoldFilePerm)
}

// readFileRaw reads a file and returns its contents.
func readFileRaw(path string) ([]byte, error) {
	// #nosec G304 -- CLI explicitly reads local files.
	return os.ReadFile(path)
}

// writeFileOverwrite overwrites a file regardless of existence.
func writeFileOverwrite(path, content string) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), scaffoldFilePerm)
}
