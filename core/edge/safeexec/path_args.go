package safeexec

import (
	"fmt"
	"path/filepath"
	"strings"
)

func ValidateArgPaths(args []string, baseDir string, allowedPrefixes []string) error {
	if len(allowedPrefixes) == 0 {
		return nil
	}
	for _, arg := range args {
		for _, candidate := range pathCandidates(arg) {
			if err := validateArgPath(candidate, baseDir, allowedPrefixes); err != nil {
				return err
			}
		}
	}
	return nil
}

func pathCandidates(arg string) []string {
	if strings.TrimSpace(arg) == "" || strings.Contains(arg, "://") {
		return nil
	}
	if key, value, ok := strings.Cut(arg, "="); ok {
		if strings.HasPrefix(key, "-") && pathLike(value) {
			return []string{value}
		}
		return nil
	}
	if strings.HasPrefix(arg, "-") || !pathLike(arg) {
		return nil
	}
	return []string{arg}
}

func validateArgPath(raw, baseDir string, allowedPrefixes []string) error {
	if strings.ContainsRune(raw, '\x00') {
		return fmt.Errorf("safeexec: argv path contains NUL byte")
	}
	if containsTraversal(raw) {
		return fmt.Errorf("safeexec: argv path traversal rejected: %s", raw)
	}
	path := filepath.Clean(raw)
	if !filepath.IsAbs(path) {
		base := strings.TrimSpace(baseDir)
		if base == "" {
			var err error
			base, err = filepath.Abs(".")
			if err != nil {
				return fmt.Errorf("safeexec: normalize cwd for argv path: %w", err)
			}
		}
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("safeexec: normalize argv path: %w", err)
	}
	abs = evalPathIfPossible(abs)
	if err := requireAllowedPrefix(abs, allowedPrefixes); err != nil {
		return fmt.Errorf("safeexec: argv path outside allowed prefixes: %s", raw)
	}
	return nil
}

func pathLike(value string) bool {
	value = strings.TrimSpace(value)
	return filepath.IsAbs(value) || strings.ContainsAny(value, `/\`)
}
