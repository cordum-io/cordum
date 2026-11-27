package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// servicesRetentionFixture gives a proof its own miniredis, never
// newTestRedisBackend's shared Windows server, so its FastForward cannot
// expire parallel tests' keys. The marker is the test name plus random hex.
func servicesRetentionFixture(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
}

// servicesRetentionTrace returns every location of marker in client's keyspace.
func servicesRetentionTrace(t *testing.T, client redis.UniversalClient, marker string) []string {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	return hits
}

// servicesRetentionRequire fails unless every key family has a hit, so no
// proof can pass on an empty keyspace.
func servicesRetentionRequire(t *testing.T, hits []string, families ...string) {
	t.Helper()
	for _, family := range families {
		if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("no %s* trace before cleanup; hits=%v", family, hits)
		}
	}
}

// servicesRetentionGone fails if any trace of marker remains.
func servicesRetentionGone(t *testing.T, client redis.UniversalClient, marker string) {
	t.Helper()
	if hits := servicesRetentionTrace(t, client, marker); len(hits) != 0 {
		t.Fatalf("trace remains after cleanup: %v", hits)
	}
}

// TestRetentionProofDIC48 is cited by docs/contract/deletion-proofs.md, with
// safetykernel's TestRetentionProofDIC48Velocity. Each gateway rate-limit
// counter cordum:rl:<scope>:<unix second> expires after rateLimitTTLSec. The
// writer reaches no export.
func TestRetentionProofDIC48(t *testing.T) {
	mr, client, marker := servicesRetentionFixture(t)
	limiter := newRedisRateLimiter(client, 10, 10)
	for _, scope := range []string{"tenant:" + marker, "ip:" + marker} {
		if !limiter.Allow(scope) {
			t.Fatalf("Allow(%q) denied", scope)
		}
	}
	servicesRetentionRequire(t, servicesRetentionTrace(t, client, marker),
		"cordum:rl:tenant:"+marker+":", "cordum:rl:ip:"+marker+":")
	mr.FastForward(rateLimitTTLSec*time.Second + time.Second)
	servicesRetentionGone(t, client, marker)
}

// TestRetentionProofDIC05 is cited by docs/contract/deletion-proofs.md. The
// policy audit log cfg:system:policy_audit keeps the newest 500 entries, so
// 500 newer entries push the marker out while the key stays. The SIEM fan-out
// (auditExporter) is nil here; exported copies are outside this proof.
func TestRetentionProofDIC05(t *testing.T) {
	_, client, marker := servicesRetentionFixture(t)
	ctx := context.Background()
	s := &server{configSvc: configsvc.NewFromClient(client)}
	if err := s.appendPolicyAudit(ctx, policybundles.PolicyAuditEntry{
		Action: "update", ResourceType: "bundle", ResourceID: marker, ActorID: marker, Message: marker,
	}); err != nil {
		t.Fatalf("appendPolicyAudit(marker): %v", err)
	}
	servicesRetentionRequire(t, servicesRetentionTrace(t, client, marker), "cfg:system:policy_audit")
	for i := range 500 {
		if err := s.appendPolicyAudit(ctx, policybundles.PolicyAuditEntry{
			Action: "update", ResourceType: "bundle", ResourceID: fmt.Sprintf("bundle-%d", i),
		}); err != nil {
			t.Fatalf("appendPolicyAudit(%d): %v", i, err)
		}
	}
	servicesRetentionGone(t, client, marker)
	if n, err := client.Exists(ctx, "cfg:system:policy_audit").Result(); err != nil || n != 1 {
		t.Fatalf("cfg:system:policy_audit should remain: n=%d err=%v", n, err)
	}
}
