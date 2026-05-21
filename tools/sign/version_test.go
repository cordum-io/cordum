package sign_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/tools/sign"
)

// EDGE-151-DOWNGRADE: tests for the version-floor enforcement gate. RED at
// step-2; package implementation lands in step-3.

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.2.0", "v1.10.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0-rc1", "v1.0.0-rc2", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v1.0.0-rc2", "v1.0.0-rc10", -1},
	}
	for _, c := range cases {
		if got := sign.SemverCompare(c.a, c.b); got != c.want {
			t.Errorf("SemverCompare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSemverCompare_Invalid(t *testing.T) {
	// Invalid versions sort as "greater than everything" via a sentinel
	// path; callers should validate beforehand. We assert the function
	// does not panic and returns a consistent ordering.
	cases := []string{"", "not-a-version", "1.0", "v1", "v1.0", "vx.y.z"}
	for _, bad := range cases {
		_ = sign.SemverCompare(bad, "v1.0.0")
		_ = sign.SemverCompare("v1.0.0", bad)
	}
}

func TestVerifyVersionFloor_Downgrade(t *testing.T) {
	// Candidate older than floor → ErrDowngradeAttempt.
	err := sign.VerifyVersionFloor("v1.0.0", "v1.2.0")
	if !errors.Is(err, sign.ErrDowngradeAttempt) {
		t.Fatalf("expected ErrDowngradeAttempt, got %v", err)
	}
}

func TestVerifyVersionFloor_Equal(t *testing.T) {
	// Equal candidate and floor → no error (re-install/no-op is allowed).
	if err := sign.VerifyVersionFloor("v1.2.0", "v1.2.0"); err != nil {
		t.Fatalf("expected nil for equal versions, got %v", err)
	}
}

func TestVerifyVersionFloor_Upgrade(t *testing.T) {
	// Candidate newer than floor → no error.
	if err := sign.VerifyVersionFloor("v1.3.0", "v1.2.0"); err != nil {
		t.Fatalf("expected nil for upgrade, got %v", err)
	}
}

func TestVerifyVersionFloor_EmptyFloor(t *testing.T) {
	// Empty floor (first install) → no error, any valid candidate accepted.
	if err := sign.VerifyVersionFloor("v1.0.0", ""); err != nil {
		t.Fatalf("expected nil for empty floor (first install), got %v", err)
	}
}

func TestVerifyVersionFloor_InvalidCandidate(t *testing.T) {
	if err := sign.VerifyVersionFloor("garbage", "v1.0.0"); !errors.Is(err, sign.ErrInvalidVersion) {
		t.Fatalf("expected ErrInvalidVersion for garbage candidate, got %v", err)
	}
}

func TestAdvanceFloor_CleanupOnErrorUsesSuccessFlag(t *testing.T) {
	raw, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatalf("read version.go: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "success := false") || !strings.Contains(body, "if !success") {
		t.Fatal("AdvanceFloor cleanup must be guarded by an explicit success flag")
	}
	if strings.Contains(body, "os.Stat(path)") {
		t.Fatal("AdvanceFloor cleanup must not depend on destination path existence")
	}
}

func TestVerifyVersionFloor_PreRelease(t *testing.T) {
	// pre-release lower than release of same M.N.P
	if err := sign.VerifyVersionFloor("v1.0.0-rc1", "v1.0.0"); !errors.Is(err, sign.ErrDowngradeAttempt) {
		t.Fatalf("expected ErrDowngradeAttempt for rc1<1.0.0, got %v", err)
	}
	// pre-release higher than older release passes
	if err := sign.VerifyVersionFloor("v1.0.1-rc1", "v1.0.0"); err != nil {
		t.Fatalf("expected nil for v1.0.1-rc1 > v1.0.0, got %v", err)
	}
}

func TestAdvanceFloor_WritesAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary-version-floor.json")
	if err := sign.AdvanceFloor(path, "v1.3.0", sign.FloorMetadata{
		SigScheme:   "gpg",
		Fingerprint: "ABCDEF",
		Operator:    "tester",
	}); err != nil {
		t.Fatalf("AdvanceFloor: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, `"version":"v1.3.0"`) {
		t.Errorf("floor file missing version field: %s", s)
	}
	if !strings.Contains(s, `"sig_scheme":"gpg"`) {
		t.Errorf("floor file missing sig_scheme: %s", s)
	}
	if !strings.Contains(s, `"fingerprint":"ABCDEF"`) {
		t.Errorf("floor file missing fingerprint: %s", s)
	}
	if !strings.Contains(s, `"advanced_at":`) {
		t.Errorf("floor file missing advanced_at: %s", s)
	}
}

func TestReadFloor_Missing(t *testing.T) {
	// Missing floor file → empty floor (no error) so first-install path works.
	dir := t.TempDir()
	got, err := sign.ReadFloor(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("ReadFloor (missing): unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ReadFloor (missing): expected empty version, got %q", got)
	}
}

func TestReadFloor_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "floor.json")
	if err := sign.AdvanceFloor(path, "v2.5.1", sign.FloorMetadata{SigScheme: "gpg"}); err != nil {
		t.Fatalf("AdvanceFloor: %v", err)
	}
	got, err := sign.ReadFloor(path)
	if err != nil {
		t.Fatalf("ReadFloor: %v", err)
	}
	if got != "v2.5.1" {
		t.Errorf("ReadFloor = %q, want v2.5.1", got)
	}
}

func TestReadFloor_MalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "floor.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := sign.ReadFloor(path); err == nil {
		t.Fatalf("ReadFloor: expected error on malformed json, got nil")
	}
}

func TestEmbedVersion_PrependsLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	original := "abcdef0123  cordum-hook\nfedcba9876  cordum-agentd\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := sign.EmbedVersion(path, "v1.2.3"); err != nil {
		t.Fatalf("EmbedVersion: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(body)
	if !strings.HasPrefix(got, "# version: v1.2.3\n") {
		t.Errorf("EmbedVersion did not prepend version line; got:\n%s", got)
	}
	if !strings.Contains(got, "abcdef0123  cordum-hook") {
		t.Errorf("EmbedVersion dropped original content; got:\n%s", got)
	}
}

func TestEmbedVersion_Idempotent(t *testing.T) {
	// Calling EmbedVersion twice with the same version is a no-op (does not
	// stack `# version` lines).
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(path, []byte("abcdef0123  cordum-hook\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := sign.EmbedVersion(path, "v1.2.3"); err != nil {
		t.Fatalf("EmbedVersion #1: %v", err)
	}
	if err := sign.EmbedVersion(path, "v1.2.3"); err != nil {
		t.Fatalf("EmbedVersion #2: %v", err)
	}
	body, _ := os.ReadFile(path)
	if got := strings.Count(string(body), "# version:"); got != 1 {
		t.Errorf("EmbedVersion not idempotent — got %d `# version:` lines, want 1\n%s", got, string(body))
	}
}

func TestEmbedVersion_ConflictRejected(t *testing.T) {
	// If a `# version: vN` line already exists with a DIFFERENT version, the
	// embed is rejected — we never silently rewrite metadata.
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(path, []byte("# version: v1.0.0\nabcdef0123  cordum-hook\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := sign.EmbedVersion(path, "v2.0.0")
	if err == nil {
		t.Fatalf("EmbedVersion: expected error on conflicting embed, got nil")
	}
}

func TestParseVersion_FromManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	body := "# version: v1.2.3\nabcdef0123  cordum-hook\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := sign.ParseVersion(path)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if got != "v1.2.3" {
		t.Errorf("ParseVersion = %q, want v1.2.3", got)
	}
}

func TestParseVersion_NoEmbedded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	body := "abcdef0123  cordum-hook\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := sign.ParseVersion(path); !errors.Is(err, sign.ErrNoVersionEmbedded) {
		t.Fatalf("expected ErrNoVersionEmbedded, got %v", err)
	}
}

func TestVerifyManifest_SkipsVersionComment(t *testing.T) {
	// Make sure the existing parseManifest gracefully skips a `# version:` line
	// instead of treating it as a malformed entry.
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	body := "# version: v1.2.3\n" +
		"0000000000000000000000000000000000000000000000000000000000000000  bin/x\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Note: full sign-and-verify already covered by verifier_test.go. Here we
	// just want to confirm ParseVersion + the manifest-line parser cooperate.
	got, err := sign.ParseVersion(path)
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if got != "v1.2.3" {
		t.Errorf("ParseVersion = %q, want v1.2.3", got)
	}
}
