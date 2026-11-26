package shadow

import (
	"context"
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
