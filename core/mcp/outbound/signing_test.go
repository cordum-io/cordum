package outbound

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newSigner + newVerifier are convenience helpers that mint an
// ephemeral keypair and wire them through a single-key trust store.
func newSigner(t *testing.T, keyID string) (*Signer, *ecdsa.PublicKey) {
	t.Helper()
	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	s, err := NewSigner(priv, keyID)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s, &priv.PublicKey
}

func newVerifier(t *testing.T, keyID string, pub *ecdsa.PublicKey, store NonceStore, skew time.Duration) *Verifier {
	t.Helper()
	v, err := NewVerifier(map[string]*ecdsa.PublicKey{keyID: pub}, store, skew)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	t.Parallel()
	signer, pub := newSigner(t, "prod-1")
	verifier := newVerifier(t, "prod-1", pub, NewInMemoryNonceStore(), DefaultClockSkew)

	method := "tools/call"
	params := []byte(`{"name":"greet","arguments":{"target":"world"}}`)
	headers, err := signer.SignRequest(method, params, "tenant-a", "agent-x")
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	for _, k := range []string{HeaderKeyID, HeaderTimestamp, HeaderNonce, HeaderTenant, HeaderAgentID, HeaderSignature} {
		if headers[k] == "" {
			t.Errorf("missing header %s", k)
		}
	}
	if headers[HeaderKeyID] != "prod-1" {
		t.Errorf("key id = %q want prod-1", headers[HeaderKeyID])
	}
	if _, err := base64.StdEncoding.DecodeString(headers[HeaderSignature]); err != nil {
		t.Errorf("signature not base64: %v", err)
	}
	if _, err := hex.DecodeString(headers[HeaderNonce]); err != nil {
		t.Errorf("nonce not hex: %v", err)
	}

	if err := verifier.VerifyRequest(headers, method, params); err != nil {
		t.Fatalf("VerifyRequest: %v", err)
	}
}

func TestVerify_RejectsTamperedParams(t *testing.T) {
	t.Parallel()
	signer, pub := newSigner(t, "k1")
	verifier := newVerifier(t, "k1", pub, NewInMemoryNonceStore(), DefaultClockSkew)
	headers, _ := signer.SignRequest("m", []byte(`{"a":1}`), "t", "a")
	err := verifier.VerifyRequest(headers, "m", []byte(`{"a":2}`))
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestVerify_RejectsTamperedMethod(t *testing.T) {
	t.Parallel()
	signer, pub := newSigner(t, "k1")
	verifier := newVerifier(t, "k1", pub, NewInMemoryNonceStore(), DefaultClockSkew)
	headers, _ := signer.SignRequest("m1", []byte(`{"a":1}`), "t", "a")
	err := verifier.VerifyRequest(headers, "m2", []byte(`{"a":1}`))
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestVerify_RejectsExpiredTimestamp(t *testing.T) {
	t.Parallel()
	signer, pub := newSigner(t, "k1")
	// Tight 1-second skew — we'll swap the header for an hour-old timestamp.
	verifier := newVerifier(t, "k1", pub, NewInMemoryNonceStore(), 1*time.Second)
	headers, _ := signer.SignRequest("m", []byte(`{}`), "t", "a")
	headers[HeaderTimestamp] = strconv.FormatInt(time.Now().Add(-1*time.Hour).Unix(), 10)
	err := verifier.VerifyRequest(headers, "m", []byte(`{}`))
	if !errors.Is(err, ErrTimestampExpired) {
		t.Errorf("want ErrTimestampExpired, got %v", err)
	}
}

func TestVerify_RejectsReplay(t *testing.T) {
	t.Parallel()
	signer, pub := newSigner(t, "k1")
	store := NewInMemoryNonceStore()
	verifier := newVerifier(t, "k1", pub, store, DefaultClockSkew)
	headers, _ := signer.SignRequest("m", []byte(`{}`), "t", "a")
	if err := verifier.VerifyRequest(headers, "m", []byte(`{}`)); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := verifier.VerifyRequest(headers, "m", []byte(`{}`))
	if !errors.Is(err, ErrNonceReplayed) {
		t.Errorf("want ErrNonceReplayed, got %v", err)
	}
}

func TestVerify_RejectsUntrustedKey(t *testing.T) {
	t.Parallel()
	signer, _ := newSigner(t, "rogue")
	// Trust a different key.
	_, otherPub := newSigner(t, "real")
	verifier := newVerifier(t, "real", otherPub, NewInMemoryNonceStore(), DefaultClockSkew)
	headers, _ := signer.SignRequest("m", []byte(`{}`), "t", "a")
	err := verifier.VerifyRequest(headers, "m", []byte(`{}`))
	if !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("want ErrUntrustedKey, got %v", err)
	}
}

func TestVerify_RejectsMissingHeader(t *testing.T) {
	t.Parallel()
	_, pub := newSigner(t, "k1")
	verifier := newVerifier(t, "k1", pub, NewInMemoryNonceStore(), DefaultClockSkew)
	err := verifier.VerifyRequest(map[string]string{}, "m", []byte(`{}`))
	if !errors.Is(err, ErrMissingHeaders) {
		t.Errorf("want ErrMissingHeaders, got %v", err)
	}
}

func TestNewVerifier_RejectsNonP256(t *testing.T) {
	t.Parallel()
	_, err := NewVerifier(map[string]*ecdsa.PublicKey{"bad": nil}, nil, 0)
	if !errors.Is(err, ErrInvalidPublicKey) {
		t.Errorf("want ErrInvalidPublicKey, got %v", err)
	}
}

func TestNewSigner_RejectsNilKey(t *testing.T) {
	t.Parallel()
	if _, err := NewSigner(nil, "k"); !errors.Is(err, ErrInvalidPrivateKey) {
		t.Errorf("want ErrInvalidPrivateKey, got %v", err)
	}
}

func TestInMemoryNonceStore_TTLExpiry(t *testing.T) {
	t.Parallel()
	// Inject a fast clock.
	store := &InMemoryNonceStore{seen: make(map[string]time.Time), now: func() time.Time { return time.Unix(1000, 0) }}
	seen, _ := store.SeenAndRecord("nonce-1", 1*time.Second)
	if seen {
		t.Fatal("fresh nonce should be unseen")
	}
	// Same instant — should see.
	seen, _ = store.SeenAndRecord("nonce-1", 1*time.Second)
	if !seen {
		t.Fatal("second read should report seen")
	}
	// Advance past TTL.
	store.now = func() time.Time { return time.Unix(1100, 0) }
	seen, _ = store.SeenAndRecord("nonce-1", 1*time.Second)
	if seen {
		t.Fatal("post-TTL read should report unseen")
	}
}

func TestHashParams_EmptyAndNullMatch(t *testing.T) {
	t.Parallel()
	a, err := hashParams(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hashParams([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("nil vs empty: %s != %s", a, b)
	}
	c, err := hashParams([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if a != c {
		t.Errorf("nil vs {}: %s != %s", a, c)
	}
}

func TestHashParams_CanonicalOrderStable(t *testing.T) {
	t.Parallel()
	a, _ := hashParams([]byte(`{"a":1,"b":2}`))
	b, _ := hashParams([]byte(`{"b":2,"a":1}`))
	if a != b {
		t.Errorf("key ordering should be normalised: %s != %s", a, b)
	}
}

func TestLoadPrivateKeyFromEnv_Roundtrip(t *testing.T) {
	// Generate and export.
	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	der, err := marshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSigningKey, base64.StdEncoding.EncodeToString(der))
	t.Setenv(EnvSigningKeyID, "test-k")
	got, id, err := LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadPrivateKeyFromEnv: %v", err)
	}
	if id != "test-k" {
		t.Errorf("id = %q want test-k", id)
	}
	if got == nil || got.X.Cmp(priv.X) != 0 || got.Y.Cmp(priv.Y) != 0 {
		t.Error("loaded key differs from original")
	}
}

func TestLoadPrivateKeyFromEnv_NotConfigured(t *testing.T) {
	// Unset both
	t.Setenv(EnvSigningKey, "")
	t.Setenv(EnvSigningKeyPath, "")
	_, _, err := LoadPrivateKeyFromEnv()
	if !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("want ErrSigningKeyNotConfigured, got %v", err)
	}
}

func TestLoadPrivateKeyFromEnv_Malformed(t *testing.T) {
	t.Setenv(EnvSigningKey, "not-a-key")
	_, _, err := LoadPrivateKeyFromEnv()
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Errorf("want ErrInvalidPrivateKey, got %v", err)
	}
}

func TestLoadTrustStoreFromEnv_Multi(t *testing.T) {
	clearTrustStoreEnv(t)
	priv1, _ := GeneratePrivateKey()
	priv2, _ := GeneratePrivateKey()
	pub1 := encodePub(t, &priv1.PublicKey)
	pub2 := encodePub(t, &priv2.PublicKey)
	t.Setenv(EnvTrustedKeyPrefix+"PROD", pub1)
	t.Setenv(EnvTrustedKeyPrefix+"DR", pub2)
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatalf("LoadTrustStoreFromEnv: %v", err)
	}
	if len(store) != 2 {
		t.Errorf("want 2 keys, got %d", len(store))
	}
	if _, ok := store["PROD"]; !ok {
		t.Error("PROD missing")
	}
	if _, ok := store["DR"]; !ok {
		t.Error("DR missing")
	}
}

func TestLoadTrustStoreFromEnv_MalformedReject(t *testing.T) {
	clearTrustStoreEnv(t)
	t.Setenv(EnvTrustedKeyPrefix+"BAD", "!!!not-base64!!!")
	_, err := LoadTrustStoreFromEnv()
	if !errors.Is(err, ErrInvalidPublicKey) {
		t.Errorf("want ErrInvalidPublicKey, got %v", err)
	}
}

// clearTrustStoreEnv unsets any stray CORDUM_MCP_INBOUND_TRUSTED_KEY_*
// entries from the test harness so each test sees a clean slate.
func clearTrustStoreEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		if name, _, ok := strings.Cut(e, "="); ok && strings.HasPrefix(name, EnvTrustedKeyPrefix) {
			t.Setenv(name, "")
		}
	}
}

// marshalECPrivateKey is a tiny helper that mirrors the one the CLI
// uses so the roundtrip test is self-contained.
func marshalECPrivateKey(priv *ecdsa.PrivateKey) ([]byte, error) {
	return x509MarshalECPrivateKey(priv)
}

// encodePub returns the base64 SPKI encoding of pub.
func encodePub(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	der, err := x509MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
