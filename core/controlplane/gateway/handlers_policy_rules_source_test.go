package gateway

import (
	"net/http"
	"testing"
)

// TestHandlePolicyRulesSurfacesAllPackSourceFields is the load-bearing
// assertion that the legacy `source` block (pack_id, fragment_id,
// overlay_name, version, installed_at, sha256) flows through the
// unified Rule envelope without lossy translation. The dashboard's
// pack_id + tier columns + Source filter depend on this.
func TestHandlePolicyRulesSurfacesAllPackSourceFields(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Bundle id "cordclaw/safety" → fragment_id="cordclaw/safety",
	// pack_id="cordclaw", overlay_name="safety". version+installed_at+
	// sha256 come from the seeded bundle metadata in
	// putBundleForUnifiedTest.
	putBundleForUnifiedTest(t, s, "cordclaw/safety", unifiedTestInputBundle)

	code, body := getUnifiedPolicyRules(t, s, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", code, body)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("body.items shape/empty; body = %v", body)
	}

	first, _ := items[0].(map[string]any)
	audit, _ := first["audit"].(map[string]any)
	if audit == nil {
		t.Fatalf("items[0].audit missing; item = %v", first)
	}
	pack, _ := audit["pack_source"].(map[string]any)
	if pack == nil {
		t.Fatalf("items[0].audit.pack_source missing; audit = %v", audit)
	}

	cases := []struct {
		field string
		want  string
	}{
		{"fragment_id", "cordclaw/safety"},
		{"pack_id", "cordclaw"},
		{"overlay_name", "safety"},
		{"version", "1.0.0"},
		{"installed_at", "2026-05-09T10:00:00Z"},
		{"sha256", "sha256:cordclaw/safety"},
	}
	for _, c := range cases {
		if got, _ := pack[c.field].(string); got != c.want {
			t.Errorf("pack_source.%s = %q, want %q", c.field, got, c.want)
		}
	}
}
