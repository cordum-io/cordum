package scheduler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
)

// sessionRetentionFixture gives a proof its own miniredis and a marker made of
// the test name plus random hex.
func sessionRetentionFixture(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
}

// sessionRetentionTrace returns every location of any term in client's keyspace.
func sessionRetentionTrace(t *testing.T, client redis.UniversalClient, terms ...string) []string {
	t.Helper()
	var hits []string
	for _, term := range terms {
		found, err := tracescan.Find(context.Background(), client, term)
		if err != nil {
			t.Fatalf("tracescan.Find(%q): %v", term, err)
		}
		hits = append(hits, found...)
	}
	slices.Sort(hits)
	return slices.Compact(hits)
}

// sessionRetentionCount counts the hits in family; a proof requires at least
// one per family before cleanup, so it cannot pass on an empty keyspace.
func sessionRetentionCount(hits []string, family string) int {
	n := 0
	for _, hit := range hits {
		if strings.HasPrefix(hit, family) {
			n++
		}
	}
	return n
}

// TestRetentionProofDIC45 is cited by docs/contract/deletion-proofs.md. Worker
// sessions session:worker:v2:<tenant>:<worker> and session:worker:legacy:<agent>
// and revocations session:revoked:<tenant>:<jti> expire with the token
// (DefaultSessionTokenLifetime); handshake challenges session:handshake:* expire
// after their ttl, and Consume deletes the challenge key. Key parts are
// base64-encoded (encodeSessionKeyPart), so the search covers the encoded
// tenant, worker and agent as well as the plain marker. The writers reach no
// export.
func TestRetentionProofDIC45(t *testing.T) {
	mr, client, marker := sessionRetentionFixture(t)
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("primary", pub); err != nil {
		t.Fatalf("trust add: %v", err)
	}
	issuer, err := NewSessionTokenIssuer(priv, "primary", trust, client, SessionTokenIssuerOptions{})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	binding := SessionBinding{
		WorkerID: marker + "-worker", AgentID: marker + "-agent", Tenant: marker + "-tenant",
		Audience: marker + "-aud", ProofKeyID: marker + "-proof", SDKVersion: marker + "-sdk",
	}
	if _, _, err := issuer.IssueBound(ctx, binding); err != nil {
		t.Fatalf("IssueBound(first): %v", err)
	}
	token, _, err := issuer.IssueBound(ctx, binding) // persistIssue revokes the first jti
	if err != nil {
		t.Fatalf("IssueBound(second): %v", err)
	}
	if _, _, err := issuer.RenewBound(ctx, token, binding); err != nil { // writeRenewTx revokes the second
		t.Fatalf("RenewBound: %v", err)
	}
	legacy := marker + "-legacy"
	if _, _, err := issuer.Issue(ctx, legacy, binding.Tenant, binding.SDKVersion); err != nil {
		t.Fatalf("Issue(legacy): %v", err)
	}
	handshakes := NewRedisHandshakeChallengeStore(client)
	challenge := &agentv1.WorkerHandshakeChallenge{
		ChallengeId: marker + "-challenge", RequestId: marker + "-request", WorkerId: binding.WorkerID,
		AgentId: binding.AgentID, TenantId: binding.Tenant, ClientNonce: []byte(marker + "-nonce"),
	}
	if ok, err := handshakes.Create(ctx, challenge, 30*time.Second); err != nil || !ok {
		t.Fatalf("handshake Create: ok=%v err=%v", ok, err)
	}

	tenant, worker, agent := encodeSessionKeyPart(binding.Tenant), encodeSessionKeyPart(binding.WorkerID), encodeSessionKeyPart(legacy)
	terms := []string{marker, tenant, worker, agent}
	hits := sessionRetentionTrace(t, client, terms...)
	for family, want := range map[string]int{
		"session:worker:v2:" + tenant + ":" + worker:                                 1,
		"session:worker:legacy:" + agent:                                             1,
		"session:revoked:" + tenant + ":":                                            2,
		"session:handshake:challenge:" + encodeSessionKeyPart(challenge.ChallengeId): 1,
		"session:handshake:request:" + worker + ":":                                  1,
		"session:handshake:nonce:" + worker + ":":                                    1,
	} {
		if got := sessionRetentionCount(hits, family); got != want {
			t.Fatalf("%s*: %d traces before cleanup, want %d; hits=%v", family, got, want, hits)
		}
	}
	if status, err := handshakes.Consume(ctx, challenge); err != nil || status != HandshakeConsumeMatched {
		t.Fatalf("handshake Consume: status=%v err=%v", status, err)
	}
	mr.FastForward(DefaultSessionTokenLifetime + time.Second)
	if hits := sessionRetentionTrace(t, client, terms...); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
}

// TestRetentionProofDIC51 is cited by docs/contract/deletion-proofs.md. A
// delegation lineage audit claim cordum:audit:delegation_lineage:<job>:<jti>
// expires after a fixed 30d. The writer reaches no export.
func TestRetentionProofDIC51(t *testing.T) {
	mr, client, marker := sessionRetentionFixture(t)
	engine := NewEngine(nil, nil, NewMemoryRegistry(), nil, store.NewRedisJobStoreFromClient(client), nil)
	if !engine.claimDelegationLineageAuditEmission(context.Background(), marker+"-job", marker+"-jti") {
		t.Fatal("claimDelegationLineageAuditEmission refused the first claim")
	}
	family := "cordum:audit:delegation_lineage:" + marker + "-job:" + marker + "-jti"
	if hits := sessionRetentionTrace(t, client, marker); sessionRetentionCount(hits, family) != 1 {
		t.Fatalf("no %s trace before cleanup; hits=%v", family, hits)
	}
	mr.FastForward(30*24*time.Hour + time.Second)
	if hits := sessionRetentionTrace(t, client, marker); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
}
