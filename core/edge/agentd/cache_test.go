package agentd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestSafeAllowCacheMissHitAndKeyIsolation(t *testing.T) {
	t.Parallel()

	clock := &cacheTestClock{now: time.Date(2026, 5, 2, 13, 0, 0, 0, time.UTC)}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 8}, clock)
	req := safeAllowCacheTestRequest()
	resp := safeAllowCacheTestResponse()

	if got, ok := cache.Get(req); ok {
		t.Fatalf("initial cache Get = %#v, want miss", got)
	}
	if !cache.Put(req, resp) {
		t.Fatal("Put returned false for safe cache-eligible ALLOW")
	}
	got, ok := cache.Get(req)
	if !ok {
		t.Fatal("Get after Put missed; want hit")
	}
	if got.Decision != string(edgecore.DecisionAllow) || got.RuleID != resp.RuleID || got.PolicySnapshot != resp.PolicySnapshot {
		t.Fatalf("cached response = %#v, want replay of %#v", got, resp)
	}

	mutations := []struct {
		name string
		req  SafeAllowCacheRequest
	}{
		{name: "tenant", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.TenantID = "tenant-b" })},
		{name: "policy mode", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.PolicyMode = edgecore.PolicyModeObserve })},
		{name: "policy snapshot", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.PolicySnapshot = "snap-cache-b" })},
		{name: "kind", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.Kind = "hook.post_tool_use" })},
		{name: "risk tags", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.RiskTags = []string{"exec", "test", "runtime"} })},
		{name: "action hash", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.ActionHash = "sha256:action-b" })},
		{name: "input hash", req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) { v.InputHash = "sha256:input-b" })},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := cache.Get(tc.req); ok {
				t.Fatalf("mutated key %s hit cache with %#v; want miss", tc.name, got)
			}
		})
	}
}

func TestSafeAllowCacheKey_DistinctPerJobSnapshot(t *testing.T) {
	t.Parallel()

	clock := &cacheTestClock{now: time.Date(2026, 5, 5, 16, 0, 0, 0, time.UTC)}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 8}, clock)
	req := safeAllowCacheTestRequest()
	req.WorkflowOverrideSnapshot = "snap-workflow-a"
	req.JobOverrideSnapshot = "snap-job-a"

	if !cache.Put(req, safeAllowCacheTestResponse()) {
		t.Fatal("Put returned false for safe cache-eligible ALLOW")
	}
	if _, ok := cache.Get(req); !ok {
		t.Fatal("Get with original tier snapshots missed; want hit")
	}

	cases := []struct {
		name string
		req  SafeAllowCacheRequest
	}{
		{
			name: "workflow snapshot",
			req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) {
				v.WorkflowOverrideSnapshot = "snap-workflow-b"
			}),
		},
		{
			name: "job snapshot",
			req: mutateSafeAllowCacheRequest(req, func(v *SafeAllowCacheRequest) {
				v.JobOverrideSnapshot = "snap-job-b"
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := cache.Get(tc.req); ok {
				t.Fatalf("mutated %s hit cache with %#v; want miss", tc.name, got)
			}
		})
	}
}

func TestSafeAllowCacheTTLEvictionAndDisable(t *testing.T) {
	t.Parallel()

	clock := &cacheTestClock{now: time.Date(2026, 5, 2, 13, 30, 0, 0, time.UTC)}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: 10 * time.Second, MaxEntries: 2}, clock)
	first := safeAllowCacheTestRequest()
	second := mutateSafeAllowCacheRequest(first, func(v *SafeAllowCacheRequest) { v.ActionHash = "sha256:action-2" })
	third := mutateSafeAllowCacheRequest(first, func(v *SafeAllowCacheRequest) { v.ActionHash = "sha256:action-3" })

	if !cache.Put(first, safeAllowCacheTestResponse()) || !cache.Put(second, safeAllowCacheTestResponse()) {
		t.Fatal("Put returned false for initial safe entries")
	}
	if _, ok := cache.Get(first); !ok {
		t.Fatal("first entry missed before eviction")
	}
	if !cache.Put(third, safeAllowCacheTestResponse()) {
		t.Fatal("Put returned false for third safe entry")
	}
	if got, ok := cache.Get(first); ok {
		t.Fatalf("oldest entry hit after max-entry eviction: %#v", got)
	}
	if _, ok := cache.Get(second); !ok {
		t.Fatal("second entry missed after evicting oldest")
	}
	if _, ok := cache.Get(third); !ok {
		t.Fatal("third entry missed after insert")
	}

	clock.Advance(11 * time.Second)
	if got, ok := cache.Get(second); ok {
		t.Fatalf("second entry hit after TTL expiry: %#v", got)
	}
	if got, ok := cache.Get(third); ok {
		t.Fatalf("third entry hit after TTL expiry: %#v", got)
	}

	disabled := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: false, TTL: time.Minute, MaxEntries: 2}, clock)
	if disabled.Put(first, safeAllowCacheTestResponse()) {
		t.Fatal("disabled cache stored an entry")
	}
	if got, ok := disabled.Get(first); ok {
		t.Fatalf("disabled cache hit with %#v; want miss", got)
	}
}

func TestSafeAllowCache_LRUEvictionAfterOverwrite(t *testing.T) {
	t.Parallel()

	clock := &cacheTestClock{now: time.Date(2026, 5, 2, 13, 45, 0, 0, time.UTC)}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 2}, clock)
	first := safeAllowCacheTestRequest()
	second := mutateSafeAllowCacheRequest(first, func(v *SafeAllowCacheRequest) { v.ActionHash = "sha256:action-2" })
	third := mutateSafeAllowCacheRequest(first, func(v *SafeAllowCacheRequest) { v.ActionHash = "sha256:action-3" })

	if !cache.Put(first, safeAllowCacheTestResponse()) || !cache.Put(second, safeAllowCacheTestResponse()) {
		t.Fatal("Put returned false for initial safe entries")
	}
	if !cache.Put(first, mutateSafeAllowCacheResponse(safeAllowCacheTestResponse(), func(v *EvaluateResponse) {
		v.RuleID = "safe-cache-rule-overwritten"
	})) {
		t.Fatal("Put returned false for overwritten safe entry")
	}
	if !cache.Put(third, safeAllowCacheTestResponse()) {
		t.Fatal("Put returned false for third safe entry")
	}

	if got, ok := cache.Get(second); ok {
		t.Fatalf("second entry hit after overwrite made first most-recently-used: %#v", got)
	}
	got, ok := cache.Get(first)
	if !ok {
		t.Fatal("overwritten first entry was evicted; want it retained as most-recently-used")
	}
	if got.RuleID != "safe-cache-rule-overwritten" {
		t.Fatalf("first entry RuleID = %q, want overwritten response", got.RuleID)
	}
	if _, ok := cache.Get(third); !ok {
		t.Fatal("third entry missed after insert")
	}
}

func TestSafeAllowCacheRejectsUnsafeOrIneligibleDecisions(t *testing.T) {
	t.Parallel()

	clock := &cacheTestClock{now: time.Date(2026, 5, 2, 14, 0, 0, 0, time.UTC)}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 16}, clock)
	baseReq := safeAllowCacheTestRequest()
	baseResp := safeAllowCacheTestResponse()

	cases := []struct {
		name string
		req  SafeAllowCacheRequest
		resp EvaluateResponse
	}{
		{name: "high-risk destructive", req: mutateSafeAllowCacheRequest(baseReq, func(v *SafeAllowCacheRequest) { v.RiskTags = []string{"exec", "destructive"} }), resp: baseResp},
		{name: "unknown risk", req: mutateSafeAllowCacheRequest(baseReq, func(v *SafeAllowCacheRequest) { v.RiskTags = []string{"unknown", "review_required"} }), resp: baseResp},
		{name: "missing safe class", req: mutateSafeAllowCacheRequest(baseReq, func(v *SafeAllowCacheRequest) { delete(v.Labels, "command.class") }), resp: baseResp},
		{name: "approval retry request", req: mutateSafeAllowCacheRequest(baseReq, func(v *SafeAllowCacheRequest) { v.ApprovalRef = "edge_appr_cache_test" }), resp: baseResp},
		{name: "deny", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) { v.Decision = string(edgecore.DecisionDeny); v.PermissionDecision = "deny" })},
		{name: "requires approval", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) {
			v.Decision = string(edgecore.DecisionRequireApproval)
			v.PermissionDecision = "deny"
			v.ApprovalRef = "edge_appr_cache_test"
		})},
		{name: "throttle", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) {
			v.Decision = string(edgecore.DecisionThrottle)
			v.PermissionDecision = "deny"
		})},
		{name: "constrain", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) {
			v.Decision = string(edgecore.DecisionConstrain)
			v.UpdatedInput = map[string]any{"command": "npm ci"}
		})},
		{name: "approval-derived allow", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) { v.ApprovalRef = "edge_appr_cache_test" })},
		{name: "degraded allow", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) { v.Degraded = true; v.ErrorCode = "gateway_timeout" })},
		{name: "gateway not cache eligible", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) { v.CacheEligible = false })},
		{name: "permission deny despite allow decision", req: baseReq, resp: mutateSafeAllowCacheResponse(baseResp, func(v *EvaluateResponse) { v.PermissionDecision = "deny" })},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mutateSafeAllowCacheRequest(tc.req, func(v *SafeAllowCacheRequest) {
				v.ActionHash = "sha256:" + strings.NewReplacer(" ", "-", "/", "-").Replace(tc.name)
			})
			if cache.Put(req, tc.resp) {
				t.Fatalf("Put returned true for ineligible case %q", tc.name)
			}
			if got, ok := cache.Get(req); ok {
				t.Fatalf("ineligible case %q hit cache with %#v; want miss", tc.name, got)
			}
		})
	}
}

func TestSafeAllowCacheStoresOnlyBoundedDecisionMetadata(t *testing.T) {
	t.Parallel()

	clock := &cacheTestClock{now: time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 2}, clock)
	req := safeAllowCacheTestRequest()
	req.Labels["note"] = "Bearer cache-secret-token"
	req.InputRedacted = map[string]any{"command": "echo Bearer cache-secret-token"}
	resp := safeAllowCacheTestResponse()
	resp.Reason = strings.Repeat("safe allow ", 512) + "Bearer cache-secret-token"
	resp.TerminalMessage = strings.Repeat("terminal ", 512) + "sk-cache-secret"

	if !cache.Put(req, resp) {
		t.Fatal("Put returned false for safe cache-eligible ALLOW")
	}
	got, ok := cache.Get(req)
	if !ok {
		t.Fatal("Get after Put missed")
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal cached response: %v", err)
	}
	text := string(payload)
	for _, secret := range []string{"cache-secret-token", "sk-cache-secret", "Bearer cache-secret-token"} {
		if strings.Contains(text, secret) {
			t.Fatalf("cached response leaked secret %q: %s", secret, text)
		}
	}
	if len(got.Reason) > MaxGatewayMetadataValueBytes+8 {
		t.Fatalf("cached reason length = %d, want bounded", len(got.Reason))
	}
	if len(got.TerminalMessage) > MaxGatewayMetadataValueBytes+8 {
		t.Fatalf("cached terminal message length = %d, want bounded", len(got.TerminalMessage))
	}
}

type cacheTestClock struct {
	now time.Time
}

func (c *cacheTestClock) Now() time.Time {
	return c.now
}

func (c *cacheTestClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func safeAllowCacheTestRequest() SafeAllowCacheRequest {
	return SafeAllowCacheRequest{
		TenantID:       "tenant-cache-a",
		PolicyMode:     edgecore.PolicyModeEnforce,
		PolicySnapshot: "snap-cache-a",
		Kind:           "hook.pre_tool_use",
		ActionName:     "bash.exec",
		Capability:     "exec.shell",
		RiskTags:       []string{"exec", "test"},
		Labels: map[string]string{
			"command.class":  "safe",
			"command.family": "test",
		},
		ActionHash: "sha256:action-a",
		InputHash:  "sha256:input-a",
	}
}

func safeAllowCacheTestResponse() EvaluateResponse {
	return EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		Reason:             "safe command allowed",
		RuleID:             "edge.safe-cache.allow",
		PolicySnapshot:     "snap-cache-a",
		ActionHash:         "sha256:action-a",
		InputHash:          "sha256:input-a",
		PermissionDecision: "allow",
		ExitCode:           0,
		TerminalTitle:      "",
		TerminalMessage:    "",
		CacheEligible:      true,
	}
}

func mutateSafeAllowCacheRequest(req SafeAllowCacheRequest, mutate func(*SafeAllowCacheRequest)) SafeAllowCacheRequest {
	req.RiskTags = append([]string(nil), req.RiskTags...)
	if req.Labels != nil {
		labels := make(map[string]string, len(req.Labels))
		for k, v := range req.Labels {
			labels[k] = v
		}
		req.Labels = labels
	}
	if req.InputRedacted != nil {
		input := make(map[string]any, len(req.InputRedacted))
		for k, v := range req.InputRedacted {
			input[k] = v
		}
		req.InputRedacted = input
	}
	mutate(&req)
	return req
}

func mutateSafeAllowCacheResponse(resp EvaluateResponse, mutate func(*EvaluateResponse)) EvaluateResponse {
	if resp.UpdatedInput != nil {
		updated := make(map[string]any, len(resp.UpdatedInput))
		for k, v := range resp.UpdatedInput {
			updated[k] = v
		}
		resp.UpdatedInput = updated
	}
	if resp.Constraints != nil {
		constraints := make(map[string]any, len(resp.Constraints))
		for k, v := range resp.Constraints {
			constraints[k] = v
		}
		resp.Constraints = constraints
	}
	mutate(&resp)
	return resp
}
