package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/cordum/cordum/core/mcp/outbound"
)

// runMCPKeygen implements `cordumctl mcp keygen [--out priv.pem]`.
//
// Generates a fresh ECDSA P-256 private key, writes it as a PKCS#8
// PEM block to --out (default stdout for the private key, prefixed
// with a big warning banner so operators don't paste it into a PR).
// Prints the base64 SPKI public key to stdout — that's the value
// operators copy into CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID> on peers.
//
// Exit codes:
//
//	0  success
//	1  I/O or key-gen failure
//	2  invalid flags
func runMCPKeygen(args []string) {
	fs := newFlagSet("mcp keygen")
	outPath := fs.String("out", "", "private key output path (omit to print to stderr with a warning)")
	fs.ParseArgs(args)

	priv, err := outbound.GeneratePrivateKey()
	if err != nil {
		fail(fmt.Sprintf("generate key: %v", err))
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		fail(fmt.Sprintf("marshal private key: %v", err))
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		fail(fmt.Sprintf("marshal public key: %v", err))
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubDER)

	if *outPath != "" {
		// 0o600 so the private key is not readable by any other user
		// on the machine. Op-sec 101 — never default 0o644 on a secret.
		if err := os.WriteFile(*outPath, privPEM, 0o600); err != nil {
			fail(fmt.Sprintf("write %s: %v", *outPath, err))
		}
		fmt.Fprintf(os.Stderr, "wrote private key to %s (mode 0600)\n", *outPath)
	} else {
		fmt.Fprintln(os.Stderr, "WARNING: --out not specified — private key printed to STDERR below.")
		fmt.Fprintln(os.Stderr, "         Redirect to a file AND set CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH.")
		fmt.Fprintln(os.Stderr, "         Never paste this into a PR / Slack / ticket / Discord.")
		fmt.Fprintln(os.Stderr, "---")
		_, _ = os.Stderr.Write(privPEM)
		fmt.Fprintln(os.Stderr, "---")
	}

	// Public key goes to stdout so operators can pipe into
	// `kubectl create secret` or copy-paste into env configuration.
	fmt.Println(pubB64)
}
