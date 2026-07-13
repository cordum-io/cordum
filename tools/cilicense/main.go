// Command cilicense emits an ephemeral, test-only license for CI jobs.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

type ciLicenseEnv struct {
	Token     string
	PublicKey string
}

type licenseEnvelope struct {
	KID       string          `json:"kid"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func generateCILicense(now time.Time) (ciLicenseEnv, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ciLicenseEnv{}, fmt.Errorf("generate signing key: %w", err)
	}
	claims := licensing.Claims{
		OrgID:     "cordum-ci",
		LicenseID: fmt.Sprintf("ci-%d", now.UnixNano()),
		Plan:      string(licensing.PlanEnterprise),
		IssuedAt:  now.UTC().Format(time.RFC3339),
		NotBefore: now.UTC().Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.UTC().Add(24 * time.Hour).Format(time.RFC3339),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return ciLicenseEnv{}, fmt.Errorf("marshal claims: %w", err)
	}
	envelope, err := json.Marshal(licenseEnvelope{
		KID:       "ci-ephemeral",
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
	})
	if err != nil {
		return ciLicenseEnv{}, fmt.Errorf("marshal license: %w", err)
	}
	return ciLicenseEnv{
		Token:     base64.StdEncoding.EncodeToString(envelope),
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
	}, nil
}

func main() {
	env, err := generateCILicense(time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\n", env.Token, env.PublicKey)
}
