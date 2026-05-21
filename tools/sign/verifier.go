package sign

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	pgperrors "github.com/ProtonMail/go-crypto/openpgp/errors"
)

// PinnedReleaseFingerprint is the SHA-1 hex-encoded uppercase OpenPGP
// fingerprint of the production release-signing key, baked in at build
// time via
//
//	go build -ldflags '-X github.com/cordum/cordum/tools/sign.PinnedReleaseFingerprint=<hex>'
//
// When empty (e.g. local dev builds via `make release-local`) no compile-time
// pinning is enforced and only the trustedFingerprints supplied to
// NewVerifier are honoured. Replacing the bundled
// `tools/keys/cordum-release.pub.asc` alone cannot bypass this check on
// production builds because the pin lives in the linker-emitted .rodata
// rather than on the filesystem.
var PinnedReleaseFingerprint string

// Verifier checks detached OpenPGP signatures over SHA256SUMS-style
// manifests and hashes binaries against the parsed manifest entries.
// The zero value is safe to call (every method returns a typed error)
// but rejects every input. Construct with NewVerifier for real use.
type Verifier struct {
	trustedFingerprints map[string]struct{}
	keyRing             openpgp.EntityList
}

// ManifestResult is the parsed view of a verified manifest. Entries are
// keyed by the forward-slash relative path (canonical regardless of host
// OS) and contain the lowercase SHA-256 hex digest expected for that file.
type ManifestResult struct {
	Entries map[string]string
}

// NewVerifier returns a verifier trusting the supplied fingerprints
// (compared case-insensitively, whitespace-trimmed) and using pubKeyBlock
// — a binary or ASCII-armored OpenPGP public key block — as the keyring
// for signature checking. Both arguments may be nil or empty; the
// returned verifier is non-nil and rejects every input via typed errors.
func NewVerifier(fingerprints []string, pubKeyBlock []byte) *Verifier {
	v := &Verifier{trustedFingerprints: make(map[string]struct{}, len(fingerprints))}
	for _, fp := range fingerprints {
		clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(fp), " ", ""))
		if clean == "" {
			continue
		}
		v.trustedFingerprints[clean] = struct{}{}
	}
	if len(pubKeyBlock) > 0 {
		if ring, err := readKeyRingAny(pubKeyBlock); err == nil {
			v.keyRing = ring
		}
	}
	return v
}

// readKeyRingAny accepts either ASCII-armored or binary OpenPGP key blocks.
func readKeyRingAny(block []byte) (openpgp.EntityList, error) {
	if bytes.HasPrefix(bytes.TrimSpace(block), []byte("-----BEGIN")) {
		return openpgp.ReadArmoredKeyRing(bytes.NewReader(block))
	}
	return openpgp.ReadKeyRing(bytes.NewReader(block))
}

// VerifyManifest validates that sigBytes is a detached signature over
// manifestBytes produced by a key whose fingerprint is in the verifier's
// trust set AND matches PinnedReleaseFingerprint (when set). On success
// it returns the parsed ManifestResult; on failure it returns a typed
// sentinel error that errors.Is identifies.
//
// Order of operations is verify-first, parse-second: an attacker who can
// craft a malformed manifest can never cause path-traversal handling code
// to run unless they also possess a trusted signing key. After signature
// validation succeeds, the manifest is parsed and any entry containing a
// parent-traversal segment, absolute path, or Windows drive root causes
// the call to fail with ErrPathTraversal even though the signature was
// otherwise valid.
func (v *Verifier) VerifyManifest(manifestBytes, sigBytes []byte) (ManifestResult, error) {
	if v == nil {
		return ManifestResult{}, fmt.Errorf("sign: nil verifier: %w", ErrSignatureMismatch)
	}
	if len(sigBytes) == 0 {
		return ManifestResult{}, ErrUnsignedManifest
	}
	if v.keyRing == nil || len(v.trustedFingerprints) == 0 {
		return ManifestResult{}, ErrPubkeyNotInTrustSet
	}

	signer, err := openpgp.CheckDetachedSignature(
		v.keyRing,
		bytes.NewReader(manifestBytes),
		bytes.NewReader(sigBytes),
		nil,
	)
	if err != nil {
		if errors.Is(err, pgperrors.ErrUnknownIssuer) {
			return ManifestResult{}, ErrPubkeyNotInTrustSet
		}
		return ManifestResult{}, fmt.Errorf("sign: openpgp verify: %w", ErrSignatureMismatch)
	}
	if signer == nil || signer.PrimaryKey == nil {
		return ManifestResult{}, ErrSignatureMismatch
	}

	fp := strings.ToUpper(hex.EncodeToString(signer.PrimaryKey.Fingerprint[:]))
	if _, ok := v.trustedFingerprints[fp]; !ok {
		return ManifestResult{}, ErrPubkeyNotInTrustSet
	}
	if PinnedReleaseFingerprint != "" {
		want := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(PinnedReleaseFingerprint), " ", ""))
		if fp != want {
			return ManifestResult{}, ErrFingerprintMismatch
		}
	}

	entries, err := parseManifest(manifestBytes)
	if err != nil {
		return ManifestResult{}, err
	}
	return ManifestResult{Entries: entries}, nil
}

// VerifyBinary computes the SHA-256 of the file at path and compares it
// in constant time against expectedHash (lowercase hex; case-insensitive).
// It returns ErrBinaryNotFound when the path is empty or missing,
// ErrHashMismatch on digest disagreement, and a wrapped I/O error in any
// other failure mode. The buffer used for hashing is bounded by io.Copy's
// internal default so malicious oversized binaries cannot trigger OOM
// against the calling install path.
func VerifyBinary(path, expectedHash string) error {
	if path == "" {
		return ErrBinaryNotFound
	}
	f, err := os.Open(path) //nolint:gosec // path is supplied by trusted release-dir caller
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrBinaryNotFound, path)
		}
		return fmt.Errorf("sign: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("sign: hash %s: %w", path, err)
	}
	got := strings.ToLower(hex.EncodeToString(h.Sum(nil)))
	want := strings.ToLower(strings.TrimSpace(expectedHash))
	if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return fmt.Errorf("%w: file=%s want=%s got=%s",
			ErrHashMismatch, filepath.Base(path), want, got)
	}
	return nil
}

func parseManifest(b []byte) (map[string]string, error) {
	entries := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		// EDGE-151-DOWNGRADE: tolerate `# version: vN.N.N` metadata lines
		// inserted by EmbedVersion at release time. These are
		// signature-covered but carry no per-binary hash.
		if strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.Index(line, "  ")
		if sep == -1 {
			return nil, fmt.Errorf("sign: malformed manifest line: %q", line)
		}
		hash := strings.TrimSpace(line[:sep])
		rel := strings.TrimSpace(line[sep+2:])
		if hash == "" || rel == "" {
			return nil, fmt.Errorf("sign: malformed manifest line: %q", line)
		}
		canon := filepath.ToSlash(rel)
		if !isSafeRelativePath(canon) {
			return nil, fmt.Errorf("%w: %q", ErrPathTraversal, rel)
		}
		entries[canon] = strings.ToLower(hash)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("sign: scan manifest: %w", err)
	}
	return entries, nil
}

// isSafeRelativePath rejects empty, absolute, parent-traversing, and
// Windows-drive-rooted paths. Input is assumed already filepath.ToSlash'd
// so a single forward-slash split is sufficient.
func isSafeRelativePath(p string) bool {
	if p == "" || p == "." {
		return false
	}
	// Unix absolute or backslash-rooted (post-ToSlash a Windows "\foo"
	// would become "/foo", but we also handle the rare raw case).
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return false
	}
	// Windows drive letter, e.g. "C:/Windows" or "C:\Windows".
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
