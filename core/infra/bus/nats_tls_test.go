package bus

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNATSTLSConfigFromEnvNone(t *testing.T) {
	cfg, err := natsTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config")
	}
}

func TestNATSTLSConfigFromEnvInsecure(t *testing.T) {
	t.Setenv(envNATSTLSInsecure, "true")
	cfg, err := natsTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatalf("expected insecure TLS config")
	}
}

func TestNATSTLSConfigFromEnvWithCAAndCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	t.Setenv(envNATSTLSCA, certPath)
	t.Setenv(envNATSTLSCert, certPath)
	t.Setenv(envNATSTLSKey, keyPath)

	cfg, err := natsTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatalf("expected root CAs set")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected client certificate")
	}
}

func TestNATSTLSConfigFromEnvMissingKey(t *testing.T) {
	dir := t.TempDir()
	certPath, _ := writeTempCert(t, dir)
	t.Setenv(envNATSTLSCert, certPath)

	_, err := natsTLSConfigFromEnv()
	if err == nil {
		t.Fatalf("expected error for missing key")
	}
}

func writeTempCert(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}
