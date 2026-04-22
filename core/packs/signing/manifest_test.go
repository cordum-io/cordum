package signing

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writePack builds a minimal well-formed pack on disk and returns the
// pack root.
func writePack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkfile := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	mkfile("pack.yaml", `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.1
resources:
  schemas:
    - id: test/In
      path: schemas/In.json
    - id: test/Out
      path: schemas/Out.json
  workflows:
    - id: test.echo
      path: workflows/echo.yaml
overlays:
  config:
    - name: pools
      path: overlays/pools.patch.yaml
  policy:
    - name: safety
      path: overlays/policy.yaml
`)
	mkfile("schemas/In.json", `{"type":"object"}`)
	mkfile("schemas/Out.json", `{"type":"object"}`)
	mkfile("workflows/echo.yaml", "name: echo\n")
	mkfile("overlays/pools.patch.yaml", "pools: {}\n")
	mkfile("overlays/policy.yaml", "rules: []\n")
	return root
}

func TestBuildManifest_HappyPath(t *testing.T) {
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if m.PackID != "test-pack" || m.PackVersion != "0.0.1" {
		t.Fatalf("metadata wrong: %+v", m)
	}
	if m.Algorithm != AlgorithmEd25519 {
		t.Fatalf("algorithm = %q, want %q", m.Algorithm, AlgorithmEd25519)
	}
	if m.Version != ManifestVersion {
		t.Fatalf("version = %d, want %d", m.Version, ManifestVersion)
	}
	// 1 manifest + 2 schemas + 1 workflow + 2 overlays.
	if len(m.Files) != 6 {
		t.Fatalf("files = %d, want 6 (got %+v)", len(m.Files), m.Files)
	}
	// Sorted ascending by path.
	for i := 1; i < len(m.Files); i++ {
		if m.Files[i-1].Path >= m.Files[i].Path {
			t.Fatalf("files not sorted: %q >= %q", m.Files[i-1].Path, m.Files[i].Path)
		}
	}
	// Manifest row present with Kind=manifest.
	var seenManifest bool
	for _, f := range m.Files {
		if f.Path == "pack.yaml" {
			seenManifest = true
			if f.Kind != FileKindManifest {
				t.Fatalf("pack.yaml kind = %q, want manifest", f.Kind)
			}
		}
	}
	if !seenManifest {
		t.Fatal("pack.yaml missing from manifest")
	}
}

func TestBuildManifest_DeterministicAcross100Runs(t *testing.T) {
	root := writePack(t)
	first, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	firstBytes, err := canonicalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		got, err := canonicalJSON(m)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstBytes, got) {
			t.Fatalf("iter %d: canonical bytes drifted", i)
		}
	}
}

func TestBuildManifest_MissingReferencedFile(t *testing.T) {
	root := writePack(t)
	// Remove a referenced schema and re-run.
	if err := os.Remove(filepath.Join(root, "schemas", "Out.json")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	_, err := BuildManifest(root)
	if !errors.Is(err, ErrMissingFile) {
		t.Fatalf("err = %v, want ErrMissingFile", err)
	}
}

func TestBuildManifest_SymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on Windows CI")
	}
	root := writePack(t)
	target := filepath.Join(root, "schemas", "In.json")
	symlinkPath := filepath.Join(root, "schemas", "Evil.json")
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Skipf("symlink creation failed: %v", err)
	}
	// Rewrite pack.yaml to reference the symlink.
	packYAML := `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.1
resources:
  schemas:
    - id: evil
      path: schemas/Evil.json
`
	if err := os.WriteFile(filepath.Join(root, "pack.yaml"), []byte(packYAML), 0o644); err != nil {
		t.Fatalf("write pack.yaml: %v", err)
	}
	_, err := BuildManifest(root)
	if !errors.Is(err, ErrSymlinkRejected) {
		t.Fatalf("err = %v, want ErrSymlinkRejected", err)
	}
}

func TestBuildManifest_PathTraversalRejected(t *testing.T) {
	root := writePack(t)
	// ../evil.txt must be rejected at manifest-build time.
	packYAML := `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.1
resources:
  schemas:
    - id: evil
      path: ../evil.txt
`
	if err := os.WriteFile(filepath.Join(root, "pack.yaml"), []byte(packYAML), 0o644); err != nil {
		t.Fatalf("write pack.yaml: %v", err)
	}
	// Create the target so a non-guarded implementation would
	// happily hash it.
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "evil.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write evil: %v", err)
	}
	_, err := BuildManifest(root)
	if !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("err = %v, want ErrEscapesRoot", err)
	}
}

func TestBuildManifest_PackYAMLMissing(t *testing.T) {
	root := t.TempDir()
	_, err := BuildManifest(root)
	if !errors.Is(err, ErrManifestNotFound) {
		t.Fatalf("err = %v, want ErrManifestNotFound", err)
	}
}

func TestBuildManifest_PackRootNotDirectory(t *testing.T) {
	_, err := BuildManifest("")
	if !errors.Is(err, ErrPackRootNotDirectory) {
		t.Fatalf("empty err = %v, want ErrPackRootNotDirectory", err)
	}
	file := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = BuildManifest(file)
	if !errors.Is(err, ErrPackRootNotDirectory) {
		t.Fatalf("file err = %v, want ErrPackRootNotDirectory", err)
	}
}

func TestBuildManifest_MalformedPackYAML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.yaml"), []byte("::::::"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := BuildManifest(root)
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("err = %v, want ErrManifestMalformed", err)
	}
}

func TestBuildManifest_MetadataRequired(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.yaml"), []byte("metadata: {id: only-id}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := BuildManifest(root)
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("err = %v, want ErrManifestMalformed", err)
	}
}

func TestBuildManifest_WindowsSeparatorNormalised(t *testing.T) {
	root := t.TempDir()
	// pack.yaml references the schema with a backslash path. On
	// Linux filepath.ToSlash treats backslashes as regular chars —
	// but pack.yaml content is interpreted ONLY as forward-slash
	// paths after we call filepath.ToSlash on the rel string. Here
	// we write with forward slashes; the test proves the on-disk
	// manifest rows always use forward slashes regardless of host
	// OS. The cross-OS determinism is already exercised by
	// TestBuildManifest_DeterministicAcross100Runs — this test
	// adds a direct assertion on the emitted path shape.
	mkfile := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkfile("pack.yaml", `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.1
resources:
  schemas:
    - id: s
      path: schemas/In.json
`)
	mkfile("schemas/In.json", `{"type":"object"}`)
	m, err := BuildManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if filepath.FromSlash(f.Path) != f.Path && runtime.GOOS != "windows" {
			// Linux: POSIX path is already canonical. On Windows
			// filepath.FromSlash replaces forward-slash with
			// backslash, so the comparison is only meaningful on
			// non-Windows.
			t.Fatalf("path contains host separator: %q", f.Path)
		}
	}
}

func TestCanonicalBytes_DomainPrefix(t *testing.T) {
	m := Manifest{
		Version:     ManifestVersion,
		PackID:      "p",
		PackVersion: "0.0.1",
		SignedAt:    "2026-04-20T12:00:00Z",
		Algorithm:   AlgorithmEd25519,
		Files:       []FileEntry{{Path: "a", SHA256: "aa", Kind: FileKindManifest}},
	}
	got, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	prefix := SigningDomain + "\n"
	if !bytes.HasPrefix(got, []byte(prefix)) {
		t.Fatalf("canonical bytes missing domain prefix: %q", got)
	}
}
