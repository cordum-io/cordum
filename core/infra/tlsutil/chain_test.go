package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test matrix:
//   1. happy path — client cert signed by the CA → VerifyChain returns nil
//   2. chain drift — client cert signed by CA_v1, operator installed CA_v2 → ChainError
//   3. expired cert — chain valid but NotAfter in the past → ChainError
//   4. non-PEM input — path exists but not a cert → wrapped read error
//   5. missing file — path does not exist → wrapped read error
//
// The core failure mode that motivated this helper is case 2 — that's the
// one that tanked the stack in a way operators couldn't diagnose from the
// downstream "tls: certificate required" error. Every field on ChainError
// is asserted so a future string-tweaks PR can't silently drop an operator
// signal.

func TestVerifyChain_HappyPath(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, _ := mustIssueChain(t, dir, "happy-ca", "cordum-client", time.Hour)
	if err := VerifyChain(certPath, caPath, RoleClient); err != nil {
		t.Fatalf("expected nil error for matching chain, got %v", err)
	}
}

func TestVerifyChain_DriftBetweenCAAndClient(t *testing.T) {
	dir := t.TempDir()
	// Issue CA_v1 + client under v1. Keep only the client.
	_, clientPath, _ := mustIssueChain(t, dir, "ca-v1", "cordum-client", time.Hour)
	// Issue CA_v2 + its own client (discarded). CA_v2 does NOT chain to the
	// v1-signed client — this is the production-seen drift.
	ca2Dir := t.TempDir()
	caV2Path, _, _ := mustIssueChain(t, ca2Dir, "ca-v2", "some-other", time.Hour)

	err := VerifyChain(clientPath, caV2Path, RoleClient)
	if err == nil {
		t.Fatalf("expected ChainError on mismatched CA")
	}
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ChainError, got %T: %v", err, err)
	}
	msg := err.Error()
	// The operator-facing message MUST cite both files + both issuer DNs +
	// the remediation command. Drop any of these and diagnosis regresses.
	mustContain(t, msg, clientPath)
	mustContain(t, msg, caV2Path)
	mustContain(t, msg, "CN=ca-v1")
	mustContain(t, msg, "CN=ca-v2")
	mustContain(t, msg, "cordumctl generate-certs --force")
	mustContain(t, msg, "does not chain")
}

func TestVerifyChain_ExpiredCert(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, _ := mustIssueChain(t, dir, "expired-ca", "cordum-client", -time.Hour)
	err := VerifyChain(certPath, caPath, RoleClient)
	if err == nil {
		t.Fatalf("expected ChainError on expired cert")
	}
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ChainError, got %T: %v", err, err)
	}
	mustContain(t, err.Error(), "cordumctl generate-certs --force")
}

func TestVerifyChain_MissingCert(t *testing.T) {
	dir := t.TempDir()
	caPath, _, _ := mustIssueChain(t, dir, "missing-cert-ca", "cordum-client", time.Hour)
	err := VerifyChain(filepath.Join(dir, "no-such-file.crt"), caPath, RoleClient)
	if err == nil {
		t.Fatalf("expected error for missing cert path")
	}
	if strings.Contains(err.Error(), "cordumctl") {
		// Missing-file is distinct from chain-drift; it should not claim the
		// fix is re-generating certs — the operator may have misconfigured
		// the env var and needs a different hint.
		t.Fatalf("missing-file error should NOT suggest generate-certs, got %v", err)
	}
}

func TestVerifyChain_NotPEM(t *testing.T) {
	dir := t.TempDir()
	caPath, _, _ := mustIssueChain(t, dir, "garbage-ca", "cordum-client", time.Hour)
	garbagePath := filepath.Join(dir, "not-a-cert.crt")
	if werr := os.WriteFile(garbagePath, []byte("this is not PEM"), 0o600); werr != nil {
		t.Fatalf("seed garbage file: %v", werr)
	}
	err := VerifyChain(garbagePath, caPath, RoleClient)
	if err == nil {
		t.Fatalf("expected error for non-PEM cert file")
	}
	mustContain(t, err.Error(), "no CERTIFICATE block")
}

// ---------------------------------------------------------------------------
// Helpers — issue a self-signed CA + client cert pair into the given dir.
// ---------------------------------------------------------------------------

func mustIssueChain(t *testing.T, dir, caCN, clientCN string, ttl time.Duration) (caPath, certPath, keyPath string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ca key: %v", err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: caCN},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("self-sign ca: %v", err)
	}
	caPath = filepath.Join(dir, "ca.crt")
	writePEM(t, caPath, "CERTIFICATE", caDER)

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen client key: %v", err)
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: clientCN},
		NotBefore:    now.Add(-time.Minute),
		// When ttl is negative the resulting cert is already expired — used
		// by TestVerifyChain_ExpiredCert.
		NotAfter:    now.Add(ttl),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caTmpl, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("sign client: %v", err)
	}
	certPath = filepath.Join(dir, "client.crt")
	writePEM(t, certPath, "CERTIFICATE", certDER)

	keyDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	keyPath = filepath.Join(dir, "client.key")
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)
	return caPath, certPath, keyPath
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	buf := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write pem %s: %v", path, err)
	}
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got:\n%s", want, got)
	}
}
