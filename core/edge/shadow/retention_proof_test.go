package shadow

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/tracescan"
)

// TestRetentionProofDIC03 is the docs/contract/deletion-proofs.md proof for
// shadow finding records. The marker is DI-C03 content (summary, hostname and
// redacted path), which only edge:shadow:finding:<id> holds; the finding ids in
// the edge:shadow:index:* sets are DI-C31. tracescan searches every key,
// including edge:shadow:index:*. Exports: none; the store writes only Redis.
func TestRetentionProofDIC03(t *testing.T) {
	t.Setenv("CORDUM_EDGE_SHADOW_RETENTION_SHORT", "1h")
	ctx := context.Background()
	store, mr := newTestStore(t)
	const terminal, active = "pr2-08-dic03-terminal", "pr2-08-dic03-active"
	resolved := createDIC03Finding(t, store, terminal)
	kept := createDIC03Finding(t, store, active)
	if _, err := store.ResolveFinding(ctx, resolved.TenantID, resolved.FindingID, ResolveRequest{ResolvedBy: "pr2-08", Reason: "retired"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	requireOnlyFindingTrace(t, store, terminal, resolved.FindingID)
	requireOnlyFindingTrace(t, store, active, kept.FindingID)

	// Past the shortened shadow_short TTL and well before its 7d default.
	mr.FastForward(time.Hour + time.Second)
	if hits := dIC03Trace(t, store, terminal); len(hits) != 0 {
		t.Fatalf("trace of %q remains after its terminal TTL: %v", terminal, hits)
	}
	if ttl := mr.TTL(findingKey(kept.FindingID)); ttl != 0 {
		t.Fatalf("active finding TTL = %s, want none", ttl)
	}
	requireOnlyFindingTrace(t, store, active, kept.FindingID)
	if _, err := store.GetFinding(ctx, kept.TenantID, kept.FindingID); err != nil {
		t.Fatalf("active finding: %v; want kept", err)
	}
}

func createDIC03Finding(t *testing.T, store *RedisStore, marker string) *ShadowAgentFinding {
	t.Helper()
	req := minimalCreateReq("tenant-dic03", "owner-dic03", "principal-dic03", "claude-code", FindingRiskHigh, "config_file", marker+" summary")
	req.Hostname = marker + "-host"
	req.RedactedPath = "/etc/" + marker + "/mcp.json"
	req.RetentionClass = ShadowRetentionShort
	finding, err := store.CreateFinding(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateFinding(%s): %v", marker, err)
	}
	return finding
}

// requireOnlyFindingTrace is the positive control: the marker is in the
// finding record and in no other key.
func requireOnlyFindingTrace(t *testing.T, store *RedisStore, marker, findingID string) {
	t.Helper()
	hits := dIC03Trace(t, store, marker)
	if len(hits) != 1 || hits[0] != findingKey(findingID) {
		t.Fatalf("trace of %q = %v, want only %s", marker, hits, findingKey(findingID))
	}
}

func dIC03Trace(t *testing.T, store *RedisStore, marker string) []string {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), store.client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	return hits
}

// TestRetentionProofDIC31.
// The finding id is DI-C31 content. Every index family the writer touched must
// hold it before cleanup; after the record's terminal TTL and the read-time
// cleanupStaleIDTx pass (ListFindings sees the stale id), no trace may remain.
// The finding-index record holds neither id nor marker, so it is checked by key.
func TestRetentionProofDIC31(t *testing.T) {
	t.Setenv("CORDUM_EDGE_SHADOW_RETENTION_SHORT", "1h")
	ctx := context.Background()
	store, mr := newTestStore(t)
	const tenant, marker = "tenant-dic31", "pr2-08-dic31"
	req := minimalCreateReq(tenant, "owner-dic31", "principal-dic31", "claude-code", FindingRiskHigh, "config_file", "dic31 summary")
	req.FindingID = marker
	req.RetentionClass = ShadowRetentionShort
	req.SourceType = SourceTypeCI
	req.CIProvider = "github_actions"
	req.Repo = "org/dic31"
	req.ClusterID = "cluster-dic31"
	req.SignalSet = []string{"mcp_config"}
	finding, err := store.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if _, err := store.ResolveFinding(ctx, tenant, finding.FindingID, ResolveRequest{ResolvedBy: "pr2-08", Reason: "retired"}); err != nil {
		t.Fatalf("ResolveFinding: %v", err)
	}
	id := finding.FindingID
	locations := []string{
		findingKey(id),
		tenantIndexKey(tenant) + ":" + id,
		secondaryIndexKey(tenant, redisIndexSegStatus, string(FindingStatusResolved)) + ":" + id,
		secondaryIndexKey(tenant, redisIndexSegRisk, string(FindingRiskHigh)) + ":" + id,
		secondaryIndexKey(tenant, redisIndexSegAgent, "claude-code") + ":" + id,
		secondaryIndexKey(tenant, redisIndexSegOwner, "owner-dic31") + ":" + id,
		sourceIndexKey(SourceTypeCI) + ":" + id,
		clusterIndexKey("cluster-dic31") + ":" + id,
		repoIndexKey("github_actions", "org/dic31") + ":" + id,
		signalIndexKey("mcp_config") + ":" + id,
	}
	slices.Sort(locations)
	if before := dIC03Trace(t, store, marker); !slices.Equal(before, locations) {
		t.Fatalf("trace of %q before cleanup = %v, want %v", marker, before, locations)
	}
	if !mr.Exists(findingIndexRefKey(tenant, id)) {
		t.Fatalf("finding-index record %s missing before cleanup", findingIndexRefKey(tenant, id))
	}

	mr.FastForward(time.Hour + time.Second)
	if _, err := store.ListFindings(ctx, ListFindingsQuery{TenantID: tenant}); err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	waitForZSetCard(t, ctx, store.client, tenantIndexKey(tenant), 0)
	waitForCleanupIdle(t, store)
	if hits := dIC03Trace(t, store, marker); len(hits) != 0 {
		t.Fatalf("trace of %q remains after terminal TTL and stale-index cleanup: %v", marker, hits)
	}
	requireNoKey(t, mr, findingIndexRefKey(tenant, id))
}
