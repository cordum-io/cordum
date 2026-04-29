package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/packs/signing"
	"github.com/redis/go-redis/v9"
)

// Gateway-side mirror verification for pack installs (task-9c63baa0
// step 3). This is the zero-trust rail: even when cordumctl verified
// the pack locally, the gateway MUST re-verify on receipt because the
// client code can be patched out.

const (
	// gatewayPackStrictEnv flips the gateway into strict mode at
	// startup time. Runtime flip via Redis still takes precedence.
	gatewayPackStrictEnv = "CORDUM_GATEWAY_PACK_STRICT"
	// gatewayPackTrustedKeyEnvPrefix lets deployers bootstrap a few
	// trusted publisher keys without round-tripping through the admin
	// endpoint. Admins register more keys via the admin endpoint
	// (separate task).
	gatewayPackTrustedKeyEnvPrefix = "CORDUM_GATEWAY_PACK_TRUSTED_KEY_"
	// gatewayStrictRedisKey is the runtime-flippable config key that
	// ops can toggle with `SET cfg:packs:strict_mode true` to enforce
	// strict mode immediately without a redeploy.
	gatewayStrictRedisKey = "cfg:packs:strict_mode"
	// gatewayTrustedKeysRedisPrefix is the scanned prefix for trusted
	// publisher keys registered via the admin endpoint. Values are
	// base64-encoded Ed25519 public keys; the kid is the suffix.
	gatewayTrustedKeysRedisPrefix = "packs:trusted_keys:"
	// gatewayPackPublisherRedisPrefix is the scanned prefix for
	// publisher metadata records keyed by kid. Values are JSON
	// documents with the publisher_id, display_name, added_at fields
	// that get attached to an installed-pack verification record.
	// Absence of this record is NOT fatal — the verifier falls back
	// to using the kid itself as the publisher_id so the DoD item
	// "installed pack metadata includes: signed, publisher_id,
	// verified_at" is always satisfied.
	gatewayPackPublisherRedisPrefix = "packs:publishers:"
	// gatewayPackPublisherEnvPrefix is the env-bootstrap analog for
	// publisher metadata. CORDUM_GATEWAY_PACK_PUBLISHER_<KID>=<publisher_id>
	// (shorthand; display_name falls back to publisher_id).
	gatewayPackPublisherEnvPrefix = "CORDUM_GATEWAY_PACK_PUBLISHER_"
	// gatewayStrictCacheTTL bounds how long a single gateway process
	// caches the Redis strict flag. Short enough that an incident
	// response flip propagates across replicas within the cache TTL.
	gatewayStrictCacheTTL = time.Second
)

// PackVerificationEventKind values match the audit-event taxonomy in
// the task plan.
const (
	PackVerificationEventVerified = "pack.install.verified"
	PackVerificationEventRejected = "pack.install.rejected"
)

// PackVerificationError is the typed error returned by the gateway
// verify helper. Code is the over-the-wire `error_code` the handler
// attaches to a 400 response; it is deliberately stable so operators
// can grep logs without chasing Go error strings.
type PackVerificationError struct {
	Code    string
	Message string
	Err     error
}

func (e *PackVerificationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *PackVerificationError) Unwrap() error { return e.Err }

// PackVerification error codes.
const (
	PackVerificationCodeUnsigned          = "pack.unsigned"
	PackVerificationCodeBadSignature      = "pack.bad_signature"
	PackVerificationCodeTampered          = "pack.tampered"
	PackVerificationCodeUnknownKID        = "pack.unknown_kid"
	PackVerificationCodeMissingCordumSig  = "pack.missing_cordum_sig"
	PackVerificationCodeMalformed         = "pack.malformed"
	PackVerificationCodeVerifyUnavailable = "pack.verify_unavailable"
)

// gatewayVerifiedPackInstall is the server-side decision record. It's
// attached to the packs.PackRecord before updatePackRegistry so the install
// state carries the verification outcome the gateway computed (not a
// client-supplied claim).
type gatewayVerifiedPackInstall struct {
	Signed              bool      `json:"signed"`
	PublisherID         string    `json:"publisher_id,omitempty"`
	KID                 string    `json:"kid,omitempty"`
	VerifiedAt          time.Time `json:"verified_at,omitempty"`
	HasCordumCounterSig bool      `json:"has_cordum_counter_sig,omitempty"`
}

// gatewayPublisherRecord pairs a trusted kid with operator-registered
// publisher metadata. DisplayName is optional; callers fall back to
// PublisherID when absent.
type gatewayPublisherRecord struct {
	KID         string    `json:"kid"`
	PublisherID string    `json:"publisher_id"`
	DisplayName string    `json:"display_name,omitempty"`
	AddedAt     time.Time `json:"added_at,omitempty"`
}

// gatewayTrustKeyring bundles the key + publisher-metadata maps returned
// by loadGatewayPackTrustKeyring. Every Keys kid has a matching
// Publishers entry (synthesised from the kid when no metadata is
// registered) so verifyPackInstallBundle can always populate
// PublisherID on a successful verify.
type gatewayTrustKeyring struct {
	Keys       map[string]ed25519.PublicKey
	Publishers map[string]gatewayPublisherRecord
}

// packStrictCache caches the server-side strict-mode flag for up to
// gatewayStrictCacheTTL so a 1000-RPS install fleet doesn't hammer
// Redis on every request. Flips propagate within TTL seconds.
type packStrictCache struct {
	mu        sync.RWMutex
	value     bool
	expiresAt time.Time
}

var packStrictFlag = &packStrictCache{}

// resolveGatewayPackStrict returns the current strict-mode flag.
// Precedence: env var (truthy → strict always), then Redis cfg key,
// then default (non-strict). Redis errors fall through to env/default
// so a Redis outage does not make every install succeed.
func resolveGatewayPackStrict(ctx context.Context, client redis.UniversalClient) bool {
	if envStrict := strings.ToLower(strings.TrimSpace(os.Getenv(gatewayPackStrictEnv))); envStrict == "1" || envStrict == "true" || envStrict == "yes" || envStrict == "on" {
		return true
	}
	packStrictFlag.mu.RLock()
	if time.Now().Before(packStrictFlag.expiresAt) {
		v := packStrictFlag.value
		packStrictFlag.mu.RUnlock()
		return v
	}
	packStrictFlag.mu.RUnlock()

	if client == nil {
		return false
	}
	val, err := client.Get(ctx, gatewayStrictRedisKey).Result()
	strict := false
	if err == nil {
		lower := strings.ToLower(strings.TrimSpace(val))
		strict = lower == "1" || lower == "true" || lower == "yes" || lower == "on"
	}
	packStrictFlag.mu.Lock()
	packStrictFlag.value = strict
	packStrictFlag.expiresAt = time.Now().Add(gatewayStrictCacheTTL)
	packStrictFlag.mu.Unlock()
	return strict
}

// loadGatewayPackTrustKeyring builds the server-side keyring from env
// overrides plus all `packs:trusted_keys:*` Redis entries, then layers
// publisher metadata from env `CORDUM_GATEWAY_PACK_PUBLISHER_<KID>=<id>`
// and Redis `packs:publishers:<kid>` JSON records on top. Every
// returned Keys entry has a matching Publishers entry — synthesised
// from the kid itself when no operator-registered record exists — so
// a successful verify always has a publisher_id to record. When the
// Redis client is nil, env is the sole source.
func loadGatewayPackTrustKeyring(ctx context.Context, client redis.UniversalClient) (*gatewayTrustKeyring, error) {
	out := &gatewayTrustKeyring{
		Keys:       map[string]ed25519.PublicKey{},
		Publishers: map[string]gatewayPublisherRecord{},
	}
	// Env bootstrap for public keys.
	for _, entry := range os.Environ() {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := entry[:eq]
		if !strings.HasPrefix(name, gatewayPackTrustedKeyEnvPrefix) {
			continue
		}
		kid := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(name, gatewayPackTrustedKeyEnvPrefix), "_", "-"))
		pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(entry[eq+1:]))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: env %s must be a %d-byte base64 public key", signing.ErrInvalidKey, name, ed25519.PublicKeySize)
		}
		out.Keys[kid] = ed25519.PublicKey(pub)
	}
	// Env bootstrap for publisher metadata.
	for _, entry := range os.Environ() {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		name := entry[:eq]
		if !strings.HasPrefix(name, gatewayPackPublisherEnvPrefix) {
			continue
		}
		kid := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(name, gatewayPackPublisherEnvPrefix), "_", "-"))
		publisherID := strings.TrimSpace(entry[eq+1:])
		if publisherID == "" {
			continue
		}
		out.Publishers[kid] = gatewayPublisherRecord{
			KID:         kid,
			PublisherID: publisherID,
			DisplayName: publisherID,
		}
	}
	if client == nil {
		attachPublisherFallback(out)
		return out, nil
	}
	// Redis keyring — scan the prefix so we don't care how many keys
	// are registered (KEYS is O(N) but acceptable here because this
	// runs per install and admins register O(10) publisher keys).
	keyIter := client.Scan(ctx, 0, gatewayTrustedKeysRedisPrefix+"*", 0).Iterator()
	for keyIter.Next(ctx) {
		key := keyIter.Val()
		kid := strings.TrimPrefix(key, gatewayTrustedKeysRedisPrefix)
		if kid == "" {
			continue
		}
		val, err := client.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			continue
		}
		// Env takes precedence if already loaded.
		if _, exists := out.Keys[kid]; exists {
			continue
		}
		out.Keys[kid] = ed25519.PublicKey(pub)
	}
	if err := keyIter.Err(); err != nil {
		attachPublisherFallback(out)
		return out, fmt.Errorf("scan %s*: %w", gatewayTrustedKeysRedisPrefix, err)
	}
	// Redis publisher metadata — best-effort, errors are non-fatal.
	// A missing publisher record is handled by attachPublisherFallback
	// below, which synthesises a record from the kid itself.
	pubIter := client.Scan(ctx, 0, gatewayPackPublisherRedisPrefix+"*", 0).Iterator()
	for pubIter.Next(ctx) {
		key := pubIter.Val()
		kid := strings.TrimPrefix(key, gatewayPackPublisherRedisPrefix)
		if kid == "" {
			continue
		}
		val, err := client.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var record gatewayPublisherRecord
		if err := json.Unmarshal([]byte(val), &record); err != nil {
			continue
		}
		record.KID = kid
		if record.PublisherID == "" {
			continue
		}
		// Env takes precedence if already loaded.
		if _, exists := out.Publishers[kid]; exists {
			continue
		}
		out.Publishers[kid] = record
	}
	attachPublisherFallback(out)
	return out, nil
}

// attachPublisherFallback fills in a synthesised publisher record for
// every kid present in Keys but absent from Publishers. The fallback
// uses the kid as both the publisher_id and display_name so the
// installed-pack metadata always carries a non-empty publisher_id
// when Signed=true — satisfying the DoD even when ops haven't
// registered a richer publisher profile.
func attachPublisherFallback(k *gatewayTrustKeyring) {
	if k == nil {
		return
	}
	for kid := range k.Keys {
		if _, ok := k.Publishers[kid]; ok {
			continue
		}
		k.Publishers[kid] = gatewayPublisherRecord{
			KID:         kid,
			PublisherID: kid,
			DisplayName: kid,
		}
	}
}

// verifyPackInstallBundle is the server-side mirror gate called by
// installPackFromDir before any state-changing step. It is a complete
// replay of the client-side gate: locate signature, load trust store
// from env+Redis, call signing.VerifyPack. Unlike the cordumctl gate
// we never warn-and-proceed — strict mode is binary at the gateway,
// resolved from env/Redis.
func verifyPackInstallBundle(ctx context.Context, client redis.UniversalClient, bundleDir, packID string) (*gatewayVerifiedPackInstall, *PackVerificationError) {
	strict := resolveGatewayPackStrict(ctx, client)
	sigPath, sigPresent := locateGatewayPackSignature(bundleDir)
	if !sigPresent {
		if strict {
			return nil, &PackVerificationError{
				Code:    PackVerificationCodeUnsigned,
				Message: fmt.Sprintf("pack %q is unsigned (strict mode)", packID),
			}
		}
		return &gatewayVerifiedPackInstall{Signed: false}, nil
	}

	keyring, err := loadGatewayPackTrustKeyring(ctx, client)
	if err != nil {
		return nil, &PackVerificationError{
			Code:    PackVerificationCodeVerifyUnavailable,
			Message: "failed to load trust keyring",
			Err:     err,
		}
	}

	raw, readErr := os.ReadFile(sigPath) // #nosec G304 -- sigPath is a fixed signature filename under the verified bundle directory.
	if readErr != nil {
		return nil, &PackVerificationError{
			Code:    PackVerificationCodeMalformed,
			Message: "cannot read signature file",
			Err:     readErr,
		}
	}
	signed, decodeErr := signing.DecodeEnvelope(raw)
	if decodeErr != nil {
		return nil, &PackVerificationError{
			Code:    PackVerificationCodeMalformed,
			Message: "signature envelope malformed",
			Err:     decodeErr,
		}
	}
	if verifyErr := signing.VerifyPack(bundleDir, signed, keyring.Keys); verifyErr != nil {
		return nil, packVerifyErrorForSigningErr(verifyErr, packID)
	}

	kid := signed.Signature.KeyID
	result := &gatewayVerifiedPackInstall{
		Signed:     true,
		KID:        kid,
		VerifiedAt: time.Now().UTC(),
	}
	if record, ok := keyring.Publishers[kid]; ok {
		result.PublisherID = record.PublisherID
	} else {
		// attachPublisherFallback always synthesises a record, but
		// guard defensively in case loadGatewayPackTrustKeyring
		// returns an inconsistent keyring on a future edit.
		result.PublisherID = kid
	}
	// Cordum counter-signature — optional sibling file. When present,
	// verify against the same keyring (admins can register Cordum's
	// counter-signing key just like any other publisher).
	cordumSigPath := filepath.Join(bundleDir, cordumCounterSigFilenameServer)
	if info, statErr := os.Stat(cordumSigPath); statErr == nil && !info.IsDir() {
		cordumRaw, readErr := os.ReadFile(cordumSigPath) // #nosec G304 -- fixed optional counter-signature filename under the verified bundle directory.
		if readErr == nil {
			if cordumEnvelope, err := signing.DecodeEnvelope(cordumRaw); err == nil {
				if err := signing.VerifyPack(bundleDir, cordumEnvelope, keyring.Keys); err == nil {
					result.HasCordumCounterSig = true
				}
			}
		}
	}
	return result, nil
}

// cordumCounterSigFilenameServer is duplicated here deliberately —
// the cordumctl package and the gateway package are independent Go
// packages (no shared constants module between them), so each declares
// its own canonical filename for the counter-signature envelope.
const cordumCounterSigFilenameServer = "pack.yaml.sig.cordum"

// locateGatewayPackSignature mirrors the cordumctl-side loader but in
// its own package. YAML (pack.yaml.sig) takes precedence over JSON.
func locateGatewayPackSignature(bundleDir string) (string, bool) {
	candidates := []string{
		filepath.Join(bundleDir, "pack.yaml.sig"),
		filepath.Join(bundleDir, "pack.yaml.sig.json"),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// packVerifyErrorForSigningErr maps the signing library's typed
// sentinels to stable over-the-wire error codes.
func packVerifyErrorForSigningErr(err error, packID string) *PackVerificationError {
	switch {
	case errors.Is(err, signing.ErrUnknownKeyID):
		return &PackVerificationError{
			Code:    PackVerificationCodeUnknownKID,
			Message: fmt.Sprintf("pack %q signed with unknown kid", packID),
			Err:     err,
		}
	case errors.Is(err, signing.ErrHashMismatch), errors.Is(err, signing.ErrMissingFile):
		return &PackVerificationError{
			Code:    PackVerificationCodeTampered,
			Message: fmt.Sprintf("pack %q body tampered since signing", packID),
			Err:     err,
		}
	case errors.Is(err, signing.ErrBadSignature),
		errors.Is(err, signing.ErrInvalidKey),
		errors.Is(err, signing.ErrDomainMismatch),
		errors.Is(err, signing.ErrUnsupportedAlgorithm):
		return &PackVerificationError{
			Code:    PackVerificationCodeBadSignature,
			Message: fmt.Sprintf("pack %q signature invalid", packID),
			Err:     err,
		}
	case errors.Is(err, signing.ErrManifestMalformed):
		return &PackVerificationError{
			Code:    PackVerificationCodeMalformed,
			Message: fmt.Sprintf("pack %q manifest malformed", packID),
			Err:     err,
		}
	}
	return &PackVerificationError{
		Code:    PackVerificationCodeBadSignature,
		Message: fmt.Sprintf("pack %q signature verification failed", packID),
		Err:     err,
	}
}

// resetGatewayPackStrictCache is a test-only helper so parallel tests
// don't leak cached flag state across runs.
func resetGatewayPackStrictCache() {
	packStrictFlag.mu.Lock()
	packStrictFlag.value = false
	packStrictFlag.expiresAt = time.Time{}
	packStrictFlag.mu.Unlock()
}

// verificationFromServerResult lifts a gatewayVerifiedPackInstall into
// the on-the-wire packs.PackRecordVerification shape stored alongside the
// pack record. Nil input returns nil so the JSON field omits cleanly.
func verificationFromServerResult(v *gatewayVerifiedPackInstall) *packs.PackRecordVerification {
	if v == nil {
		return nil
	}
	out := &packs.PackRecordVerification{
		Signed:              v.Signed,
		PublisherID:         v.PublisherID,
		KID:                 v.KID,
		HasCordumCounterSig: v.HasCordumCounterSig,
		SignatureAlgorithm:  signing.AlgorithmEd25519,
		PackSignatureVer:    signing.ManifestVersion,
	}
	if !v.VerifiedAt.IsZero() {
		out.VerifiedAt = v.VerifiedAt.UTC().Format(time.RFC3339)
	}
	if !v.Signed {
		out.Warnings = []string{"pack accepted unsigned — gateway strict mode disabled"}
	}
	return out
}
