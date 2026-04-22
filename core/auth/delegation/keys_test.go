package delegation

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadSigningKeyFromEnv_Base64RoundTrip(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	t.Setenv(envDelegationActiveKeyID, "DLG_2")
	t.Setenv(envDelegationPrivateKey, base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv(envDelegationPublicKeyStem+"DLG_2", "ed25519:"+base64.StdEncoding.EncodeToString(publicKey))

	signingKey, err := LoadSigningKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadSigningKeyFromEnv() error = %v", err)
	}
	if signingKey.KID != "dlg-2" {
		t.Fatalf("LoadSigningKeyFromEnv() kid = %q, want dlg-2", signingKey.KID)
	}

	keyring, err := LoadVerificationKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadVerificationKeysFromEnv() error = %v", err)
	}

	message := []byte("delegation-roundtrip")
	signature := ed25519.Sign(signingKey.PrivateKey, message)
	if !ed25519.Verify(keyring["dlg-2"], message, signature) {
		t.Fatal("generated signature did not verify")
	}
}

func TestLoadSigningKeyFromEnv_PKCS8PEM(t *testing.T) {
	generated, err := GenerateSigningKey("dlg-9")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}
	pemBytes, err := EncodePrivateKeyPEM(generated.PrivateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM() error = %v", err)
	}

	t.Setenv(envDelegationPrivateKey, string(pemBytes))
	t.Setenv(envDelegationActiveKeyID, "dlg-9")

	loaded, err := LoadSigningKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadSigningKeyFromEnv() error = %v", err)
	}
	if !reflect.DeepEqual([]byte(loaded.PrivateKey), []byte(generated.PrivateKey)) {
		t.Fatal("loaded private key does not match generated key")
	}
}

func TestLoadSigningKeyFromFileRequiresStrictPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not expose portable unix permission bits")
	}

	generated, err := GenerateSigningKey("dlg-1")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}
	pemBytes, err := EncodePrivateKeyPEM(generated.PrivateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "delegation.pem")
	if err := os.WriteFile(path, pemBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = LoadSigningKeyFromFile(path)
	if !errors.Is(err, ErrDelegationKeyPermissions) {
		t.Fatalf("LoadSigningKeyFromFile() error = %v, want ErrDelegationKeyPermissions", err)
	}
}

func TestLoadSigningKeyFromFileStrictFile(t *testing.T) {
	generated, err := GenerateSigningKey("dlg-1")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}
	pemBytes, err := EncodePrivateKeyPEM(generated.PrivateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "delegation.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := LoadSigningKeyFromFile(path)
	if err != nil {
		t.Fatalf("LoadSigningKeyFromFile() error = %v", err)
	}
	if !reflect.DeepEqual([]byte(loaded.PrivateKey), []byte(generated.PrivateKey)) {
		t.Fatal("loaded private key does not match generated key")
	}
}

func TestLoadVerificationKeysFromEnv_MultipleKids(t *testing.T) {
	keyA := bytes.Repeat([]byte{0x11}, ed25519.SeedSize)
	keyB := bytes.Repeat([]byte{0x22}, ed25519.SeedSize)
	pubA := ed25519.NewKeyFromSeed(keyA).Public().(ed25519.PublicKey)
	pubB := ed25519.NewKeyFromSeed(keyB).Public().(ed25519.PublicKey)

	t.Setenv(envDelegationPublicKeyStem+"PRIMARY", base64.StdEncoding.EncodeToString(pubA))
	t.Setenv(envDelegationPublicKeyStem+"ROTATED_2", base64.StdEncoding.EncodeToString(pubB))

	keyring, err := LoadVerificationKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadVerificationKeysFromEnv() error = %v", err)
	}
	if got := SortedKeyIDs(keyring); !reflect.DeepEqual(got, []string{"primary", "rotated-2"}) {
		t.Fatalf("SortedKeyIDs() = %v, want [primary rotated-2]", got)
	}
}

func TestSigningKeyStringRedactsPrivateMaterial(t *testing.T) {
	key, err := GenerateSigningKey("dlg-1")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}
	text := key.String()
	if strings.Contains(text, base64.StdEncoding.EncodeToString(key.PrivateKey)) {
		t.Fatalf("String() leaked private key material: %s", text)
	}
	if !strings.Contains(text, "<redacted>") {
		t.Fatalf("String() = %q, want redaction marker", text)
	}
}
