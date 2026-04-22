// Package signing implements Ed25519 signing + verification for Cordum
// packs. It produces a canonical manifest of a pack's signed resources
// (pack.yaml plus every file referenced by resources.schemas,
// resources.workflows, overlays.config, overlays.policy), hashes each
// file with SHA-256, and signs the manifest under the domain-separated
// context string "cordum.pack.v1" so a key compromise in one Cordum
// signature domain (license, delegation, MCP outbound) does not cross
// over into another.
package signing

import "errors"

// Typed errors returned by the signing library. Callers should use
// errors.Is to branch on them — the library never wraps them in
// fmt.Errorf to preserve the sentinel.
var (
	// ErrPackRootNotDirectory is returned when the pack-root argument
	// does not resolve to a readable directory.
	ErrPackRootNotDirectory = errors.New("signing: pack root is not a directory")
	// ErrManifestNotFound is returned when pack.yaml is missing from
	// the pack root.
	ErrManifestNotFound = errors.New("signing: pack.yaml not found")
	// ErrManifestMalformed is returned when the manifest payload does
	// not match the expected envelope shape.
	ErrManifestMalformed = errors.New("signing: manifest malformed")
	// ErrMissingFile is returned when the manifest references a file
	// that does not exist on disk.
	ErrMissingFile = errors.New("signing: referenced file missing")
	// ErrSymlinkRejected is returned when the walker encounters a
	// symlink inside the pack root. Symlinks are a supply-chain
	// hazard (pointing at /etc/passwd, for instance) so we refuse to
	// sign them at all.
	ErrSymlinkRejected = errors.New("signing: symlink rejected")
	// ErrEscapesRoot is returned when a manifest-referenced path
	// resolves outside the pack root (e.g. "../secret.key").
	ErrEscapesRoot = errors.New("signing: path escapes pack root")
	// ErrHashMismatch is returned when a file's on-disk hash differs
	// from the hash recorded in the signed manifest.
	ErrHashMismatch = errors.New("signing: file hash mismatch")
	// ErrUnknownKeyID is returned when the signature's kid is not
	// present in the verification keyring.
	ErrUnknownKeyID = errors.New("signing: unknown key id")
	// ErrBadSignature is returned when the Ed25519 signature does not
	// verify against the public key for the advertised kid.
	ErrBadSignature = errors.New("signing: bad signature")
	// ErrInvalidKey is returned when a supplied signing/verification
	// key has the wrong size or encoding.
	ErrInvalidKey = errors.New("signing: invalid key")
	// ErrUnsupportedAlgorithm is returned when the envelope carries an
	// algorithm string other than "ed25519".
	ErrUnsupportedAlgorithm = errors.New("signing: unsupported algorithm")
	// ErrDomainMismatch is returned when the envelope advertises a
	// domain string other than the one this library signs under.
	// Prevents cross-context signature replay.
	ErrDomainMismatch = errors.New("signing: domain mismatch")
)
