package certgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAll(t *testing.T) {
	dir := t.TempDir()
	err := GenerateAll(Options{BaseDir: dir, Days: 30})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}

	// Verify all expected files exist.
	for _, rel := range []string{
		"ca/ca.crt", "ca/ca.key",
		"server/tls.crt", "server/tls.key",
		"client/tls.crt", "client/tls.key",
	} {
		path := filepath.Join(dir, rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// Verify server cert is signed by CA.
	caCert := loadCert(t, filepath.Join(dir, "ca/ca.crt"))
	serverCert := loadCert(t, filepath.Join(dir, "server/tls.crt"))
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := serverCert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("server cert chain validation failed: %v", err)
	}

	// Verify client cert is signed by CA.
	clientCert := loadCert(t, filepath.Join(dir, "client/tls.crt"))
	if _, err := clientCert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client cert chain validation failed: %v", err)
	}
}

func TestGenerateCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if err := GenerateCA(dir, 30, false); err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	cert := loadCert(t, filepath.Join(dir, "ca.crt"))
	if !cert.IsCA {
		t.Fatal("expected IsCA=true")
	}
	if cert.BasicConstraintsValid == false {
		t.Fatal("expected BasicConstraintsValid")
	}
	key := loadKey(t, filepath.Join(dir, "ca.key"))
	if key.Curve != elliptic.P256() {
		t.Fatal("expected P-256 curve")
	}
}

func TestServerCert(t *testing.T) {
	caDir := filepath.Join(t.TempDir(), "ca")
	if err := GenerateCA(caDir, 30, false); err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	serverDir := filepath.Join(t.TempDir(), "server")
	sans := []string{"localhost", "nats", "127.0.0.1", "::1"}
	if err := GenerateServerCert(caDir, serverDir, 30, sans, false); err != nil {
		t.Fatalf("GenerateServerCert: %v", err)
	}

	cert := loadCert(t, filepath.Join(serverDir, "tls.crt"))

	// Chain validation.
	caCert := loadCert(t, filepath.Join(caDir, "ca.crt"))
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("chain validation failed: %v", err)
	}

	// EKU.
	hasServerAuth := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Fatal("missing ExtKeyUsageServerAuth")
	}

	// SANs.
	hasDNS := func(name string) bool {
		for _, dn := range cert.DNSNames {
			if dn == name {
				return true
			}
		}
		return false
	}
	if !hasDNS("localhost") {
		t.Error("missing SAN: localhost")
	}
	if !hasDNS("nats") {
		t.Error("missing SAN: nats")
	}
	hasIP := func(target string) bool {
		ip := net.ParseIP(target)
		for _, sanIP := range cert.IPAddresses {
			if sanIP.Equal(ip) {
				return true
			}
		}
		return false
	}
	if !hasIP("127.0.0.1") {
		t.Error("missing IP SAN: 127.0.0.1")
	}
	if !hasIP("::1") {
		t.Error("missing IP SAN: ::1")
	}
}

func TestClientCert(t *testing.T) {
	caDir := filepath.Join(t.TempDir(), "ca")
	if err := GenerateCA(caDir, 30, false); err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	clientDir := filepath.Join(t.TempDir(), "client")
	if err := GenerateClientCert(caDir, clientDir, "test-client", 30, false); err != nil {
		t.Fatalf("GenerateClientCert: %v", err)
	}

	cert := loadCert(t, filepath.Join(clientDir, "tls.crt"))

	// Chain validation.
	caCert := loadCert(t, filepath.Join(caDir, "ca.crt"))
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("chain validation failed: %v", err)
	}

	// CN.
	if cert.Subject.CommonName != "test-client" {
		t.Fatalf("expected CN=test-client, got %s", cert.Subject.CommonName)
	}

	// EKU.
	hasClientAuth := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	if !hasClientAuth {
		t.Fatal("missing ExtKeyUsageClientAuth")
	}
}

func TestForceOverwrite(t *testing.T) {
	dir := t.TempDir()

	// First generation should succeed.
	if err := GenerateAll(Options{BaseDir: dir, Days: 30}); err != nil {
		t.Fatalf("first GenerateAll: %v", err)
	}

	// Second generation without force should fail.
	if err := GenerateAll(Options{BaseDir: dir, Days: 30}); err == nil {
		t.Fatal("expected error without force")
	}

	// Second generation with force should succeed.
	if err := GenerateAll(Options{BaseDir: dir, Days: 30, Force: true}); err != nil {
		t.Fatalf("force GenerateAll: %v", err)
	}
}

func TestDefaultSANs(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateAll(Options{BaseDir: dir, Days: 30}); err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}

	cert := loadCert(t, filepath.Join(dir, "server/tls.crt"))

	// Verify all default SANs are present.
	for _, expected := range DefaultSANs {
		found := false
		for _, dn := range cert.DNSNames {
			if dn == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing default SAN: %s", expected)
		}
	}

	// Verify loopback IPs.
	for _, ipStr := range []string{"127.0.0.1", "::1"} {
		ip := net.ParseIP(ipStr)
		found := false
		for _, sanIP := range cert.IPAddresses {
			if sanIP.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing IP SAN: %s", ipStr)
		}
	}
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func loadCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert %s: %v", path, err)
	}
	return cert
}

func loadKey(t *testing.T, path string) *ecdsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("no PEM block in %s", path)
	}
	raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse key %s: %v", path, err)
	}
	key, ok := raw.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("key %s is not ecdsa", path)
	}
	return key
}
