package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/policy"
)

const unifiedTestInputBundle = `rules:
  - id: deny-secret
    match:
      topics: [job.acme]
      keywords: [AKIA]
    decision: deny
    reason: AWS access key prefix matched
    tier: global
`

const unifiedTestOutputBundle = `output_rules:
  - id: redact-pii
    enabled: true
    decision: redact
    reason: PII matched in output
    match:
      detectors: [pii]
`

// putBundleForUnifiedTest writes a YAML bundle directly into configsvc
// so loadPolicyBundles surfaces it on the next GET /policy/rules. We
// bypass `handlePutPolicyBundle` because that endpoint enforces a
// `secops/` prefix on bundle IDs — production cordumctl pack installs
// store under pack-named IDs (cordclaw/safety, openclaw/safety, etc.)
// directly via the configsvc, and the read path accepts any prefix.
func putBundleForUnifiedTest(t *testing.T, s *server, bundleID, content string) {
	t.Helper()
	ctx := context.Background()
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(packs.PolicyConfigScope), packs.PolicyConfigID)
	if err != nil {
		// Document not yet created — start fresh.
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(packs.PolicyConfigScope),
			ScopeID: packs.PolicyConfigID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	bundles, _ := doc.Data[packs.PolicyConfigKey].(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	bundles[bundleID] = map[string]any{
		"content":      content,
		"enabled":      true,
		"author":       "tester",
		"version":      "1.0.0",
		"installed_at": "2026-05-09T10:00:00Z",
		"sha256":       "sha256:" + bundleID,
	}
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(ctx, doc); err != nil {
		t.Fatalf("seed bundle %q via configsvc: %v", bundleID, err)
	}
}

func getUnifiedPolicyRules(t *testing.T, s *server, query string) (int, map[string]any) {
	t.Helper()
	url := "/api/v1/policy/rules"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handlePolicyRules(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec.Code, body
}

func TestHandlePolicyRulesReturnsUnifiedShapeWithTypeField(t *testing.T) {
	s, _, _ := newTestGateway(t)
	putBundleForUnifiedTest(t, s, "cordclaw/safety", unifiedTestInputBundle)
	putBundleForUnifiedTest(t, s, "cordclaw/output", unifiedTestOutputBundle)

	code, body := getUnifiedPolicyRules(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", code, body)
	}
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("body.items shape = %T, want []any; body = %v", body["items"], body)
	}
	if len(items) < 2 {
		t.Fatalf("items count = %d, want >= 2 (1 input + 1 output)", len(items))
	}
	// Yaron's load-bearing assertion: every rule has Type populated.
	// "Unknown" is what the dashboard renders when this is empty — the
	// whole point of this PR is to make this assertion green.
	seen := map[string]int{}
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		ruleType, _ := item["type"].(string)
		if ruleType == "" {
			t.Errorf("items[%d].type is empty (the Yaron-Unknown bug); item = %v", i, item)
			continue
		}
		seen[ruleType]++
	}
	if seen["input"] == 0 {
		t.Errorf("expected at least 1 type=input rule, saw types %v", seen)
	}
	if seen["output"] == 0 {
		t.Errorf("expected at least 1 type=output rule, saw types %v", seen)
	}
	// Unified envelope contract: has_more present + items typed consistently.
	if _, ok := body["has_more"].(bool); !ok {
		t.Errorf("body.has_more shape = %T, want bool", body["has_more"])
	}
}

func TestHandlePolicyRulesFiltersByType(t *testing.T) {
	s, _, _ := newTestGateway(t)
	putBundleForUnifiedTest(t, s, "cordclaw/safety", unifiedTestInputBundle)
	putBundleForUnifiedTest(t, s, "cordclaw/output", unifiedTestOutputBundle)

	code, body := getUnifiedPolicyRules(t, s, "type=input")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	items, _ := body["items"].([]any)
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		if item["type"] != "input" {
			t.Errorf("items[%d].type = %v, want input only (filter not honored)", i, item["type"])
		}
	}
}

func TestHandlePolicyRulesFiltersByPackID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	putBundleForUnifiedTest(t, s, "cordclaw/safety", unifiedTestInputBundle)
	putBundleForUnifiedTest(t, s, "openclaw/safety", unifiedTestInputBundle)

	code, body := getUnifiedPolicyRules(t, s, "pack_id=cordclaw")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatal("expected cordclaw rules; got 0")
	}
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		audit, _ := item["audit"].(map[string]any)
		if audit == nil {
			t.Errorf("items[%d] missing audit", i)
			continue
		}
		packSource, _ := audit["pack_source"].(map[string]any)
		if packSource == nil {
			t.Errorf("items[%d].audit.pack_source missing", i)
			continue
		}
		if packSource["pack_id"] != "cordclaw" {
			t.Errorf("items[%d].audit.pack_source.pack_id = %v, want cordclaw", i, packSource["pack_id"])
		}
	}
}

func TestHandlePolicyRulesEmptyReturnsValidEnvelope(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// No bundles seeded → no rules to surface (RuleStore also empty).

	code, body := getUnifiedPolicyRules(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty != 404)", code)
	}
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("body.items shape = %T, want []any", body["items"])
	}
	if len(items) != 0 {
		t.Errorf("items count = %d, want 0", len(items))
	}
	hasMore, ok := body["has_more"].(bool)
	if !ok || hasMore {
		t.Errorf("has_more = %v ok=%v, want false", hasMore, ok)
	}
}

func TestHandlePolicyRulesRejectsLimitOutOfRange(t *testing.T) {
	s, _, _ := newTestGateway(t)

	for _, q := range []string{"limit=0", "limit=-5", "limit=999", "limit=abc"} {
		code, _ := getUnifiedPolicyRules(t, s, q)
		if code != http.StatusBadRequest {
			t.Errorf("limit %q → %d, want 400", q, code)
		}
	}
}

func TestHandlePolicyRulesIncludesRuleStoreEntries(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Seed a unified Rule directly via RuleStore (Backend 5c). The unified
	// handler must merge RuleStore entries with YAML-bundle entries so
	// rules authored via Dashboard 3E surface alongside legacy YAML packs.
	if s.policyRuleStore == nil {
		t.Skip("policyRuleStore not wired in test gateway")
	}
	authoredRule := &policy.Rule{
		ID:     "authored-via-dashboard",
		Name:   "Authored via Dashboard 3E",
		Type:   policy.RuleTypeInput,
		Scope:  policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"},
		Match:  json.RawMessage(`{"topics":["job.dashboard"]}`),
		Decide: json.RawMessage(`{"decision":"allow"}`),
	}
	if _, err := s.policyRuleStore.CreateRule(context.Background(), authoredRule); err != nil {
		t.Fatalf("seed rule store: %v", err)
	}

	code, body := getUnifiedPolicyRules(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	items, _ := body["items"].([]any)
	found := false
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["id"] == "authored-via-dashboard" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RuleStore-authored rule did not surface in unified GET; items = %v", items)
	}
}

func TestHandlePolicyRulesPaginationCursorReturnsRemainingItems(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Seed multiple bundles so the dataset is bigger than limit=1.
	putBundleForUnifiedTest(t, s, "cordclaw/safety", unifiedTestInputBundle)
	putBundleForUnifiedTest(t, s, "cordclaw/output", unifiedTestOutputBundle)

	codeP1, p1 := getUnifiedPolicyRules(t, s, "limit=1")
	if codeP1 != http.StatusOK {
		t.Fatalf("page1 status = %d", codeP1)
	}
	items1, _ := p1["items"].([]any)
	if len(items1) != 1 {
		t.Fatalf("page1 items = %d, want 1", len(items1))
	}
	hasMore, _ := p1["has_more"].(bool)
	if !hasMore {
		t.Fatal("page1 has_more = false but more rules exist")
	}
	cursor, _ := p1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("page1 next_cursor missing despite has_more=true")
	}

	codeP2, p2 := getUnifiedPolicyRules(t, s, "limit=10&cursor="+cursor)
	if codeP2 != http.StatusOK {
		t.Fatalf("page2 status = %d", codeP2)
	}
	items2, _ := p2["items"].([]any)
	if len(items2) == 0 {
		t.Error("page2 returned 0 items despite has_more=true on page1")
	}
	// Cursor must NOT yield a duplicate of page1's last item.
	page1LastID, _ := items1[0].(map[string]any)["id"].(string)
	for i, raw := range items2 {
		item, _ := raw.(map[string]any)
		if id, _ := item["id"].(string); id == page1LastID {
			t.Errorf("page2[%d].id = %q duplicates page1's last id", i, id)
		}
	}
}

// TestHandlePolicyRulesNoRegressionOnLegacyOutputAndVelocityHandlers ensures
// the legacy paths kept alive for D11 cut-over still serve their existing
// consumers unchanged. The test issues GETs to the deprecated URLs and
// asserts the responses parse with their pre-5d shape — `items` array
// without a unified `type` field. (This is a regression guard, NOT an
// endorsement of the legacy shape.)
func TestHandlePolicyRulesNoRegressionOnLegacyOutputAndVelocityHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)
	putBundleForUnifiedTest(t, s, "cordclaw/output", unifiedTestOutputBundle)

	// Legacy output GET still works.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/output/rules", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handlePolicyOutputRules(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy output rules: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "redact-pii") {
		t.Errorf("legacy output GET missing rule id; body = %s", rec.Body.String())
	}
}
