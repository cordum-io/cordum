package signing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PackYAMLName is the fixed file name of the manifest at the root of
// every Cordum pack.
const PackYAMLName = "pack.yaml"

// packYAMLDoc is the subset of pack.yaml the walker cares about.
// Everything outside these fields is intentionally ignored.
type packYAMLDoc struct {
	Metadata struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Resources struct {
		Schemas []struct {
			Path string `yaml:"path"`
		} `yaml:"schemas"`
		Workflows []struct {
			Path string `yaml:"path"`
		} `yaml:"workflows"`
	} `yaml:"resources"`
	Overlays struct {
		Config []struct {
			Path string `yaml:"path"`
		} `yaml:"config"`
		Policy []struct {
			Path string `yaml:"path"`
		} `yaml:"policy"`
	} `yaml:"overlays"`
}

// BuildManifest walks packRoot and produces a canonical Manifest
// ready for signing. SignedAt is set to now (UTC, RFC3339) — callers
// that need a deterministic value can override the result's
// SignedAt. The caller picks the key id at sign time; the walker is
// oblivious.
func BuildManifest(packRoot string) (Manifest, error) {
	return buildManifestAt(packRoot, time.Now().UTC())
}

// BuildManifestWithClock is the deterministic variant of BuildManifest
// used by tests and reproducible builds — callers supply the SignedAt
// value directly.
func BuildManifestWithClock(packRoot string, signedAt time.Time) (Manifest, error) {
	return buildManifestAt(packRoot, signedAt.UTC())
}

func buildManifestAt(packRoot string, signedAt time.Time) (Manifest, error) {
	absRoot, err := resolvePackRoot(packRoot)
	if err != nil {
		return Manifest{}, err
	}

	manifestPath := filepath.Join(absRoot, PackYAMLName)
	manifestBytes, err := readRegularFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, fmt.Errorf("%w: %s", ErrManifestNotFound, manifestPath)
		}
		return Manifest{}, err
	}

	var doc packYAMLDoc
	if err := yaml.Unmarshal(manifestBytes, &doc); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifestMalformed, err)
	}
	packID := strings.TrimSpace(doc.Metadata.ID)
	packVersion := strings.TrimSpace(doc.Metadata.Version)
	if packID == "" || packVersion == "" {
		return Manifest{}, fmt.Errorf("%w: metadata.id and metadata.version are required", ErrManifestMalformed)
	}

	entries := make([]FileEntry, 0, 8)
	entries = append(entries, FileEntry{
		Path:      PackYAMLName,
		SHA256:    sha256Hex(manifestBytes),
		SizeBytes: int64(len(manifestBytes)),
		Kind:      FileKindManifest,
	})

	add := func(rel string, kind FileKind) error {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			return nil
		}
		entry, err := hashRelPath(absRoot, rel, kind)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	}

	for _, s := range doc.Resources.Schemas {
		if err := add(s.Path, FileKindSchema); err != nil {
			return Manifest{}, err
		}
	}
	for _, w := range doc.Resources.Workflows {
		if err := add(w.Path, FileKindWorkflow); err != nil {
			return Manifest{}, err
		}
	}
	for _, c := range doc.Overlays.Config {
		if err := add(c.Path, FileKindOverlay); err != nil {
			return Manifest{}, err
		}
	}
	for _, p := range doc.Overlays.Policy {
		if err := add(p.Path, FileKindOverlay); err != nil {
			return Manifest{}, err
		}
	}

	// De-duplicate on Path: if the same file is referenced by two
	// different pack.yaml entries we still sign it once. This keeps
	// the manifest stable under cosmetic rearrangements of
	// pack.yaml.
	entries = dedupeFiles(entries)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	return Manifest{
		Version:     ManifestVersion,
		PackID:      packID,
		PackVersion: packVersion,
		SignedAt:    signedAt.Format(time.RFC3339),
		Algorithm:   AlgorithmEd25519,
		Files:       entries,
	}, nil
}

// CanonicalBytes returns the byte string that gets signed. Callers
// normally do not need to call this directly — SignManifest /
// VerifyManifest use it internally — but it is exported so operators
// can reproduce the signing preimage for debugging.
func CanonicalBytes(m Manifest) ([]byte, error) {
	// JSON with sorted slice of files + no whitespace. Go's
	// encoding/json sorts map keys alphabetically and Files is a
	// slice we pre-sort in the builder, so marshalling is
	// deterministic.
	body, err := canonicalJSON(m)
	if err != nil {
		return nil, err
	}
	buf := bytes.Buffer{}
	buf.WriteString(SigningDomain)
	buf.WriteByte('\n')
	buf.Write(body)
	return buf.Bytes(), nil
}

func canonicalJSON(m Manifest) ([]byte, error) {
	out := Manifest{
		Version:     m.Version,
		PackID:      m.PackID,
		PackVersion: m.PackVersion,
		SignedAt:    m.SignedAt,
		Algorithm:   m.Algorithm,
		Files:       append([]FileEntry(nil), m.Files...),
	}
	sort.Slice(out.Files, func(i, j int) bool {
		return out.Files[i].Path < out.Files[j].Path
	})
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	// encoding/json appends a trailing newline; strip it for a
	// byte-stable preimage.
	body := buf.Bytes()
	if len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	return body, nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func resolvePackRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%w: empty path", ErrPackRootNotDirectory)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPackRootNotDirectory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrPackRootNotDirectory, root)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// readRegularFile loads the contents of path, rejecting symlinks and
// any non-regular file type. This is the security guardrail that
// stops a symlinked /etc/passwd from flowing into a signed manifest.
func readRegularFile(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrSymlinkRejected, p)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file: %s", ErrMissingFile, p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// hashRelPath resolves a pack.yaml-relative path to an absolute file,
// hashes it, and returns a FileEntry with a POSIX path.
func hashRelPath(absRoot, rel string, kind FileKind) (FileEntry, error) {
	// Normalise the path separator BEFORE absolute-path resolution
	// so a Windows-signed pack using "schemas\HelloInput.json"
	// yields the same manifest as a Linux-signed pack using
	// "schemas/HelloInput.json".
	posix := filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	if posix == "" {
		return FileEntry{}, fmt.Errorf("%w: empty referenced path", ErrManifestMalformed)
	}
	if strings.HasPrefix(posix, "/") {
		return FileEntry{}, fmt.Errorf("%w: absolute path %q", ErrEscapesRoot, posix)
	}
	abs := filepath.Join(absRoot, filepath.FromSlash(posix))
	absResolved, err := filepath.Abs(abs)
	if err != nil {
		return FileEntry{}, err
	}
	rootWithSep := absRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	if !strings.HasPrefix(absResolved+string(filepath.Separator), rootWithSep) && absResolved != absRoot {
		return FileEntry{}, fmt.Errorf("%w: %s", ErrEscapesRoot, posix)
	}
	raw, err := readRegularFile(absResolved)
	if err != nil {
		if os.IsNotExist(err) {
			return FileEntry{}, fmt.Errorf("%w: %s", ErrMissingFile, posix)
		}
		return FileEntry{}, err
	}
	return FileEntry{
		Path:      path.Clean(posix),
		SHA256:    sha256Hex(raw),
		SizeBytes: int64(len(raw)),
		Kind:      kind,
	}, nil
}

func dedupeFiles(in []FileEntry) []FileEntry {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]FileEntry, 0, len(in))
	for _, entry := range in {
		if _, ok := seen[entry.Path]; ok {
			continue
		}
		seen[entry.Path] = struct{}{}
		out = append(out, entry)
	}
	return out
}
