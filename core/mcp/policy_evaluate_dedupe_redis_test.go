package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestInvokeToolWithPolicy_RedisDedupeDoesNotPersistRawToolResult(t *testing.T) {
	ctx := newAuthedToolCallCtx()
	client, _ := newMiniRedisDedupeBackend(t)
	canary := "raw-dedupe-canary-20260519"
	secretShape := strings.Join([]string{"token", "test", canary}, "-")
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{
			Content: []ContentItem{
				{Type: "text", Text: "raw text " + canary + " " + secretShape},
				{Type: "blob", Data: "raw-data-" + canary, MIMEType: "application/octet-stream"},
			},
			IsError: true,
			StructuredContent: map[string]any{
				"payload": canary,
				"token":   secretShape,
			},
		},
	}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, &fakeEventEmitter{}, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = NewRedisDedupeStore(client)
	params := ToolCallParams{Name: "fs.read_file", Arguments: json.RawMessage(`{"path":"/tmp/raw-dedupe"}`)}

	first, err := InvokeToolWithPolicy(ctx, deps, params, "local-fs")
	if err != nil {
		t.Fatalf("first InvokeToolWithPolicy: %v", err)
	}
	if first == nil || !toolCallResultContains(t, first, canary) {
		t.Fatalf("first result = %#v, want raw upstream canary before Redis duplicate suppression", first)
	}
	payload := singleRedisDedupePayload(t, ctx, client)
	for i, forbidden := range []string{canary, secretShape, "raw-data-" + canary, "structuredContent", "payload", "token"} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("Redis dedupe payload contains forbidden raw result fragment at index %d", i)
		}
	}
	for i, required := range []string{"\"state\":\"completed\"", "\"is_error\":true", "\"content_count\":2", "\"result_sha256\""} {
		if !strings.Contains(payload, required) {
			t.Errorf("Redis dedupe payload missing safe metadata fragment %d (%s)", i, required)
		}
	}

	second, err := InvokeToolWithPolicy(ctx, deps, params, "local-fs")
	if err != nil {
		t.Fatalf("duplicate InvokeToolWithPolicy: %v", err)
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream calls = %d, want 1 (Redis duplicate must not re-invoke upstream)", upstream.calls)
	}
	if second == nil || !second.IsError {
		t.Fatalf("duplicate result = %#v, want non-nil result preserving IsError=true", second)
	}
	if toolCallResultContains(t, second, canary) || toolCallResultContains(t, second, secretShape) {
		t.Fatalf("duplicate result leaked raw canary/secret")
	}
}

func TestInvokeToolWithPolicy_RedisDedupeMalformedCompletedRecordReruns(t *testing.T) {
	ctx := newAuthedToolCallCtx()
	client, _ := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	params := ToolCallParams{Name: "fs.read_file", Arguments: json.RawMessage(`{"path":"/tmp/malformed-dedupe"}`)}
	dedupeID := semanticDedupeKeyForCall(ctx, params, "local-fs")
	store.Store(dedupeID, &redisDedupeRecord{State: redisDedupeStateCompleted})

	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "fresh result"}}},
	}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, &fakeEventEmitter{}, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = store

	got, err := InvokeToolWithPolicy(ctx, deps, params, "local-fs")
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy with malformed Redis dedupe record: %v", err)
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream calls = %d, want 1 (malformed completed metadata must rerun upstream)", upstream.calls)
	}
	if got == nil || len(got.Content) != 1 || got.Content[0].Text != "fresh result" {
		t.Fatalf("result = %#v, want fresh upstream result", got)
	}
	payload := singleRedisDedupePayload(t, ctx, client)
	if !strings.Contains(payload, "\"result_sha256\"") || strings.Contains(payload, "fresh result") {
		t.Fatalf("post-rerun Redis payload does not contain safe metadata only")
	}
}

func singleRedisDedupePayload(t *testing.T, ctx context.Context, client interface {
	Keys(context.Context, string) *redis.StringSliceCmd
	Get(context.Context, string) *redis.StringCmd
}) string {
	t.Helper()
	keys, err := client.Keys(ctx, MCPDedupeKeyPrefix+"*").Result()
	if err != nil {
		t.Fatalf("list Redis dedupe keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("Redis dedupe key count = %d (%v), want 1", len(keys), keys)
	}
	payload, err := client.Get(ctx, keys[0]).Result()
	if err != nil {
		t.Fatalf("read Redis dedupe key %s: %v", keys[0], err)
	}
	return payload
}

func toolCallResultContains(t *testing.T, result *ToolCallResult, needle string) bool {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal ToolCallResult for contains check: %v", err)
	}
	return strings.Contains(string(raw), needle)
}
