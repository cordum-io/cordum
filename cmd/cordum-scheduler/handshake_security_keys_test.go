package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
)

func TestLoadHandshakeSecurityConfigValidatesPinnedP256Key(t *testing.T) {
	setValidHandshakeSecurityEnv(t, "warn")
	cfg, err := loadHandshakeSecurityConfig()
	if err != nil {
		t.Fatalf("load valid config: %v", err)
	}
	if cfg.mode != scheduler.HandshakeModeWarn || cfg.audience != scheduler.WorkerHandshakeAudience {
		t.Fatalf("unexpected mode/audience: %q %q", cfg.mode, cfg.audience)
	}
	if cfg.schedulerPrivateKey == nil || cfg.publicKeySHA256 == "" {
		t.Fatal("validated private key and public-key fingerprint are required")
	}
	if strings.Contains(string(cfg.schedulerPublicKeyPEM), "PRIVATE") {
		t.Fatal("public-key export must never contain private material")
	}
}

func TestLoadHandshakeSecurityConfigRejectsMismatchedPublicKey(t *testing.T) {
	setValidHandshakeSecurityEnv(t, "enforce")
	_, publicPath := writeHandshakeKeyPair(t, elliptic.P256())
	t.Setenv(scheduler.EnvHandshakePublicKeyFile, publicPath)
	if _, err := loadHandshakeSecurityConfig(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched public export must refuse boot; got %v", err)
	}
}

func TestLoadHandshakeSecurityConfigRejectsWrongCurve(t *testing.T) {
	clearHandshakeSecurityEnv(t)
	t.Setenv(scheduler.EnvHandshakeMode, "warn")
	setHandshakeIdentityEnv(t)
	privatePath, publicPath := writeHandshakeKeyPair(t, elliptic.P384())
	t.Setenv(scheduler.EnvHandshakePrivateKeyFile, privatePath)
	t.Setenv(scheduler.EnvHandshakePublicKeyFile, publicPath)
	if _, err := loadHandshakeSecurityConfig(); err == nil || !strings.Contains(err.Error(), "P-256") {
		t.Fatalf("non-P-256 key must refuse boot; got %v", err)
	}
}

func TestLoadHandshakeSecurityConfigRejectsMalformedOrHugePrivateKey(t *testing.T) {
	for name, data := range map[string][]byte{
		"malformed": []byte("not a key"),
		"oversized": make([]byte, maxHandshakePrivateKeyBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			setValidHandshakeSecurityEnv(t, "warn")
			path := t.TempDir() + "/private.pem"
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(scheduler.EnvHandshakePrivateKeyFile, path)
			if _, err := loadHandshakeSecurityConfig(); err == nil {
				t.Fatalf("%s private key must refuse boot", name)
			}
		})
	}
}

func TestLoadHandshakeSecurityConfigAcceptsSEC1P256PrivateKey(t *testing.T) {
	setValidHandshakeSecurityEnv(t, "enforce")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := t.TempDir() + "/private.pem"
	writePEM(t, privatePath, "EC PRIVATE KEY", der, 0o600)
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := t.TempDir() + "/public.pem"
	writePEM(t, publicPath, "PUBLIC KEY", publicDER, 0o644)
	t.Setenv(scheduler.EnvHandshakePrivateKeyFile, privatePath)
	t.Setenv(scheduler.EnvHandshakePublicKeyFile, publicPath)
	if _, err := loadHandshakeSecurityConfig(); err != nil {
		t.Fatalf("valid SEC1 P-256 key rejected: %v", err)
	}
}
