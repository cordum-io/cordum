package redisutil

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

func TestParseOptionsNoTLS(t *testing.T) {
	opts, err := ParseOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig != nil {
		t.Fatalf("expected nil TLS config")
	}
}

func TestParseOptionsInsecureTLS(t *testing.T) {
	t.Setenv(envRedisTLSInsecure, "true")
	opts, err := ParseOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig == nil || !opts.TLSConfig.InsecureSkipVerify {
		t.Fatalf("expected insecure TLS config")
	}
}

func TestParseOptionsTLSCA(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	t.Setenv(envRedisTLSCA, certPath)
	t.Setenv(envRedisTLSCert, certPath)
	t.Setenv(envRedisTLSKey, keyPath)

	opts, err := ParseOptions("redis://localhost:6379")
	if err != nil {
		t.Fatalf("ParseOptions error: %v", err)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.RootCAs == nil {
		t.Fatalf("expected root CAs set")
	}
	if len(opts.TLSConfig.Certificates) != 1 {
		t.Fatalf("expected client certificate")
	}
}

func TestParseOptionsMissingKey(t *testing.T) {
	dir := t.TempDir()
	certPath, _ := writeTempCert(t, dir)
	t.Setenv(envRedisTLSCert, certPath)

	_, err := ParseOptions("redis://localhost:6379")
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
