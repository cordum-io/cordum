//go:build capproduction

package capproduction

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type productionTLS struct {
	server     *tls.Config
	client     *tls.Config
	caPath     string
	clientCert string
	clientKey  string
}

func newProductionTLS(t *testing.T) productionTLS {
	t.Helper()
	dir := t.TempDir()
	caKey := generateRSA(t)
	ca := certificateTemplate(t, true, nil)
	ca.Subject = pkix.Name{CommonName: "CAP production test CA"}
	caDER := createCertificate(t, ca, ca, &caKey.PublicKey, caKey)
	serverCert := issueCertificate(t, ca, caKey, true)
	clientCert := issueCertificate(t, ca, caKey, false)
	caPool := x509.NewCertPool()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if !caPool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append test CA")
	}
	caPath := filepath.Join(dir, "ca.pem")
	clientCertPath := filepath.Join(dir, "client.pem")
	clientKeyPath := filepath.Join(dir, "client-key.pem")
	writeFile(t, caPath, caPEM)
	writeTLSKeyPair(t, clientCertPath, clientKeyPath, clientCert)
	return productionTLS{
		server: &tls.Config{
			MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert.cert},
			ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: caPool,
		},
		client: &tls.Config{
			MinVersion: tls.VersionTLS12, ServerName: "localhost", RootCAs: caPool,
			Certificates: []tls.Certificate{clientCert.cert},
		},
		caPath: caPath, clientCert: clientCertPath, clientKey: clientKeyPath,
	}
}

type issuedCertificate struct {
	cert tls.Certificate
	der  []byte
	key  *rsa.PrivateKey
}

func issueCertificate(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, server bool) issuedCertificate {
	t.Helper()
	key := generateRSA(t)
	template := certificateTemplate(t, false, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der := createCertificate(t, template, ca, &key.PublicKey, caKey)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load issued certificate: %v", err)
	}
	return issuedCertificate{cert: cert, der: der, key: key}
}

func certificateTemplate(t *testing.T, isCA bool, usages []x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatalf("certificate serial: %v", err)
	}
	return &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "cap-production"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: isCA, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: usages,
	}
}

func createCertificate(
	t *testing.T, template, parent *x509.Certificate,
	public *rsa.PublicKey, signer *rsa.PrivateKey,
) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, public, signer)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return der
}

func generateRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func writeTLSKeyPair(t *testing.T, certPath, keyPath string, issued issuedCertificate) {
	t.Helper()
	writeFile(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issued.der}))
	writeFile(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(issued.key)}))
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
