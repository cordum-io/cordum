package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateCertsSANFlagIsRepeatable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	gatewayFQDN := "cordum-api-gateway.cordum.svc.cluster.local"
	runGenerateCertsCmd([]string{"--dir", dir, "--san", gatewayFQDN, "--san", "10.0.0.7"})

	data, err := os.ReadFile(filepath.Join(dir, "server", "tls.crt"))
	if err != nil {
		t.Fatalf("read server cert: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("server cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse server cert: %v", err)
	}
	for _, host := range []string{gatewayFQDN, "10.0.0.7", "localhost", "cordum-api-gateway"} {
		if err := cert.VerifyHostname(host); err != nil {
			t.Errorf("server cert does not cover %q: %v", host, err)
		}
	}
}
