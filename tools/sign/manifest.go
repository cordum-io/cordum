package sign

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BuildManifest hashes each file with SHA-256 and emits a cosign-compatible
// SHA256SUMS-style manifest. Lines are sorted by the forward-slash relative
// path so identical inputs always yield byte-identical output regardless of
// host OS or filesystem ordering — the determinism is what lets the release
// pipeline compare a freshly-built manifest against the canonical artefact
// without spurious diffs. The hash format is `<lowercase-hex>  <relpath>\n`,
// matching the output of `sha256sum`.
//
// Each input path is read from disk; the path itself is recorded in the
// manifest after filepath.ToSlash normalisation, so callers should pass
// relative paths that already match the layout used at install time. Any
// I/O error is returned wrapped with the offending path for diagnosis.
func BuildManifest(paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return []byte{}, nil
	}
	type entry struct {
		rel  string
		hash string
	}
	entries := make([]entry, 0, len(paths))
	for _, p := range paths {
		rel := filepath.ToSlash(p)
		hash, err := hashFile(p)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{rel: rel, hash: hash})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s  %s\n", e.hash, e.rel)
	}
	return []byte(b.String()), nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p) //nolint:gosec // build-time release tooling
	if err != nil {
		return "", fmt.Errorf("sign: open %s: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("sign: hash %s: %w", p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
