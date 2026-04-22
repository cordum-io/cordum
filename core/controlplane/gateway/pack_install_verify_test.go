package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/packs/signing"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

// writeGatewayTestPack lays out a minimal signable pack in a temp dir
// and optionally signs it. Returns (packRoot, publisherKID, publisher
// public key). When sign is false, pack.yaml.sig is NOT created.
func writeGatewayTestPack(t *testing.T, sign bool) (string, string, ed25519.PublicKey) {
	t.Helper()
	root := t.TempDir()
	mkfile := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mkfile("pack.yaml", `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: gateway-sig-test
  version: 0.1.0
resources:
  schemas:
    - id: t/In
      path: schemas/In.json
`)
	mkfile("schemas/In.json", `{"type":"object"}`)

	if !sign {
		return root, "", nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	manifest, err := signing.BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	signed, err := signing.SignManifest(manifest, priv, "gwkey-test")
	if err != nil {
		t.Fatalf("SignManifest: %v", err)
	}
	body, err := signing.EncodeEnvelopeYAML(signed)
	if err != nil {
		t.Fatalf("EncodeEnvelopeYAML: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.yaml.sig"), body, 0o644); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	return root, "gwkey-test", pub
}

func TestGatewayVerify_UnsignedInNonStrictAccepted(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "")
	root, _, _ := writeGatewayTestPack(t, false)
	res, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr != nil {
		t.Fatalf("unexpected verify error: %v", verr)
	}
	if res.Signed {
		t.Errorf("Signed should be false on unsigned pack")
	}
}

func TestGatewayVerify_UnsignedInStrictRejected(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	root, _, _ := writeGatewayTestPack(t, false)
	_, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr == nil {
		t.Fatal("want unsigned rejection")
	}
	if verr.Code != PackVerificationCodeUnsigned {
		t.Errorf("code=%s, want %s", verr.Code, PackVerificationCodeUnsigned)
	}
}

func TestGatewayVerify_SignedAccepted(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	root, kid, pub := writeGatewayTestPack(t, true)

	// Register the publisher key via env so nil-client path works.
	envVar := gatewayPackTrustedKeyEnvPrefix + strings.ToUpper(strings.ReplaceAll(kid, "-", "_"))
	t.Setenv(envVar, base64.StdEncoding.EncodeToString(pub))

	res, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	if !res.Signed {
		t.Errorf("Signed should be true")
	}
	if res.KID != kid {
		t.Errorf("KID=%q, want %q", res.KID, kid)
	}
	// Fallback: when no publisher metadata is registered, publisher_id
	// falls back to the kid itself so the installed-pack record DoD
	// ("installed pack metadata includes: signed, publisher_id,
	// verified_at") is always satisfied.
	if res.PublisherID != kid {
		t.Errorf("PublisherID fallback=%q, want kid=%q", res.PublisherID, kid)
	}
	if res.VerifiedAt.IsZero() {
		t.Errorf("VerifiedAt must be set on a successful verify")
	}
	// Sanity-check the on-the-wire serialization carries publisher_id.
	onWire := verificationFromServerResult(res)
	if onWire == nil || onWire.PublisherID != kid {
		t.Errorf("verificationFromServerResult did not surface PublisherID: %+v", onWire)
	}
}

func TestGatewayVerify_PublisherMetadataFromEnv(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	root, kid, pub := writeGatewayTestPack(t, true)

	keyEnv := gatewayPackTrustedKeyEnvPrefix + strings.ToUpper(strings.ReplaceAll(kid, "-", "_"))
	t.Setenv(keyEnv, base64.StdEncoding.EncodeToString(pub))
	pubEnv := gatewayPackPublisherEnvPrefix + strings.ToUpper(strings.ReplaceAll(kid, "-", "_"))
	t.Setenv(pubEnv, "acme-publisher")

	res, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	if res.PublisherID != "acme-publisher" {
		t.Errorf("PublisherID=%q, want acme-publisher", res.PublisherID)
	}
}

func TestGatewayVerify_PublisherMetadataFromRedis(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	defer func() { _ = client.Close() }()

	root, kid, pub := writeGatewayTestPack(t, true)
	if err := client.Set(context.Background(), gatewayTrustedKeysRedisPrefix+kid, base64.StdEncoding.EncodeToString(pub), 0).Err(); err != nil {
		t.Fatal(err)
	}
	metaJSON := `{"publisher_id":"example-corp","display_name":"Example Corp","added_at":"2026-04-20T00:00:00Z"}`
	if err := client.Set(context.Background(), gatewayPackPublisherRedisPrefix+kid, metaJSON, 0).Err(); err != nil {
		t.Fatal(err)
	}

	res, verr := verifyPackInstallBundle(context.Background(), client, root, "gateway-sig-test")
	if verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	if res.PublisherID != "example-corp" {
		t.Errorf("PublisherID=%q, want example-corp", res.PublisherID)
	}
	onWire := verificationFromServerResult(res)
	if onWire.PublisherID != "example-corp" {
		t.Errorf("serialized PublisherID=%q, want example-corp", onWire.PublisherID)
	}
}

func TestGatewayVerify_TamperedRejected(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	root, kid, pub := writeGatewayTestPack(t, true)
	envVar := gatewayPackTrustedKeyEnvPrefix + strings.ToUpper(strings.ReplaceAll(kid, "-", "_"))
	t.Setenv(envVar, base64.StdEncoding.EncodeToString(pub))

	// Tamper with a signed file.
	if err := os.WriteFile(filepath.Join(root, "schemas/In.json"), []byte(`{"type":"array"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr == nil {
		t.Fatal("want tamper rejection")
	}
	if verr.Code != PackVerificationCodeTampered {
		t.Errorf("code=%s, want %s", verr.Code, PackVerificationCodeTampered)
	}
	if !errors.Is(verr, signing.ErrHashMismatch) {
		t.Errorf("Unwrap should return ErrHashMismatch, got %v", verr.Err)
	}
}

func TestGatewayVerify_UnknownKidRejected(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	root, _, _ := writeGatewayTestPack(t, true)
	// NO env key registered → keyring empty → unknown kid.
	_, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr == nil {
		t.Fatal("want unknown kid error")
	}
	if verr.Code != PackVerificationCodeUnknownKID {
		t.Errorf("code=%s, want %s", verr.Code, PackVerificationCodeUnknownKID)
	}
}

func TestGatewayVerify_StrictFlagFlippedViaRedis(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "")
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	defer func() { _ = client.Close() }()

	root, _, _ := writeGatewayTestPack(t, false)

	// Initial: redis cfg unset, no env → non-strict, unsigned accepted.
	if _, verr := verifyPackInstallBundle(context.Background(), client, root, "gateway-sig-test"); verr != nil {
		t.Fatalf("expected non-strict to accept, got %v", verr)
	}

	// Flip to strict via Redis, clear cache so new value propagates.
	if err := client.Set(context.Background(), gatewayStrictRedisKey, "true", 0).Err(); err != nil {
		t.Fatal(err)
	}
	resetGatewayPackStrictCache()

	_, verr := verifyPackInstallBundle(context.Background(), client, root, "gateway-sig-test")
	if verr == nil || verr.Code != PackVerificationCodeUnsigned {
		t.Fatalf("post-flip should reject as unsigned, got %v", verr)
	}
}

func TestGatewayVerify_TrustedKeyFromRedis(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	defer func() { _ = client.Close() }()

	root, kid, pub := writeGatewayTestPack(t, true)
	if err := client.Set(context.Background(), gatewayTrustedKeysRedisPrefix+kid, base64.StdEncoding.EncodeToString(pub), 0).Err(); err != nil {
		t.Fatal(err)
	}
	res, verr := verifyPackInstallBundle(context.Background(), client, root, "gateway-sig-test")
	if verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	if !res.Signed || res.KID != kid {
		t.Errorf("result=%+v", res)
	}
}

func TestGatewayVerify_MalformedEnvelope(t *testing.T) {
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "true")
	root, _, _ := writeGatewayTestPack(t, false)
	if err := os.WriteFile(filepath.Join(root, "pack.yaml.sig"), []byte("not-a-signature"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, verr := verifyPackInstallBundle(context.Background(), nil, root, "gateway-sig-test")
	if verr == nil {
		t.Fatal("want malformed error")
	}
	if verr.Code != PackVerificationCodeMalformed {
		t.Errorf("code=%s, want %s", verr.Code, PackVerificationCodeMalformed)
	}
}

func TestVerificationFromServerResult(t *testing.T) {
	nilOut := verificationFromServerResult(nil)
	if nilOut != nil {
		t.Error("nil input should produce nil output")
	}
	unsigned := verificationFromServerResult(&gatewayVerifiedPackInstall{Signed: false})
	if unsigned == nil || unsigned.Signed {
		t.Error("unsigned round-trip broken")
	}
	if len(unsigned.Warnings) == 0 {
		t.Error("unsigned result should carry a warning")
	}
	if err := yaml.Unmarshal([]byte(""), &struct{}{}); err != nil {
		t.Logf("yaml guard: %v", err)
	}
}
