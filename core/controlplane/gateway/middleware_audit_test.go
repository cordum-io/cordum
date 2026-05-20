package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// captureAuditSender records every SIEMEvent forwarded to it so tests
// can assert the producer-side TenantID. Mirrors the recordingSender
// pattern used in other gateway tests but lives in this file to keep
// the audit-focused fixtures self-contained.
type captureAuditSender struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (c *captureAuditSender) Send(e audit.SIEMEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captureAuditSender) Close() error { return nil }

func (c *captureAuditSender) snapshot() []audit.SIEMEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.SIEMEvent, len(c.events))
	copy(out, c.events)
	return out
}

// TestMiddlewareAuditReadAttributesTenantIDWhenAuthCtxNil asserts the
// producer-side fix at middleware.go's auditReadMiddleware: when the
// request has no auth context (anonymous read) the emitted SIEMEvent
// MUST carry TenantID = model.DefaultTenant, NOT empty string. Without
// this, the audit chain sink-level fallback would still attribute the
// event but at the higher slog.Warn level (per task-3fad45d3 Phase 4
// downgrade) — surfacing as a per-request producer-bug log line on
// every anonymous read. Producer-side attribution silences the warn.
func TestMiddlewareAuditReadAttributesTenantIDWhenAuthCtxNil(t *testing.T) {
	t.Parallel()
	cap := &captureAuditSender{}
	// sampleRate=1.0 forces every GET to audit so we don't depend on the
	// crypto/rand sampling path. /api/policy is on the always-audit list
	// per isSensitiveRead, so even sampleRate=0 would emit; we set 1.0
	// for the test signal regardless.
	handler := auditReadMiddleware(cap, 1.0, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/policy", nil)
	// Intentionally no auth context on req.Context() — simulates the
	// anonymous-read code path.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].TenantID != "default" {
		t.Fatalf("TenantID = %q, want %q (model.DefaultTenant — anonymous-read must default)",
			events[0].TenantID, "default")
	}
}

// TestMiddlewareAuditReadHonorsXTenantIDHeader asserts the second tier
// of the resolver priority chain: when authCtx is nil but the request
// carries an X-Tenant-ID header (some pre-auth or anonymous-but-known
// callers do this), the audit event lands on that tenant chain rather
// than the default. Preserves per-tenant chain isolation even on
// unauthenticated paths.
func TestMiddlewareAuditReadHonorsXTenantIDHeader(t *testing.T) {
	t.Parallel()
	cap := &captureAuditSender{}
	handler := auditReadMiddleware(cap, 1.0, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/policy", nil)
	req.Header.Set("X-Tenant-ID", "tenant-anonymous-but-known")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].TenantID != "tenant-anonymous-but-known" {
		t.Fatalf("TenantID = %q, want %q (X-Tenant-ID header should beat default)",
			events[0].TenantID, "tenant-anonymous-but-known")
	}
}

// driveAuthFailure exercises apiKeyMiddleware on /api/v1/jobs with a
// captureAuditSender attached so callers can inspect the emitted
// SIEMEvent. When scopes is non-nil the provider grants those scopes
// and POST /api/v1/jobs (jobs:write required) triggers the
// `key_scope_insufficient` SIEMEvent path. When scopes is nil the
// caller's apiKey is rejected outright, triggering the
// `request_auth_failed` SIEMEvent path. tenantHeader is set verbatim
// when non-empty (so callers can also pass whitespace-only values to
// exercise the trim path inside ResolveTenantForAudit).
func driveAuthFailure(t *testing.T, scopes []string, apiKey string, tenantHeader string) []audit.SIEMEvent {
	t.Helper()
	cap := &captureAuditSender{}
	var provider *auth.BasicAuthProvider
	if scopes != nil {
		provider = newScopedAPIKeyAuthProvider(t, scopes)
	} else {
		// nil scopes ⇒ wrong-secret path: provider has no matching key,
		// so ValidateKey returns a non-ScopeError.
		provider = newScopedAPIKeyAuthProvider(t, []string{"jobs:read"})
	}
	handler := apiKeyMiddleware(provider, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("auth-failure path should not reach handler")
	}), cap)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req.Header.Set("X-API-Key", apiKey)
	if tenantHeader != "" {
		req.Header.Set("X-Tenant-ID", tenantHeader)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	provider.DrainUsage()
	return cap.snapshot()
}

// TestApiKeyMiddlewareAttributesTenantIDOnScopeInsufficient asserts
// the producer-side fix at middleware.go's apiKeyMiddleware
// `key_scope_insufficient` branch: the emitted SIEMEvent MUST carry a
// non-empty TenantID resolved via model.ResolveTenantForAudit, even
// though authCtx is nil because auth itself failed. Mirrors the
// auditReadMiddleware pattern (parent task-3fad45d3 commit 3094dde7).
// Without this, the audit chain sink-level fallback would attribute
// the event at slog.Warn level on every API-key auth failure.
func TestApiKeyMiddlewareAttributesTenantIDOnScopeInsufficient(t *testing.T) {
	cases := []struct {
		name         string
		tenantHeader string
		want         string
	}{
		{
			name:         "header_present",
			tenantHeader: "tnt_test_123",
			want:         "tnt_test_123",
		},
		{
			name:         "header_absent_defaults",
			tenantHeader: "",
			want:         model.DefaultTenant,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// no t.Parallel: driveAuthFailure → newScopedAPIKeyAuthProvider uses t.Setenv.
			events := driveAuthFailure(t, []string{"jobs:read"}, "scoped-test-secret", tc.tenantHeader)
			if len(events) != 1 {
				t.Fatalf("expected 1 audit event, got %d", len(events))
			}
			if events[0].Reason != "key_scope_insufficient" {
				t.Fatalf("Reason = %q, want %q", events[0].Reason, "key_scope_insufficient")
			}
			if events[0].TenantID != tc.want {
				t.Fatalf("TenantID = %q, want %q (scope_insufficient producer must attribute)",
					events[0].TenantID, tc.want)
			}
		})
	}
}

// TestApiKeyMiddlewareAttributesTenantIDOnRequestAuthFailed mirrors
// the scope_insufficient assertion for the second auth-failure branch
// at middleware.go's apiKeyMiddleware `request_auth_failed` path
// (wrong API key, not a ScopeError). Both branches share the same
// producer-side requirement: non-empty TenantID from
// ResolveTenantForAudit even when authCtx is nil.
func TestApiKeyMiddlewareAttributesTenantIDOnRequestAuthFailed(t *testing.T) {
	cases := []struct {
		name         string
		tenantHeader string
		want         string
	}{
		{
			name:         "header_present",
			tenantHeader: "tnt_test_456",
			want:         "tnt_test_456",
		},
		{
			name:         "header_absent_defaults",
			tenantHeader: "",
			want:         model.DefaultTenant,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// no t.Parallel: driveAuthFailure → newScopedAPIKeyAuthProvider uses t.Setenv.
			// nil scopes signals wrong-key path; the provider rejects the
			// non-matching secret with a plain error (not ScopeError).
			events := driveAuthFailure(t, nil, "definitely-not-the-real-secret", tc.tenantHeader)
			if len(events) != 1 {
				t.Fatalf("expected 1 audit event, got %d", len(events))
			}
			if events[0].Reason != "request_auth_failed" {
				t.Fatalf("Reason = %q, want %q", events[0].Reason, "request_auth_failed")
			}
			if events[0].TenantID != tc.want {
				t.Fatalf("TenantID = %q, want %q (request_auth_failed producer must attribute)",
					events[0].TenantID, tc.want)
			}
		})
	}
}

// TestAuditChainSenderTenantlessCounterOnAuthFailureMix is the
// regression-defense companion to the two producer-site tests above.
// Drives a mix of scope_insufficient + request_auth_failed requests
// (with and without X-Tenant-ID header) through apiKeyMiddleware
// chained into a real auditChainSender + miniredis chainer, and
// asserts ZERO emissions of the slog.Warn "audit chain: tenantless
// event" fallback message. After the producer-side fix, the sink-level
// fallback (audit_chain_sender.go:59) MUST NOT fire under any
// auth-failure mix — that fallback only exists to catch NEW producer
// sites that forget to attribute (task rail #2).
//
// Sequential (not t.Parallel) because slog.SetDefault is global and
// must not race with sibling tests' log output landing in the buffer.
func TestAuditChainSenderTenantlessCounterOnAuthFailureMix(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	defer func() { _ = client.Close() }()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	chainer := audit.NewChainer(client, "")
	downstream := &captureAuditSender{}
	sender := newAuditChainSender(chainer, downstream)

	provider := newScopedAPIKeyAuthProvider(t, []string{"jobs:read"})
	handler := apiKeyMiddleware(provider, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("auth-failure path should not reach handler")
	}), sender)

	mix := []struct {
		apiKey       string
		tenantHeader string
	}{
		{apiKey: "scoped-test-secret", tenantHeader: "tnt_a"}, // scope_insufficient, header
		{apiKey: "scoped-test-secret", tenantHeader: ""},      // scope_insufficient, no header (default)
		{apiKey: "wrong-secret-1", tenantHeader: "tnt_b"},     // request_auth_failed, header
		{apiKey: "wrong-secret-2", tenantHeader: ""},          // request_auth_failed, no header (default)
		{apiKey: "scoped-test-secret", tenantHeader: "   "},   // whitespace-only header → defaults
	}
	for _, r := range mix {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
		req.Header.Set("X-API-Key", r.apiKey)
		if r.tenantHeader != "" {
			req.Header.Set("X-Tenant-ID", r.tenantHeader)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	provider.DrainUsage()

	// Confirm we actually drove the audit path before checking the warn count
	// (otherwise an empty buffer would pass for the wrong reason).
	downstreamEvents := downstream.snapshot()
	if len(downstreamEvents) < len(mix) {
		t.Fatalf("downstream sink received %d events, want >= %d (test harness wired wrong)",
			len(downstreamEvents), len(mix))
	}

	count := strings.Count(buf.String(), "audit chain: tenantless event")
	if count != 0 {
		t.Fatalf("slog.Warn 'audit chain: tenantless event' fired %d times under normal auth-failure mix; "+
			"want 0 (producer-side attribution must silence sink fallback)\n--- slog buffer ---\n%s",
			count, buf.String())
	}

	// Belt-and-suspenders: every forwarded event must carry a non-empty TenantID
	// after the producer-side fix. If any event has TenantID="" we'd be relying
	// on the sink-level rewrite, which defeats the whole producer-side fix.
	for i, ev := range downstreamEvents {
		if strings.TrimSpace(ev.TenantID) == "" {
			t.Fatalf("downstream event[%d] has empty TenantID after producer fix (reason=%q)",
				i, ev.Reason)
		}
	}
}
