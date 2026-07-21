package workercredentials

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestCreateEnrollsAndResolvesProofKey(t *testing.T) {
	svc := newTestService(t)
	keyPEM := testPublicKeyPEM(t, elliptic.P256())
	issued, err := svc.Create(context.Background(), IssueInput{
		TenantID: "tenant-a", WorkerID: "worker-a", CreatedBy: "tester",
		ProofKeyID: " key-a ", ProofAlgorithm: " ECDSA_P256_SHA256 ",
		ProofPublicKeyPEM: "\n" + keyPEM + "\n",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issued.Credential.ProofKeyID != "key-a" {
		t.Fatalf("proof key id = %q, want key-a", issued.Credential.ProofKeyID)
	}
	if issued.Credential.ProofAlgorithm != ProofAlgorithmECDSAP256SHA256 {
		t.Fatalf("proof algorithm = %q", issued.Credential.ProofAlgorithm)
	}
	if issued.Credential.ProofPublicKeyPEM != keyPEM {
		t.Fatalf("proof public key was not canonicalized:\n%s", issued.Credential.ProofPublicKeyPEM)
	}

	record, err := svc.GetByWorkerID(context.Background(), "worker-a")
	if err != nil || record == nil || record.TenantID != "tenant-a" {
		t.Fatalf("GetByWorkerID: record=%+v err=%v", record, err)
	}
	if leaked, err := svc.Get(context.Background(), "tenant-b", "worker-a"); err != nil || leaked != nil {
		t.Fatalf("tenant-scoped Get leaked record=%+v err=%v", leaked, err)
	}
	pub, revoked, err := svc.ResolveProofKey(context.Background(), "worker-a", "key-a")
	if err != nil || revoked || pub == nil || pub.Curve != elliptic.P256() {
		t.Fatalf("ResolveProofKey: pub=%+v revoked=%v err=%v", pub, revoked, err)
	}
	if pub, revoked, err := svc.ResolveProofKey(context.Background(), "worker-a", "wrong-key"); err != nil || revoked || pub != nil {
		t.Fatalf("wrong key resolved: pub=%+v revoked=%v err=%v", pub, revoked, err)
	}
}

func TestCreateRejectsInvalidProofKeyEnrollment(t *testing.T) {
	p256PEM := testPublicKeyPEM(t, elliptic.P256())
	rsaPEM := testRSAPublicKeyPEM(t)
	cases := []struct {
		name, keyID, algorithm, publicPEM, want string
	}{
		{"missing key id", "", ProofAlgorithmECDSAP256SHA256, p256PEM, "proof key fields must be provided together"},
		{"missing algorithm", "key-a", "", p256PEM, "proof key fields must be provided together"},
		{"missing public key", "key-a", ProofAlgorithmECDSAP256SHA256, "", "proof key fields must be provided together"},
		{"wrong algorithm", "key-a", "ECDSA_P384_SHA384", p256PEM, "proof algorithm must be ECDSA_P256_SHA256"},
		{"key id whitespace", "key a", ProofAlgorithmECDSAP256SHA256, p256PEM, "proof key id"},
		{"key id too long", strings.Repeat("a", 129), ProofAlgorithmECDSAP256SHA256, p256PEM, "proof key id"},
		{"malformed pem", "key-a", ProofAlgorithmECDSAP256SHA256, "not pem", "SPKI PEM"},
		{"oversized pem", "key-a", ProofAlgorithmECDSAP256SHA256, strings.Repeat("x", 4097), "too large"},
		{"oversized padded pem", "key-a", ProofAlgorithmECDSAP256SHA256, strings.Repeat(" ", 4097) + p256PEM, "too large"},
		{"wrong curve", "key-a", ProofAlgorithmECDSAP256SHA256, testPublicKeyPEM(t, elliptic.P384()), "P-256"},
		{"wrong key type", "key-a", ProofAlgorithmECDSAP256SHA256, rsaPEM, "ECDSA"},
		{"trailing data", "key-a", ProofAlgorithmECDSAP256SHA256, p256PEM + "junk", "trailing data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newTestService(t).Create(context.Background(), IssueInput{
				TenantID: "default", WorkerID: "worker-a", CreatedBy: "tester",
				ProofKeyID: tc.keyID, ProofAlgorithm: tc.algorithm, ProofPublicKeyPEM: tc.publicPEM,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestProofKeyRotationAndRevocation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	keyA := testPublicKeyPEM(t, elliptic.P256())
	keyB := testPublicKeyPEM(t, elliptic.P256())
	keyC := testPublicKeyPEM(t, elliptic.P256())

	createProofCredential(t, svc, "key-a", keyA)
	withoutProof, err := svc.Create(ctx, IssueInput{TenantID: "default", WorkerID: "worker-a", CreatedBy: "tester"})
	if err != nil || withoutProof.Credential.ProofKeyID != "key-a" {
		t.Fatalf("rotation without proof did not preserve key: credential=%+v err=%v", withoutProof.Credential, err)
	}
	createProofCredential(t, svc, "key-b", keyB)
	if pub, _, err := svc.ResolveProofKey(ctx, "worker-a", "key-a"); err != nil || pub != nil {
		t.Fatalf("superseded key resolved: pub=%+v err=%v", pub, err)
	}
	if pub, revoked, err := svc.ResolveProofKey(ctx, "worker-a", "key-b"); err != nil || revoked || pub == nil {
		t.Fatalf("replacement key missing: pub=%+v revoked=%v err=%v", pub, revoked, err)
	}

	if err := svc.Revoke(ctx, "default", "worker-a"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if pub, revoked, err := svc.ResolveProofKey(ctx, "worker-a", "key-b"); err != nil || !revoked || pub != nil {
		t.Fatalf("revoked proof resolved: pub=%+v revoked=%v err=%v", pub, revoked, err)
	}
	if _, err := svc.Create(ctx, IssueInput{TenantID: "default", WorkerID: "worker-a", CreatedBy: "tester"}); err == nil || !strings.Contains(err.Error(), "fresh proof key required") {
		t.Fatalf("revoked rotation without fresh key error = %v", err)
	}
	record, err := svc.GetByWorkerID(ctx, "worker-a")
	if err != nil || record == nil || !record.Revoked() || record.ProofKeyID != "key-b" {
		t.Fatalf("failed rotation mutated revoked record: record=%+v err=%v", record, err)
	}
	createProofCredential(t, svc, "key-c", keyC)
	if pub, revoked, err := svc.ResolveProofKey(ctx, "worker-a", "key-c"); err != nil || revoked || pub == nil {
		t.Fatalf("fresh key did not reactivate record: pub=%+v revoked=%v err=%v", pub, revoked, err)
	}
}

func TestGetByWorkerIDValidatesInput(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.GetByWorkerID(context.Background(), "  "); err == nil {
		t.Fatal("expected empty worker ID rejection")
	}
	if record, err := svc.GetByWorkerID(context.Background(), "missing"); err != nil || record != nil {
		t.Fatalf("missing record = %+v, err=%v", record, err)
	}
}

func TestCredentialForWriteUsesLatestProofKeyAcrossCASRetries(t *testing.T) {
	legacy, err := credentialForWrite(Credential{WorkerID: "worker-a"}, Credential{
		WorkerID: "worker-a", AgentID: "legacy-agent",
	}, true)
	if err != nil || legacy.AgentID != "" {
		t.Fatalf("legacy rotation preserved stale authority: %+v, err=%v", legacy, err)
	}
	base := Credential{WorkerID: "worker-a", AgentID: "agent-stale"}
	first, err := credentialForWrite(base, Credential{
		WorkerID: "worker-a", ProofKeyID: "key-a", ProofAlgorithm: ProofAlgorithmECDSAP256SHA256,
		ProofPublicKeyPEM: "pem-a", AgentID: "agent-a",
	}, true)
	if err != nil || first.ProofKeyID != "key-a" || first.AgentID != "agent-a" {
		t.Fatalf("first candidate = %+v, err=%v", first, err)
	}
	latest, err := credentialForWrite(base, Credential{
		WorkerID: "worker-a", ProofKeyID: "key-b", ProofAlgorithm: ProofAlgorithmECDSAP256SHA256,
		ProofPublicKeyPEM: "pem-b", AgentID: "agent-b",
	}, true)
	if err != nil || latest.ProofKeyID != "key-b" || latest.ProofPublicKeyPEM != "pem-b" || latest.AgentID != "agent-b" {
		t.Fatalf("retry candidate did not use latest proof authority: %+v, err=%v", latest, err)
	}
}

func TestCredentialResolveProofKeyUsesFetchedSnapshot(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	issuedA := createProofCredential(t, svc, "shared-key", testPublicKeyPEM(t, elliptic.P256()))
	publicA, revoked, err := issuedA.Credential.ResolveProofKey("shared-key")
	if err != nil || revoked || publicA == nil {
		t.Fatalf("resolve fetched key A: pub=%+v revoked=%v err=%v", publicA, revoked, err)
	}
	createProofCredential(t, svc, "shared-key", testPublicKeyPEM(t, elliptic.P256()))
	publicFromSnapshot, _, err := issuedA.Credential.ResolveProofKey("shared-key")
	if err != nil || publicFromSnapshot == nil || !publicFromSnapshot.Equal(publicA) {
		t.Fatalf("fetched snapshot changed after rotation: pub=%+v err=%v", publicFromSnapshot, err)
	}
	publicCurrent, _, err := svc.ResolveProofKey(ctx, "worker-a", "shared-key")
	if err != nil || publicCurrent == nil || publicCurrent.Equal(publicA) {
		t.Fatalf("service did not resolve rotated key: pub=%+v err=%v", publicCurrent, err)
	}
}

func TestCredentialResolveProofKeyRejectsInvalidSnapshot(t *testing.T) {
	issued := createProofCredential(t, newTestService(t), "key-a", testPublicKeyPEM(t, elliptic.P256()))
	if publicKey, revoked, err := issued.Credential.ResolveProofKey("wrong"); err != nil || revoked || publicKey != nil {
		t.Fatalf("mismatched key: pub=%+v revoked=%v err=%v", publicKey, revoked, err)
	}
	revokedRecord := issued.Credential
	revokedRecord.RevokedAt = "2026-07-16T12:00:00Z"
	if publicKey, revoked, err := revokedRecord.ResolveProofKey("key-a"); err != nil || !revoked || publicKey != nil {
		t.Fatalf("revoked key: pub=%+v revoked=%v err=%v", publicKey, revoked, err)
	}
	corrupt := issued.Credential
	corrupt.ProofPublicKeyPEM = "not pem"
	if publicKey, _, err := corrupt.ResolveProofKey("key-a"); err == nil || publicKey != nil {
		t.Fatalf("corrupt key: pub=%+v err=%v", publicKey, err)
	}
}

func createProofCredential(t *testing.T, svc *Service, keyID, publicPEM string) IssuedCredential {
	t.Helper()
	issued, err := svc.Create(context.Background(), IssueInput{
		TenantID: "default", WorkerID: "worker-a", CreatedBy: "tester",
		ProofKeyID: keyID, ProofAlgorithm: ProofAlgorithmECDSAP256SHA256, ProofPublicKeyPEM: publicPEM,
	})
	if err != nil {
		t.Fatalf("Create %s: %v", keyID, err)
	}
	return issued
}

func testPublicKeyPEM(t *testing.T, curve elliptic.Curve) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func testRSAPublicKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}
