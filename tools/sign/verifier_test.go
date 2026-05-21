package sign_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/cordum/cordum/tools/sign"
)

// EDGE-151: Tests for the binary-integrity verifier package. All RED in step-2
// (package implementation lands in step-3). Tests sign manifests with ephemeral
// OpenPGP entities so they are self-contained and do not depend on
// tools/test-keys/* fixtures.

func newTestEntity(t *testing.T, name string) *openpgp.Entity {
	t.Helper()
	ent, err := openpgp.NewEntity(name, "TEST-ONLY", name+"@cordum.example", nil)
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	return ent
}

func entityFingerprint(ent *openpgp.Entity) string {
	return strings.ToUpper(hex.EncodeToString(ent.PrimaryKey.Fingerprint[:]))
}

func serializeArmoredPub(t *testing.T, ent *openpgp.Entity) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := ent.Serialize(&buf); err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return buf.Bytes()
}

func detachSign(t *testing.T, ent *openpgp.Entity, payload []byte) []byte {
	t.Helper()
	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, ent, bytes.NewReader(payload), &packet.Config{}); err != nil {
		t.Fatalf("DetachSign: %v", err)
	}
	return sig.Bytes()
}

func manifestLine(content []byte, relPath string) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]) + "  " + relPath
}

func buildManifest(entries ...string) []byte {
	return []byte(strings.Join(entries, "\n") + "\n")
}

func writeBinary(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestVerifyManifest_GoodSignature(t *testing.T) {
	ent := newTestEntity(t, "release")
	manifest := buildManifest(
		manifestLine([]byte("hook-binary"), "cordum-hook-linux-amd64"),
		manifestLine([]byte("agentd-binary"), "cordum-agentd-linux-amd64"),
	)
	sig := detachSign(t, ent, manifest)

	v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))

	result, err := v.VerifyManifest(manifest, sig)
	if err != nil {
		t.Fatalf("VerifyManifest: unexpected error: %v", err)
	}
	if got := result.Entries["cordum-hook-linux-amd64"]; got == "" {
		t.Fatalf("entry for cordum-hook-linux-amd64 missing")
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
}

func TestVerifyManifest_BadSignature(t *testing.T) {
	ent := newTestEntity(t, "release")
	manifest := buildManifest(manifestLine([]byte("x"), "bin/x"))
	sig := detachSign(t, ent, manifest)
	if len(sig) < 10 {
		t.Fatalf("signature too short to corrupt")
	}
	// Corrupt the middle so it parses as PGP but fails crypto verification.
	corrupted := append([]byte(nil), sig...)
	corrupted[len(corrupted)/2] ^= 0xFF

	v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))

	_, err := v.VerifyManifest(manifest, corrupted)
	if !errors.Is(err, sign.ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestVerifyManifest_WrongPubkey(t *testing.T) {
	signer := newTestEntity(t, "attacker")
	trusted := newTestEntity(t, "release")

	manifest := buildManifest(manifestLine([]byte("x"), "bin/x"))
	sig := detachSign(t, signer, manifest)

	v := sign.NewVerifier([]string{entityFingerprint(trusted)}, serializeArmoredPub(t, trusted))

	_, err := v.VerifyManifest(manifest, sig)
	if !errors.Is(err, sign.ErrPubkeyNotInTrustSet) {
		t.Fatalf("expected ErrPubkeyNotInTrustSet, got %v", err)
	}
}

func TestVerifyManifest_MissingSignature(t *testing.T) {
	ent := newTestEntity(t, "release")
	manifest := buildManifest(manifestLine([]byte("x"), "bin/x"))

	v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))

	_, err := v.VerifyManifest(manifest, nil)
	if !errors.Is(err, sign.ErrUnsignedManifest) {
		t.Fatalf("expected ErrUnsignedManifest, got %v", err)
	}
	_, err = v.VerifyManifest(manifest, []byte{})
	if !errors.Is(err, sign.ErrUnsignedManifest) {
		t.Fatalf("expected ErrUnsignedManifest for empty slice, got %v", err)
	}
}

func TestVerifyBinary_HashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeBinary(t, dir, "cordum-hook", []byte("real-binary"))

	wrong := sha256.Sum256([]byte("not-the-real-binary"))
	err := sign.VerifyBinary(path, hex.EncodeToString(wrong[:]))
	if !errors.Is(err, sign.ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

func TestVerifyBinary_GoodHash(t *testing.T) {
	dir := t.TempDir()
	content := []byte("real-binary")
	path := writeBinary(t, dir, "cordum-hook", content)

	sum := sha256.Sum256(content)
	if err := sign.VerifyBinary(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("VerifyBinary: unexpected error: %v", err)
	}
}

func TestVerifyBinary_MissingBinary(t *testing.T) {
	dir := t.TempDir()
	err := sign.VerifyBinary(filepath.Join(dir, "does-not-exist"), strings.Repeat("0", 64))
	if !errors.Is(err, sign.ErrBinaryNotFound) {
		t.Fatalf("expected ErrBinaryNotFound, got %v", err)
	}
}

func TestVerifierFingerprintPinning(t *testing.T) {
	// File-trust contains entity A, but PinnedReleaseFingerprint is set to
	// a different fingerprint — verifier MUST refuse even though the
	// signature itself is cryptographically valid against the file pubkey.
	saved := sign.PinnedReleaseFingerprint
	defer func() { sign.PinnedReleaseFingerprint = saved }()

	ent := newTestEntity(t, "release")
	manifest := buildManifest(manifestLine([]byte("x"), "bin/x"))
	sig := detachSign(t, ent, manifest)

	otherFingerprint := strings.Repeat("A", 40)
	sign.PinnedReleaseFingerprint = otherFingerprint

	v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))
	_, err := v.VerifyManifest(manifest, sig)
	if !errors.Is(err, sign.ErrFingerprintMismatch) {
		t.Fatalf("expected ErrFingerprintMismatch, got %v", err)
	}
}

func TestVerifyCrossPlatform_PathSeparator(t *testing.T) {
	ent := newTestEntity(t, "release")
	// Manifest always uses forward-slashes (Unix-style relative paths),
	// even when verified on Windows where the local filesystem uses backslash.
	manifest := buildManifest(manifestLine([]byte("x"), "bin/cordum-hook"))
	sig := detachSign(t, ent, manifest)

	v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))
	result, err := v.VerifyManifest(manifest, sig)
	if err != nil {
		t.Fatalf("VerifyManifest: %v", err)
	}
	// Forward-slash form must be the canonical lookup key regardless of host OS.
	if _, ok := result.Entries["bin/cordum-hook"]; !ok {
		t.Fatalf("forward-slash entry missing from result; entries: %v", result.Entries)
	}
	// Backslash form must not appear (we never index by host-specific separators).
	if _, ok := result.Entries["bin\\cordum-hook"]; ok {
		t.Fatalf("backslash entry should not appear in result")
	}
}

func TestManifestPathTraversalRejected(t *testing.T) {
	ent := newTestEntity(t, "release")
	// Attempt to sneak in an absolute path / parent-traversal entry.
	cases := []string{
		manifestLine([]byte("x"), "../../etc/passwd"),
		manifestLine([]byte("x"), "/etc/passwd"),
		manifestLine([]byte("x"), `C:\Windows\System32\drivers\etc\hosts`),
		manifestLine([]byte("x"), "bin/../../../etc/shadow"),
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			manifest := buildManifest(line)
			sig := detachSign(t, ent, manifest)
			v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))
			_, err := v.VerifyManifest(manifest, sig)
			if !errors.Is(err, sign.ErrPathTraversal) {
				t.Fatalf("expected ErrPathTraversal for line %q, got %v", line, err)
			}
		})
	}
}

func TestVerifyManifest_AcceptsEmbeddedVersionLine(t *testing.T) {
	// EDGE-151-DOWNGRADE: parseManifest must skip `# version: vX.Y.Z` lines
	// inserted by the release-time EmbedVersion helper so signature coverage
	// includes the version metadata without breaking the hash loop.
	ent := newTestEntity(t, "release")
	manifest := []byte("# version: v1.2.3\n" +
		manifestLine([]byte("hook-binary"), "cordum-hook-linux-amd64") + "\n")
	sigBytes := detachSign(t, ent, manifest)

	v := sign.NewVerifier([]string{entityFingerprint(ent)}, serializeArmoredPub(t, ent))
	result, err := v.VerifyManifest(manifest, sigBytes)
	if err != nil {
		t.Fatalf("VerifyManifest: unexpected error: %v", err)
	}
	if _, ok := result.Entries["cordum-hook-linux-amd64"]; !ok {
		t.Fatalf("expected entry for cordum-hook-linux-amd64; got entries: %v", result.Entries)
	}
	if _, ok := result.Entries["# version: v1.2.3"]; ok {
		t.Fatalf("comment line leaked into manifest entries")
	}
}

func TestNilSafety(t *testing.T) {
	// Zero-value verifier must not panic; it must return typed errors.
	t.Run("ZeroVerifier_VerifyManifest", func(t *testing.T) {
		var v *sign.Verifier
		if _, err := v.VerifyManifest(nil, nil); err == nil {
			t.Fatalf("expected error from nil receiver, got nil")
		}
	})
	t.Run("NewVerifier_NilArgs", func(t *testing.T) {
		v := sign.NewVerifier(nil, nil)
		if v == nil {
			t.Fatalf("NewVerifier returned nil with nil args; should return non-nil verifier")
		}
		if _, err := v.VerifyManifest(nil, nil); err == nil {
			t.Fatalf("expected typed error from empty verifier, got nil")
		}
	})
	t.Run("VerifyBinary_EmptyPath", func(t *testing.T) {
		err := sign.VerifyBinary("", strings.Repeat("0", 64))
		if err == nil {
			t.Fatalf("expected error for empty path, got nil")
		}
	})
}
