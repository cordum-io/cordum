package main

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cordum/cordum/core/packs/signing"
)

// PackTrustStore is the client-side keyring consulted by cordumctl
// pack install when deciding whether a pack signature is acceptable.
//
// Keys holds every trusted Ed25519 public key indexed by its kid.
// Publishers carries human-readable metadata for each trusted kid so
// operators can confirm what they are trusting. CordumCounterSigningKey
// is the well-known public key that Cordum uses to counter-sign packs
// it has reviewed — --require-cordum-sig demands a signature chain
// including this kid in addition to the publisher's.
type PackTrustStore struct {
	Keys                    map[string]ed25519.PublicKey
	Publishers              map[string]PackPublisherRecord
	CordumCounterSigningKey *ed25519.PublicKey
	CordumCounterSigningKID string
}

// PackPublisherRecord describes a trusted key. All fields are optional
// except KID; when a trusted-keys file carries no publisher metadata
// we only know the kid, and the operator is trusted to have vetted the
// filename convention before dropping the file in the directory.
type PackPublisherRecord struct {
	KID         string    `yaml:"kid" json:"kid"`
	PublisherID string    `yaml:"publisher_id" json:"publisher_id"`
	DisplayName string    `yaml:"display_name" json:"display_name"`
	AddedAt     time.Time `yaml:"added_at" json:"added_at"`
}

// packTrustStoreMaxKeys bounds the keyring so a pathological directory
// can't OOM a cordumctl run.
const packTrustStoreMaxKeys = 256

// envPackTrustedKeyPrefix is the prefix for env-based trusted keys.
// Setting e.g. CORDUM_PACK_TRUSTED_KEY_ACME=<base64-pub> registers a
// public key under kid "acme" (case-lowered). The kid is derived from
// the env suffix so no per-key decoding of metadata is necessary.
const envPackTrustedKeyPrefix = "CORDUM_PACK_TRUSTED_KEY_"

// packTrustedKeysDirEnv lets ops pin the trust store to a specific
// directory via the environment instead of --trusted-keys on every
// invocation.
const packTrustedKeysDirEnv = "CORDUM_PACK_TRUSTED_KEYS_DIR"

// Typed errors for the pack trust store.
var (
	// ErrEmptyKeyringInStrictMode is returned when strict mode is on
	// but no trusted keys could be loaded. Refusing to install is
	// safer than accepting anything that happens to verify against an
	// empty keyring (which, for the crypto API, is never anything).
	ErrEmptyKeyringInStrictMode = errors.New("pack trust: empty keyring in strict mode (add trusted keys via --trusted-keys, CORDUM_PACK_TRUSTED_KEYS_DIR, or CORDUM_PACK_TRUSTED_KEY_<KID>)")
	// ErrTrustStoreFull is returned when the trust store would exceed
	// its 256-key size cap.
	ErrTrustStoreFull = errors.New("pack trust: too many trusted keys (max 256)")
	// ErrTrustedKeyPermissions is returned on POSIX when a key file's
	// mode is more permissive than 0600.
	ErrTrustedKeyPermissions = errors.New("pack trust: trusted key file has unsafe permissions (want 0600)")
)

// embeddedCordumCounterSigningKey holds the Cordum counter-signing
// public key shipped with the binary. The file is intentionally small
// and will be empty in the open-source distribution until Cordum's
// counter-signing workflow lands (separate task). When empty,
// --require-cordum-sig produces an actionable error at install time
// rather than a silent "no chain found" fallthrough.
//
//go:embed trusted-keys/cordum-verified.pub
var embeddedCordumCounterSigningKey []byte

// PackTrustStoreOptions configures how LoadPackTrustStore resolves
// its keyring. Empty fields fall through to the documented defaults.
type PackTrustStoreOptions struct {
	// TrustedKeysDir is the --trusted-keys CLI flag. Files ending in
	// .pub are loaded; the filename (minus extension) is the fallback
	// kid if the file body doesn't carry one.
	TrustedKeysDir string
	// ExtraKeyFiles lets callers stuff in individual --key paths on
	// top of the directory-based keyring. Useful for `cordumctl pack
	// verify --key=X --trusted-keys=...`.
	ExtraKeyFiles []string
	// Strict toggles the empty-keyring guard. An empty keyring in
	// non-strict mode is fine (verify-only-when-signed); in strict
	// mode it's an error.
	Strict bool
	// HomeDirOverride lets tests inject a fake home for the default
	// path lookup without mutating process state. Empty means use
	// os.UserHomeDir.
	HomeDirOverride string
	// EnvLookup lets tests inject a fake environment. Defaults to
	// os.Environ+os.Getenv behavior when nil.
	EnvLookup func(string) string
	// EnvList enumerates env vars for the CORDUM_PACK_TRUSTED_KEY_<KID>
	// sweep. Defaults to os.Environ when nil.
	EnvList func() []string
}

// LoadPackTrustStore resolves the client-side keyring in the documented
// precedence order (first loader wins per kid):
//  1. --trusted-keys=<dir> CLI flag (opts.TrustedKeysDir) plus
//     explicit --key files via opts.ExtraKeyFiles.
//  2. Env CORDUM_PACK_TRUSTED_KEY_<KID>=<base64>.
//  3. $CORDUM_PACK_TRUSTED_KEYS_DIR or ~/.cordum/trusted-keys/.
//  4. Embedded cordum-verified.pub (the Cordum counter-signing key).
//
// Strict mode plus an empty keyring returns ErrEmptyKeyringInStrictMode.
// Exceeding packTrustStoreMaxKeys returns ErrTrustStoreFull.
func LoadPackTrustStore(opts PackTrustStoreOptions) (*PackTrustStore, error) {
	env := opts.EnvLookup
	if env == nil {
		env = os.Getenv
	}
	envList := opts.EnvList
	if envList == nil {
		envList = os.Environ
	}
	store := &PackTrustStore{
		Keys:       map[string]ed25519.PublicKey{},
		Publishers: map[string]PackPublisherRecord{},
	}

	// (1a) --trusted-keys directory.
	if dir := strings.TrimSpace(opts.TrustedKeysDir); dir != "" {
		if err := appendTrustedKeysDir(store, dir); err != nil {
			return nil, err
		}
	}
	// (1b) --key extras.
	for _, path := range opts.ExtraKeyFiles {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if err := appendTrustedKeyFile(store, path); err != nil {
			return nil, err
		}
	}

	// (2) Env-based overrides (do NOT overwrite a CLI-loaded key).
	for _, entry := range envList() {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := entry[:eq]
		if !strings.HasPrefix(name, envPackTrustedKeyPrefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, envPackTrustedKeyPrefix)
		if suffix == "" {
			continue
		}
		kid := strings.ToLower(strings.ReplaceAll(suffix, "_", "-"))
		if _, existing := store.Keys[kid]; existing {
			continue
		}
		value := strings.TrimSpace(entry[eq+1:])
		if value == "" {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: env %s must be a %d-byte base64 public key", signing.ErrInvalidKey, name, ed25519.PublicKeySize)
		}
		if len(store.Keys)+1 > packTrustStoreMaxKeys {
			return nil, ErrTrustStoreFull
		}
		store.Keys[kid] = ed25519.PublicKey(pub)
		store.Publishers[kid] = PackPublisherRecord{
			KID:         kid,
			PublisherID: "env:" + kid,
			DisplayName: "env:" + name,
			AddedAt:     time.Now().UTC(),
		}
	}

	// (3) Default trusted-keys directory — env var wins over ~/.cordum.
	if opts.TrustedKeysDir == "" {
		defaultDir := strings.TrimSpace(env(packTrustedKeysDirEnv))
		if defaultDir == "" {
			defaultDir = defaultTrustedKeysDir(opts.HomeDirOverride)
		}
		if defaultDir != "" {
			if info, err := os.Stat(defaultDir); err == nil && info.IsDir() {
				if err := appendTrustedKeysDir(store, defaultDir); err != nil {
					return nil, err
				}
			}
		}
	}

	// (4) Embedded Cordum counter-signing key — registered under its
	// own kid so --require-cordum-sig can identify it.
	if err := appendEmbeddedCordumKey(store); err != nil {
		return nil, err
	}

	if opts.Strict && len(store.Keys) == 0 {
		return nil, ErrEmptyKeyringInStrictMode
	}
	return store, nil
}

// HasCordumCounterSigningKey reports whether the embedded Cordum
// counter-signing key is available. Callers use this to gate
// --require-cordum-sig with a clear error rather than a mystery
// unknown-kid failure from the signing layer.
func (s *PackTrustStore) HasCordumCounterSigningKey() bool {
	return s != nil && s.CordumCounterSigningKey != nil && len(*s.CordumCounterSigningKey) == ed25519.PublicKeySize
}

// defaultTrustedKeysDir resolves the per-user default trust-store path.
// On a misconfigured shell with no HOME, it returns the empty string —
// the caller treats that as "no default directory" rather than
// guessing at ./ .cordum or writing outside the cwd.
func defaultTrustedKeysDir(homeOverride string) string {
	home := strings.TrimSpace(homeOverride)
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(resolved) == "" {
			return ""
		}
		home = resolved
	}
	return filepath.Join(home, ".cordum", "trusted-keys")
}

func appendTrustedKeysDir(store *PackTrustStore, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(p)
		if !strings.HasSuffix(lower, ".pub") {
			return nil
		}
		return appendTrustedKeyFile(store, p)
	})
}

func appendTrustedKeyFile(store *PackTrustStore, path string) error {
	if err := checkTrustedKeyPermissions(path); err != nil {
		return err
	}
	kid, pub, err := loadPackPublicKeyFile(path)
	if err != nil {
		return fmt.Errorf("trusted key %s: %w", path, err)
	}
	if _, existing := store.Keys[kid]; existing {
		// Precedence: first loader wins. Silently ignore the dup; a
		// later --key flag should not stomp a directory entry that
		// the operator already vetted.
		return nil
	}
	if len(store.Keys)+1 > packTrustStoreMaxKeys {
		return ErrTrustStoreFull
	}
	store.Keys[kid] = pub
	store.Publishers[kid] = PackPublisherRecord{
		KID:         kid,
		PublisherID: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		DisplayName: filepath.Base(path),
		AddedAt:     keyFileModTime(path),
	}
	return nil
}

// checkTrustedKeyPermissions enforces 0600 on POSIX. On Windows the
// Go permission bits don't map to NTFS ACLs, so we can only warn;
// returning an error there would make the flow unusable on a
// perfectly-secured NTFS install.
func checkTrustedKeyPermissions(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Only the owner-read/write bits are allowed; anything in group or
	// other is a leak.
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s has mode %o", ErrTrustedKeyPermissions, path, info.Mode().Perm())
	}
	return nil
}

func keyFileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime().UTC()
}

func appendEmbeddedCordumKey(store *PackTrustStore) error {
	raw := strings.TrimSpace(string(embeddedCordumCounterSigningKey))
	if raw == "" {
		return nil
	}
	// Build a temp file so we can route through loadPackPublicKeyFile
	// without duplicating YAML/JSON decoding.
	kid, pub, err := decodePackPublicKeyBytes(embeddedCordumCounterSigningKey)
	if err != nil {
		return fmt.Errorf("embedded cordum-verified.pub: %w", err)
	}
	if _, existing := store.Keys[kid]; !existing {
		if len(store.Keys)+1 > packTrustStoreMaxKeys {
			return ErrTrustStoreFull
		}
		store.Keys[kid] = pub
		store.Publishers[kid] = PackPublisherRecord{
			KID:         kid,
			PublisherID: "cordum",
			DisplayName: "Cordum counter-signing (embedded)",
			AddedAt:     time.Time{},
		}
	}
	key := pub
	store.CordumCounterSigningKey = &key
	store.CordumCounterSigningKID = kid
	return nil
}

// decodePackPublicKeyBytes parses an in-memory .pub payload. Shares
// the file-format logic with loadPackPublicKeyFile in pack_sign.go so
// the embedded key uses exactly the same schema as operator-supplied
// files.
func decodePackPublicKeyBytes(raw []byte) (string, ed25519.PublicKey, error) {
	tmp, err := os.CreateTemp("", "cordum-embedded-*.pub")
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		return "", nil, err
	}
	return loadPackPublicKeyFile(tmp.Name())
}
