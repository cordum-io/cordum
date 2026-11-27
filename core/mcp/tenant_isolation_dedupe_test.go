package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// tenantEchoUpstream answers each call with the caller's tenant, so a result
// shows which tenant's upstream call produced it.
type tenantEchoUpstream struct{ calls []string }

func (u *tenantEchoUpstream) Invoke(ctx context.Context, _ ToolCallParams) (*ToolCallResult, error) {
	meta, _ := CallMetadataFromContext(ctx)
	u.calls = append(u.calls, meta.Tenant)
	return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "result-for-" + meta.Tenant}}}, nil
}

func TestTenantIsolationMCPDedupeKey(t *testing.T) {
	client, mr := newMiniRedisDedupeBackend(t)
	upstream := &tenantEchoUpstream{}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, &fakeEventEmitter{}, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = NewRedisDedupeStore(client)
	params := ToolCallParams{Name: "fs.read_file", Arguments: json.RawMessage(`{"path":"/tmp/shared"}`)}
	// Only the tenant differs: same principal, agent, session, execution and args.
	ctxFor := func(tenant string) context.Context {
		return WithCallMetadata(context.Background(), CallMetadata{
			Tenant: tenant, Principal: "p1", AgentID: "agent_alpha", SessionID: "sess_42", ExecutionID: "exec_99",
		})
	}

	if got, err := InvokeToolWithPolicy(ctxFor("tnt_a"), deps, params, "local-fs"); err != nil || got == nil || len(got.Content) != 1 || got.Content[0].Text != "result-for-tnt_a" {
		t.Fatalf("tenant-a call = %#v, %v", got, err)
	}
	keyA := MCPDedupeKeyPrefix + semanticDedupeKeyForCall(ctxFor("tnt_a"), params, "local-fs")
	rawA, err := mr.Get(keyA)
	if err != nil || rawA == "" {
		t.Fatalf("tenant-a dedupe row %s = %q, %v; want a completed row", keyA, rawA, err)
	}

	got, err := InvokeToolWithPolicy(ctxFor("tnt_b"), deps, params, "local-fs")
	if err != nil || got == nil || len(got.Content) != 1 || got.Content[0].Text != "result-for-tnt_b" {
		t.Fatalf("tenant-b call with tenant-a's dedupe input = %#v, %v; want its own upstream result", got, err)
	}
	if !slices.Equal(upstream.calls, []string{"tnt_a", "tnt_b"}) {
		t.Fatalf("upstream calls = %v; want one per tenant", upstream.calls)
	}
	keyB := MCPDedupeKeyPrefix + semanticDedupeKeyForCall(ctxFor("tnt_b"), params, "local-fs")
	if keyB == keyA || !mr.Exists(keyB) {
		t.Fatalf("tenant-b dedupe row %s not separate from tenant-a's %s", keyB, keyA)
	}
	if after, err := mr.Get(keyA); err != nil || after != rawA {
		t.Fatalf("tenant-a dedupe row changed: %q, %v", after, err)
	}

	// Dedupe still holds inside tenant A.
	if _, err := InvokeToolWithPolicy(ctxFor("tnt_a"), deps, params, "local-fs"); err != nil || len(upstream.calls) != 2 {
		t.Fatalf("tenant-a retry: err %v, upstream calls %v; want deduplicated", err, upstream.calls)
	}
}
