package policysign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func resetBootEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvStrictMode, "")
	t.Setenv(EnvSigningKey, "")
	t.Setenv(EnvSigningKeyPath, "")
	t.Setenv(EnvSigningKeyID, "")
	t.Setenv(envLegacyPublicKey, "")
	t.Setenv(envLegacyKeyID, "")
}

func TestCheckGatewayBoot_OffOK(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "off")
	if err := CheckGatewayBoot(); err != nil {
		t.Fatalf("off + no key should pass, got %v", err)
	}
}

func TestCheckGatewayBoot_WarnNoKeyWarns(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "warn")
	if err := CheckGatewayBoot(); err != nil {
		t.Fatalf("warn + no key should pass (with log), got %v", err)
	}
}

func TestCheckGatewayBoot_EnforceNoKeyFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "enforce")
	err := CheckGatewayBoot()
	if !errors.Is(err, ErrEnforceMissingSigningKey) {
		t.Fatalf("want ErrEnforceMissingSigningKey, got %v", err)
	}
}

func TestCheckGatewayBoot_EnforceWithKeyOK(t *testing.T) {
	resetBootEnv(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvStrictMode, "enforce")
	t.Setenv(EnvSigningKey, base64.StdEncoding.EncodeToString(priv))
	t.Setenv(EnvSigningKeyID, "p1")
	if err := CheckGatewayBoot(); err != nil {
		t.Fatalf("enforce + key should pass, got %v", err)
	}
}

func TestCheckGatewayBoot_BadKeyFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "warn")
	t.Setenv(EnvSigningKey, "!!!malformed!!!")
	err := CheckGatewayBoot()
	if err == nil {
		t.Fatal("malformed key should fail at boot")
	}
}

func TestCheckKernelBoot_OffOK(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "off")
	if err := CheckKernelBoot(); err != nil {
		t.Fatalf("off + no trust store should pass, got %v", err)
	}
}

func TestCheckKernelBoot_WarnEmptyStorePasses(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "warn")
	if err := CheckKernelBoot(); err != nil {
		t.Fatalf("warn + empty store should pass, got %v", err)
	}
}

func TestCheckKernelBoot_EnforceEmptyStoreFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "enforce")
	err := CheckKernelBoot()
	if !errors.Is(err, ErrEnforceMissingTrustStore) {
		t.Fatalf("want ErrEnforceMissingTrustStore, got %v", err)
	}
}

func TestCheckKernelBoot_EnforceWithStoreOK(t *testing.T) {
	resetBootEnv(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvStrictMode, "enforce")
	t.Setenv(EnvPublicKeyPrefix+"P1", base64.StdEncoding.EncodeToString(pub))
	if err := CheckKernelBoot(); err != nil {
		t.Fatalf("enforce + trust store should pass, got %v", err)
	}
}

func TestCheckKernelBoot_EnforceInvalidKeyFails(t *testing.T) {
	resetBootEnv(t)
	t.Setenv(EnvStrictMode, "enforce")
	t.Setenv(EnvPublicKeyPrefix+"BAD", "!!!not-base64!!!")
	err := CheckKernelBoot()
	if err == nil {
		t.Fatal("malformed trust key should fail")
	}
}
