package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
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

// TestRetentionProofDIC46 is cited by docs/contract/deletion-proofs.md. An MCP
// approval record mcp:approvals:<id> and its tuple index
// mcp:approvals:idx:<tenant>:<agent>:<tool>:<hash> both expire after the
// request TTL plus 1m; Resolve keeps the record's remaining TTL. No audit hook
// is installed, so the writer reaches no export.
func TestRetentionProofDIC46(t *testing.T) {
	mr, client, marker := servicesRetentionFixture(t)
	ctx := context.Background()
	approvals := NewMCPApprovalStore(client)
	rec, err := approvals.EnqueueMCPApproval(ctx, &MCPApprovalRequest{
		Tenant: marker + "-tenant", AgentID: marker + "-agent", Principal: marker + "-principal",
		ToolName: marker + "-tool", ArgsHash: marker + "-hash", ArgsJSON: json.RawMessage(`{"proof":"` + marker + `"}`),
		Requester: marker + "-requester", Reason: marker + "-reason",
	})
	if err != nil {
		t.Fatalf("EnqueueMCPApproval: %v", err)
	}
	if _, err := approvals.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, marker+"-resolver", marker+"-resolution"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	servicesRetentionRequire(t, servicesRetentionTrace(t, client, marker), "mcp:approvals:"+rec.ID,
		"mcp:approvals:idx:"+marker+"-tenant:"+marker+"-agent:"+marker+"-tool:"+marker+"-hash")
	mr.FastForward(mcpApprovalDefaultTTL + time.Minute + time.Second)
	servicesRetentionGone(t, client, marker)
}

// TestRetentionProofDIC44 is cited by docs/contract/deletion-proofs.md, with
// TestRetentionProofDIC44Cascade. The issue counter
// delegation:issue:<tenant>:<agent>:<minute> expires after 2m and a direct
// revocation delegation:revoked:<jti> at the token's expiry. The writers reach
// no export.
func TestRetentionProofDIC44(t *testing.T) {
	mr, client, marker := servicesRetentionFixture(t)
	ctx := context.Background()
	s := &server{jobStore: store.NewRedisJobStoreFromClient(client)}
	if ok, err := s.allowDelegationIssue(ctx, marker+"-tenant", marker+"-agent"); err != nil || !ok {
		t.Fatalf("allowDelegationIssue: ok=%v err=%v", ok, err)
	}
	if err := delegation.NewRedisRevocationStoreFromClient(client).Revoke(ctx, marker+"-jti", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	servicesRetentionRequire(t, servicesRetentionTrace(t, client, marker),
		"delegation:issue:"+marker+"-tenant:"+marker+"-agent:", "delegation:revoked:"+marker+"-jti")
	mr.FastForward(2*time.Minute + time.Second)
	servicesRetentionGone(t, client, marker)
}

// TestRetentionProofDIC44Cascade is cited by docs/contract/deletion-proofs.md.
// The revoke API (handlers_delegation.go) revokes through CascadeRevoke, which
// writes delegation:revoked:<jti> for the fixed 24h delegationRevocationTTL and
// also rewrites DI-C43's token records. DI-C43 has no cleanup path, so after
// the TTL the marker may remain only in DI-C43's families.
func TestRetentionProofDIC44Cascade(t *testing.T) {
	mr, client, marker := servicesRetentionFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	jti := marker + "-jti"
	if err := delegation.NewRedisListStoreFromClient(client).RecordIssuedToken(ctx, delegation.DelegationView{
		JTI: jti, Tenant: marker + "-tenant", Subject: marker + "-agent", Issuer: marker + "-issuer",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RecordIssuedToken: %v", err)
	}
	if _, err := delegation.NewRedisRevocationStoreFromClient(client).CascadeRevoke(ctx, jti, marker+"-reason", now, true); err != nil {
		t.Fatalf("CascadeRevoke: %v", err)
	}
	servicesRetentionRequire(t, servicesRetentionTrace(t, client, marker), "delegation:revoked:"+jti)
	mr.FastForward(24*time.Hour + time.Second)
	dic43 := []string{"delegation:token:", "delegation:by-agent:", "delegation:active:", "delegation:all:", "delegation:children:"}
	hits := servicesRetentionTrace(t, client, marker)
	for _, hit := range hits {
		if !slices.ContainsFunc(dic43, func(family string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("trace outside DI-C43's families after cleanup: %v", hits)
		}
	}
}

// TestRetentionProofDIC54 is cited by docs/contract/deletion-proofs.md. It
// proves only DI-C54's snapshot trim: both capture sites keep the newest 10
// entries of cfg:system:policy_snapshots, so 10 newer captures through a site
// push that site's marker note out while the key stays. C54's explicit deletes
// are out of scope. The capture handler's audit entry (DI-C05) carries the
// snapshot id, not the note, and auditExporter is nil, so no export is reached.
func TestRetentionProofDIC54(t *testing.T) {
	_, client, marker := servicesRetentionFixture(t)
	ctx := context.Background()
	s := &server{configSvc: configsvc.NewFromClient(client)}
	handler := func(note string) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/snapshots", strings.NewReader(`{"note":"`+note+`"}`))
		rec := httptest.NewRecorder()
		s.handleCapturePolicyBundleSnapshot(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("handleCapturePolicyBundleSnapshot(%q): %d %s", note, rec.Code, rec.Body.String())
		}
	}
	withBundles := func(note string) {
		snap, err := buildPolicyBundleSnapshot(map[string]any{"proof": map[string]any{"enabled": true}}, note)
		if err == nil {
			err = s.storePolicyBundleSnapshots(ctx, snap)
		}
		if err != nil || snap.ID == "" {
			t.Fatalf("storePolicyBundleSnapshots(%q): id=%q err=%v", note, snap.ID, err)
		}
	}
	for _, site := range []struct {
		name    string
		capture func(string)
	}{{"handler", handler}, {"bundles", withBundles}} {
		site.capture(marker + "-" + site.name)
		servicesRetentionRequire(t, servicesRetentionTrace(t, client, marker), "cfg:system:policy_snapshots")
		for i := range 10 {
			site.capture(fmt.Sprintf("newer-%s-%d", site.name, i))
		}
		servicesRetentionGone(t, client, marker)
	}
	if n, err := client.Exists(ctx, "cfg:system:policy_snapshots").Result(); err != nil || n != 1 {
		t.Fatalf("cfg:system:policy_snapshots should remain: n=%d err=%v", n, err)
	}
}
