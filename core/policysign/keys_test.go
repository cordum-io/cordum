package policysign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func generatePEMPrivate(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return pub, priv, pemStr
}

func generatePEMPublic(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// clearPolicyEnv isolates tests from any stray env vars in the test harness.
func clearPolicyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		EnvSigningKey, EnvSigningKeyPath, EnvSigningKeyID, EnvDevSigningSeed,
		envLegacyPublicKey, envLegacyKeyID,
	} {
		t.Setenv(name, "")
	}
	for _, entry := range os.Environ() {
		name, _, _ := splitEnvEntry(entry)
		if len(name) > len(EnvPublicKeyPrefix) && name[:len(EnvPublicKeyPrefix)] == EnvPublicKeyPrefix {
			t.Setenv(name, "")
		}
	}
}

func splitEnvEntry(entry string) (string, string, bool) {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i], entry[i+1:], true
		}
	}
	return entry, "", false
}

func TestLoadPrivateKeyFromEnv_PEM(t *testing.T) {
	clearPolicyEnv(t)
	_, priv, pemStr := generatePEMPrivate(t)
	t.Setenv(EnvSigningKey, pemStr)
	t.Setenv(EnvSigningKeyID, "primary")
	got, id, err := LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id != "primary" {
		t.Errorf("id = %q want primary", id)
	}
	if len(got) != ed25519.PrivateKeySize {
		t.Fatalf("key size = %d", len(got))
	}
	if !priv.Equal(got) {
		t.Error("keys differ")
	}
}

func TestLoadPrivateKeyFromEnv_Base64(t *testing.T) {
	clearPolicyEnv(t)
	_, priv, _ := generatePEMPrivate(t)
	t.Setenv(EnvSigningKey, base64.StdEncoding.EncodeToString(priv))
	got, id, err := LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id != DefaultKeyID {
		t.Errorf("id = %q want %q", id, DefaultKeyID)
	}
	if !priv.Equal(got) {
		t.Error("keys differ")
	}
}

func TestLoadPrivateKeyFromEnv_Seed(t *testing.T) {
	clearPolicyEnv(t)
	_, priv, _ := generatePEMPrivate(t)
	seed := priv.Seed()
	t.Setenv(EnvSigningKey, base64.StdEncoding.EncodeToString(seed))
	got, _, err := LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !priv.Equal(got) {
		t.Error("seed-derived key differs from original")
	}
}

func TestLoadPrivateKeyFromEnv_Path(t *testing.T) {
	clearPolicyEnv(t)
	_, _, pemStr := generatePEMPrivate(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, []byte(pemStr), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSigningKeyPath, path)
	_, _, err := LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
}

func TestLoadPrivateKeyFromEnv_NotConfigured(t *testing.T) {
	clearPolicyEnv(t)
	_, _, err := LoadPrivateKeyFromEnv()
	if !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Fatalf("want ErrSigningKeyNotConfigured, got %v", err)
	}
}

func TestLoadPrivateKeyFromEnv_DevSeedFallback(t *testing.T) {
	clearPolicyEnv(t)
	t.Setenv(EnvSigningKeyID, "dev-local")
	t.Setenv(EnvDevSigningSeed, "cordum-local-policy-signing-v1")
	got, id, err := LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id != "dev-local" {
		t.Fatalf("id = %q want dev-local", id)
	}
	if !derivePrivateKeyFromSeed("cordum-local-policy-signing-v1").Equal(got) {
		t.Fatal("derived private key mismatch")
	}
}

func TestLoadPrivateKeyFromEnv_Malformed(t *testing.T) {
	clearPolicyEnv(t)
	t.Setenv(EnvSigningKey, "not-a-key")
	_, _, err := LoadPrivateKeyFromEnv()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("want ErrInvalidPrivateKey, got %v", err)
	}
}

func TestLoadPrivateKeyFromEnv_WrongSize(t *testing.T) {
	clearPolicyEnv(t)
	t.Setenv(EnvSigningKey, base64.StdEncoding.EncodeToString([]byte("short")))
	_, _, err := LoadPrivateKeyFromEnv()
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("want ErrInvalidPrivateKey, got %v", err)
	}
}

func TestLoadTrustStoreFromEnv_Multi(t *testing.T) {
	clearPolicyEnv(t)
	pub1, _, _ := generatePEMPrivate(t)
	pub2, _, _ := generatePEMPrivate(t)
	t.Setenv(EnvPublicKeyPrefix+"PRIMARY", base64.StdEncoding.EncodeToString(pub1))
	t.Setenv(EnvPublicKeyPrefix+"SECONDARY", base64.StdEncoding.EncodeToString(pub2))
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("len = %d want 2", store.Len())
	}
	if k, ok := store.Lookup("PRIMARY"); !ok || !pub1.Equal(k) {
		t.Error("PRIMARY not found or mismatched")
	}
	if k, ok := store.Lookup("secondary"); !ok || !pub2.Equal(k) {
		t.Error("SECONDARY not found via case-insensitive lookup")
	}
}

func TestLoadTrustStoreFromEnv_Legacy(t *testing.T) {
	clearPolicyEnv(t)
	pub, _, _ := generatePEMPrivate(t)
	t.Setenv(envLegacyPublicKey, base64.StdEncoding.EncodeToString(pub))
	t.Setenv(envLegacyKeyID, "rotating")
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := store.Lookup("rotating"); !ok {
		t.Fatal("legacy key id missing")
	}
}

func TestLoadTrustStoreFromEnv_LegacyDefaultID(t *testing.T) {
	clearPolicyEnv(t)
	pub, _, _ := generatePEMPrivate(t)
	t.Setenv(envLegacyPublicKey, base64.StdEncoding.EncodeToString(pub))
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := store.Lookup(DefaultKeyID); !ok {
		t.Fatal("legacy key missing default id")
	}
}

func TestLoadTrustStoreFromEnv_Malformed(t *testing.T) {
	clearPolicyEnv(t)
	t.Setenv(EnvPublicKeyPrefix+"BAD", "!!!not-base64!!!")
	_, err := LoadTrustStoreFromEnv()
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("want ErrInvalidPublicKey, got %v", err)
	}
}

func TestLoadTrustStoreFromEnv_EmptySkipped(t *testing.T) {
	clearPolicyEnv(t)
	t.Setenv(EnvPublicKeyPrefix+"EMPTY", "   ")
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if store.Len() != 0 {
		t.Errorf("store should ignore blank values, got %d", store.Len())
	}
}

func TestLoadTrustStoreFromEnv_DevSeedFallback(t *testing.T) {
	clearPolicyEnv(t)
	t.Setenv(EnvSigningKeyID, "dev-local")
	t.Setenv(EnvDevSigningSeed, "cordum-local-policy-signing-v1")
	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("len = %d want 1", store.Len())
	}
	pub, ok := store.Lookup("dev-local")
	if !ok {
		t.Fatal("dev-local key missing")
	}
	want := derivePrivateKeyFromSeed("cordum-local-policy-signing-v1").Public().(ed25519.PublicKey)
	if !want.Equal(pub) {
		t.Fatal("derived public key mismatch")
	}
}

func TestParsePublicKey_PEM(t *testing.T) {
	pub, _, _ := generatePEMPrivate(t)
	pemStr := generatePEMPublic(t, pub)
	got, err := parsePublicKey(pemStr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !pub.Equal(got) {
		t.Error("keys differ")
	}
}

func TestParsePublicKey_WrongSize(t *testing.T) {
	if _, err := parsePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("want ErrInvalidPublicKey, got %v", err)
	}
}

func TestTrustStore_AddRejectsInvalid(t *testing.T) {
	store := NewTrustStore()
	if err := store.Add("", make(ed25519.PublicKey, 32)); !errors.Is(err, ErrEmptyKeyID) {
		t.Errorf("want ErrEmptyKeyID, got %v", err)
	}
	if err := store.Add("id", make(ed25519.PublicKey, 3)); !errors.Is(err, ErrInvalidPublicKey) {
		t.Errorf("want ErrInvalidPublicKey, got %v", err)
	}
}

func TestTrustStore_IDsSorted(t *testing.T) {
	store := NewTrustStore()
	pub, _, _ := generatePEMPrivate(t)
	_ = store.Add("beta", pub)
	_ = store.Add("alpha", pub)
	_ = store.Add("gamma", pub)
	ids := store.IDs()
	want := []string{"alpha", "beta", "gamma"}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids = %v want %v", ids, want)
			break
		}
	}
}
