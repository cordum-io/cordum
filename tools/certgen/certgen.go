// Package certgen generates CA, server and client TLS certificates for Cordum.
//
// Dev environments use auto-generated self-signed certs with full verification
// (proper CA trust, correct SANs). Production uses operator-provided certs.
// The code paths are identical — no insecure flags needed.
package certgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	dirPerm  = 0o750
	keyPerm  = 0o600
	certPerm = 0o644
)

// DefaultSANs are the DNS names included in server certificates so that
// TLS verification passes for every service in the Docker network.
var DefaultSANs = []string{
	"localhost",
	"nats",
	"redis",
	"cordum-api-gateway",
	"safety-kernel",
	"scheduler",
	"workflow-engine",
	"context-engine",
	"dashboard",
}

// Options controls certificate generation behaviour.
type Options struct {
	// BaseDir is the root directory for generated certificate assets.
	// Sub-directories ca/, server/, client/ are created beneath it.
	BaseDir string

	// Days is the validity period for all generated certificates.
	// Defaults to 365 if zero.
	Days int

	// Force overwrites existing files when true.
	Force bool

	// SANs are additional DNS names to include in the server certificate.
	// DefaultSANs are always included.
	SANs []string
}

func (o *Options) days() int {
	if o.Days <= 0 {
		return 365
	}
	return o.Days
}

func (o *Options) serverSANs() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range DefaultSANs {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range o.SANs {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// GenerateAll generates a CA keypair, a server certificate (with SANs)
// and a client certificate, all under opts.BaseDir.
func GenerateAll(opts Options) error {
	caDir := filepath.Join(opts.BaseDir, "ca")
	serverDir := filepath.Join(opts.BaseDir, "server")
	clientDir := filepath.Join(opts.BaseDir, "client")

	if err := GenerateCA(caDir, opts.Days, opts.Force); err != nil {
		return fmt.Errorf("generate ca: %w", err)
	}
	if err := GenerateServerCert(caDir, serverDir, opts.days(), opts.serverSANs(), opts.Force); err != nil {
		return fmt.Errorf("generate server cert: %w", err)
	}
	if err := GenerateClientCert(caDir, clientDir, "cordum-client", opts.days(), opts.Force); err != nil {
		return fmt.Errorf("generate client cert: %w", err)
	}
	return nil
}

// GenerateCA creates a self-signed CA certificate and private key.
func GenerateCA(dir string, days int, force bool) error {
	if days <= 0 {
		days = 365
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if !force {
		if _, err := os.Stat(certPath); err == nil {
			return fmt.Errorf("file exists: %s (use force to overwrite)", certPath)
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ca key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Cordum Dev CA"},
			CommonName:   "Cordum Dev CA",
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create ca cert: %w", err)
	}

	if err := writeCert(certPath, der); err != nil {
		return err
	}
	return writeKey(keyPath, key)
}

// GenerateServerCert creates a server certificate signed by the CA.
func GenerateServerCert(caDir, serverDir string, days int, sans []string, force bool) error {
	if days <= 0 {
		days = 365
	}
	caCert, caKey, err := loadCA(caDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serverDir, dirPerm); err != nil {
		return fmt.Errorf("mkdir %s: %w", serverDir, err)
	}

	certPath := filepath.Join(serverDir, "tls.crt")
	keyPath := filepath.Join(serverDir, "tls.key")
	if !force {
		if _, err := os.Stat(certPath); err == nil {
			return fmt.Errorf("file exists: %s (use force to overwrite)", certPath)
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return err
	}

	var dnsNames []string
	var ips []net.IP
	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			ips = append(ips, ip)
		} else {
			dnsNames = append(dnsNames, san)
		}
	}
	// Always include loopback IPs.
	ips = appendUniqueIP(ips, net.ParseIP("127.0.0.1"))
	ips = appendUniqueIP(ips, net.ParseIP("::1"))

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Cordum"},
			CommonName:   "cordum-server",
		},
		DNSNames:    dnsNames,
		IPAddresses: ips,
		NotBefore:   now.Add(-1 * time.Minute),
		NotAfter:    now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create server cert: %w", err)
	}

	if err := writeCert(certPath, der); err != nil {
		return err
	}
	return writeKey(keyPath, key)
}

// GenerateClientCert creates a client certificate signed by the CA.
func GenerateClientCert(caDir, clientDir, cn string, days int, force bool) error {
	if days <= 0 {
		days = 365
	}
	if cn == "" {
		cn = "cordum-client"
	}
	caCert, caKey, err := loadCA(caDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(clientDir, dirPerm); err != nil {
		return fmt.Errorf("mkdir %s: %w", clientDir, err)
	}

	certPath := filepath.Join(clientDir, "tls.crt")
	keyPath := filepath.Join(clientDir, "tls.key")
	if !force {
		if _, err := os.Stat(certPath); err == nil {
			return fmt.Errorf("file exists: %s (use force to overwrite)", certPath)
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate client key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Cordum"},
			CommonName:   cn,
		},
		NotBefore:   now.Add(-1 * time.Minute),
		NotAfter:    now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create client cert: %w", err)
	}

	if err := writeCert(certPath, der); err != nil {
		return err
	}
	return writeKey(keyPath, key)
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// randomSerial generates a 128-bit random serial number using crypto/rand.
func randomSerial() (*big.Int, error) {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt")) // #nosec G304 -- operator-provided path.
	if err != nil {
		return nil, nil, fmt.Errorf("read ca cert: %w", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca.key")) // #nosec G304 -- operator-provided path.
	if err != nil {
		return nil, nil, fmt.Errorf("read ca key: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("decode ca cert pem")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("decode ca key pem")
	}
	rawKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca key: %w", err)
	}
	ecKey, ok := rawKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("ca key is not ecdsa")
	}
	return cert, ecKey, nil
}

func writeCert(path string, der []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, certPerm) // #nosec G304
	if err != nil {
		return fmt.Errorf("write cert %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, keyPerm) // #nosec G304
	if err != nil {
		return fmt.Errorf("write key %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func appendUniqueIP(ips []net.IP, ip net.IP) []net.IP {
	for _, existing := range ips {
		if existing.Equal(ip) {
			return ips
		}
	}
	return append(ips, ip)
}
