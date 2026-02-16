package runtime

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNATSTLSConfigFromEnv_None(t *testing.T) {
	t.Setenv("NATS_TLS_CA", "")
	t.Setenv("NATS_TLS_CERT", "")
	t.Setenv("NATS_TLS_KEY", "")
	t.Setenv("NATS_TLS_INSECURE", "")
	t.Setenv("NATS_TLS_SERVER_NAME", "")

	cfg, err := NATSTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil config when no TLS vars set")
	}
}

func TestNATSTLSConfigFromEnv_Insecure(t *testing.T) {
	t.Setenv("NATS_TLS_CA", "")
	t.Setenv("NATS_TLS_CERT", "")
	t.Setenv("NATS_TLS_KEY", "")
	t.Setenv("NATS_TLS_INSECURE", "1")
	t.Setenv("NATS_TLS_SERVER_NAME", "")

	cfg, err := NATSTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=true")
	}
}

func TestNATSTLSConfigFromEnv_WithCA(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, keyPath := generateTestCerts(t, dir)

	t.Setenv("NATS_TLS_CA", caPath)
	t.Setenv("NATS_TLS_CERT", certPath)
	t.Setenv("NATS_TLS_KEY", keyPath)
	t.Setenv("NATS_TLS_INSECURE", "")
	t.Setenv("NATS_TLS_SERVER_NAME", "nats")

	cfg, err := NATSTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs to be populated")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
	}
	if cfg.ServerName != "nats" {
		t.Fatalf("expected ServerName=nats, got %s", cfg.ServerName)
	}
}

func TestNATSTLSConfigFromEnv_MissingKey(t *testing.T) {
	dir := t.TempDir()
	_, certPath, _ := generateTestCerts(t, dir)

	t.Setenv("NATS_TLS_CA", "")
	t.Setenv("NATS_TLS_CERT", certPath)
	t.Setenv("NATS_TLS_KEY", "") // key is missing
	t.Setenv("NATS_TLS_INSECURE", "")
	t.Setenv("NATS_TLS_SERVER_NAME", "")

	_, err := NATSTLSConfigFromEnv()
	if err == nil {
		t.Fatal("expected error when cert set without key")
	}
}

func TestNATSTLSConfigFromEnv_BadCA(t *testing.T) {
	dir := t.TempDir()
	badCA := filepath.Join(dir, "bad-ca.crt")
	if err := os.WriteFile(badCA, []byte("not a cert"), 0o600); err != nil {
		t.Fatalf("write bad ca: %v", err)
	}

	t.Setenv("NATS_TLS_CA", badCA)
	t.Setenv("NATS_TLS_CERT", "")
	t.Setenv("NATS_TLS_KEY", "")
	t.Setenv("NATS_TLS_INSECURE", "")
	t.Setenv("NATS_TLS_SERVER_NAME", "")

	_, err := NATSTLSConfigFromEnv()
	if err == nil {
		t.Fatal("expected error for bad CA file")
	}
}

func TestNATSTLSConfigFromEnv_ServerName(t *testing.T) {
	t.Setenv("NATS_TLS_CA", "")
	t.Setenv("NATS_TLS_CERT", "")
	t.Setenv("NATS_TLS_KEY", "")
	t.Setenv("NATS_TLS_INSECURE", "")
	t.Setenv("NATS_TLS_SERVER_NAME", "custom-server")

	cfg, err := NATSTLSConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ServerName != "custom-server" {
		t.Fatalf("expected ServerName=custom-server, got %s", cfg.ServerName)
	}
}

// generateTestCerts creates a self-signed CA and a client keypair in dir.
// Returns (caPath, certPath, keyPath).
func generateTestCerts(t *testing.T, dir string) (string, string, string) {
	t.Helper()

	// CA key + cert
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	caPath := filepath.Join(dir, "ca.crt")
	writePEM(t, caPath, "CERTIFICATE", caDER)

	// Client key + cert signed by CA
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("client cert: %v", err)
	}

	certPath := filepath.Join(dir, "tls.crt")
	writePEM(t, certPath, "CERTIFICATE", clientDER)

	keyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath := filepath.Join(dir, "tls.key")
	writePEM(t, keyPath, "PRIVATE KEY", keyDER)

	return caPath, certPath, keyPath
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("pem encode %s: %v", path, err)
	}
}
