// EDGE-143.5 — tests for §10.2 query-param validation on the shadow
// agents list handler. Lives in its own file to avoid collision with
// task-4cd8299f on handlers_edge_shadow_agents_test.go (per chat
// msg-901f7405 file-collision warning).
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow"
)

// TestShadowAgentListExtensions_Validation hits parseShadowFindingListQuery
// through the full handler with §10.2 query params: each invalid input
// returns 400+edgeErrCodeInvalidRequest; valid inputs return 200.
func TestShadowAgentListExtensions_Validation(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		want   int
		reason string
	}{
		// Invalid §10.2 params → 400.
		{"invalid_source_type", "/api/v1/edge/shadow-agents?source_type=cosmic", http.StatusBadRequest, "source_type not in enum"},
		{"invalid_ci_provider", "/api/v1/edge/shadow-agents?ci_provider=bamboo", http.StatusBadRequest, "ci_provider not in enum"},
		{"repo_without_ci_provider", "/api/v1/edge/shadow-agents?repo=acme/x", http.StatusBadRequest, "repo requires ci_provider"},
		{"confidence_min_too_high", "/api/v1/edge/shadow-agents?confidence_min=1.5", http.StatusBadRequest, "confidence_min > 1"},
		{"confidence_min_negative", "/api/v1/edge/shadow-agents?confidence_min=-0.1", http.StatusBadRequest, "confidence_min < 0"},
		{"confidence_min_not_a_number", "/api/v1/edge/shadow-agents?confidence_min=abc", http.StatusBadRequest, "confidence_min malformed"},
		{"confidence_min_nan", "/api/v1/edge/shadow-agents?confidence_min=NaN", http.StatusBadRequest, "confidence_min NaN bypass"},
		{"confidence_min_inf", "/api/v1/edge/shadow-agents?confidence_min=+Inf", http.StatusBadRequest, "confidence_min Inf bypass"},
		{"first_seen_after_malformed", "/api/v1/edge/shadow-agents?first_seen_after=tomorrow", http.StatusBadRequest, "first_seen_after not RFC3339"},
		{"last_seen_before_malformed", "/api/v1/edge/shadow-agents?last_seen_before=2026-99-99T00:00:00Z", http.StatusBadRequest, "last_seen_before invalid date"},
		{"invalid_signal_shape", "/api/v1/edge/shadow-agents?signal=HAS-CAPS", http.StatusBadRequest, "signal regex"},
		{"include_managed_skip_bad", "/api/v1/edge/shadow-agents?include_managed_skip=maybe", http.StatusBadRequest, "boolean parse"},

		// Valid §10.2 params → 200.
		{"valid_source_type", "/api/v1/edge/shadow-agents?source_type=kubernetes", http.StatusOK, "happy"},
		{"valid_ci_provider_plus_repo", "/api/v1/edge/shadow-agents?ci_provider=github_actions&repo=acme/x", http.StatusOK, "composite ok"},
		{"valid_confidence_min", "/api/v1/edge/shadow-agents?confidence_min=0.5", http.StatusOK, "in range"},
		{"valid_first_seen_after", "/api/v1/edge/shadow-agents?first_seen_after=2026-01-01T00:00:00Z", http.StatusOK, "RFC3339"},
		{"valid_repeated_signals", "/api/v1/edge/shadow-agents?signal=k8s_heartbeat_missing&signal=ci_fork_pr", http.StatusOK, "any-of"},
		{"valid_include_managed_skip", "/api/v1/edge/shadow-agents?include_managed_skip=true", http.StatusOK, "boolean true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newShadowGateway(t)
			rec := getShadow(t, s, "tenant-a", tc.path)
			if rec.Code != tc.want {
				t.Fatalf("%s: got %d, want %d (%s); body=%s", tc.name, rec.Code, tc.want, tc.reason, rec.Body.String())
			}
		})
	}
}

// TestShadowAgentListExtensions_LimitCappedAtMax asserts the gateway
// boundary enforces shadow.MaxListPageSize on the caller-supplied
// `?limit=` query value. Without a boundary cap, an oversized
// caller value flows into shadow.ListFindingsQuery.Limit and depends
// solely on the store-layer clampListPageSize re-clamp. This test
// pins boundary-level defense-in-depth required by task-7beba845 DoD
// ("Boundary-level cap applied (request decode) AND store-level cap").
func TestShadowAgentListExtensions_LimitCappedAtMax(t *testing.T) {
	t.Run("parse layer clamps adversarial limit to MaxListPageSize", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/edge/shadow-agents?limit=99999999", nil)
		rec := httptest.NewRecorder()
		q, ok := parseShadowFindingListQuery(rec, req, "tenant-a")
		if !ok {
			t.Fatalf("parseShadowFindingListQuery rejected adversarial limit, want clamped+ok=true; body=%s", rec.Body.String())
		}
		if q.Limit > shadow.MaxListPageSize {
			t.Fatalf("parsed q.Limit = %d, want <= MaxListPageSize=%d (boundary cap missing)", q.Limit, shadow.MaxListPageSize)
		}
		if q.Limit != shadow.MaxListPageSize {
			t.Fatalf("parsed q.Limit = %d, want exactly MaxListPageSize=%d when caller asks for more", q.Limit, shadow.MaxListPageSize)
		}
	})

	t.Run("parse layer preserves under-cap limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/edge/shadow-agents?limit=42", nil)
		rec := httptest.NewRecorder()
		q, ok := parseShadowFindingListQuery(rec, req, "tenant-a")
		if !ok {
			t.Fatalf("parseShadowFindingListQuery rejected limit=42; body=%s", rec.Body.String())
		}
		if q.Limit != 42 {
			t.Fatalf("parsed q.Limit = %d, want 42 (under-cap value must pass through)", q.Limit)
		}
	})

	t.Run("parse layer preserves omitted limit as zero so store applies default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/edge/shadow-agents", nil)
		rec := httptest.NewRecorder()
		q, ok := parseShadowFindingListQuery(rec, req, "tenant-a")
		if !ok {
			t.Fatalf("parseShadowFindingListQuery rejected no-limit query; body=%s", rec.Body.String())
		}
		if q.Limit != 0 {
			t.Fatalf("parsed q.Limit = %d, want 0 (omitted → store applies DefaultListPageSize)", q.Limit)
		}
	})

	t.Run("http end-to-end caps page even when client requests millions", func(t *testing.T) {
		s := newShadowGateway(t)
		// Seed > MaxListPageSize findings so the cap actually fires.
		const total = shadow.MaxListPageSize + 25
		for i := 0; i < total; i++ {
			body := validShadowCreateBody("tenant-cap")
			body.AgentID = ""
			rec := postShadow(t, s, "tenant-cap", "/api/v1/edge/shadow-agents", body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("seed[%d] create status = %d; body=%s", i, rec.Code, rec.Body.String())
			}
		}
		rec := getShadow(t, s, "tenant-cap", "/api/v1/edge/shadow-agents?limit=99999999")
		if rec.Code != http.StatusOK {
			t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var page shadow.FindingPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(page.Findings) > shadow.MaxListPageSize {
			t.Fatalf("list returned %d findings, exceeds MaxListPageSize=%d", len(page.Findings), shadow.MaxListPageSize)
		}
		if len(page.Findings) != shadow.MaxListPageSize {
			t.Fatalf("list returned %d findings, want exactly MaxListPageSize=%d (enough seeded)", len(page.Findings), shadow.MaxListPageSize)
		}
	})
}

// TestShadowAgentListExtensions_SignalCap asserts the per-request signal
// list is capped at 16 entries (DoS prevention per design §10.5 spirit).
func TestShadowAgentListExtensions_SignalCap(t *testing.T) {
	s := newShadowGateway(t)
	path := "/api/v1/edge/shadow-agents?"
	for i := 0; i < 17; i++ {
		if i > 0 {
			path += "&"
		}
		path += "signal=sig_" + string(rune('a'+i%26)) + "_x"
	}
	rec := getShadow(t, s, "tenant-a", path)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("17 signals = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
