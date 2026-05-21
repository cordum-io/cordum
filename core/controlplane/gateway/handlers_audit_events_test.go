package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// seedAuditEvents appends customizable SIEM events through the real Chainer
// to the gateway's shared Redis client and installs the chainer on the
// server. Returns the appended events (with Seq/EventHash populated by
// Append) so tests can assert against the wire payload.
func seedAuditEvents(t *testing.T, s *server, tenant string, events []audit.SIEMEvent) []audit.SIEMEvent {
	t.Helper()
	chainer := audit.NewChainer(s.redisClient(), "")
	s.auditChainer = chainer
	out := make([]audit.SIEMEvent, 0, len(events))
	for i := range events {
		ev := events[i]
		ev.TenantID = tenant
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		}
		if err := chainer.Append(context.Background(), &ev); err != nil {
			t.Fatalf("seed append[%d]: %v", i, err)
		}
		out = append(out, ev)
	}
	return out
}

func decodeAuditEventsResponse(t *testing.T, rec *httptest.ResponseRecorder) auditEventsResponse {
	t.Helper()
	// Copy the bytes off the recorder before decoding: json.NewDecoder
	// reads from rec.Body and advances its read cursor, so a subsequent
	// rec.Body.String() in the error path (and any caller that wants to
	// re-scan the body for secret-leak checks) sees an empty buffer. The
	// copy is cheap (single-page responses) and preserves the recorder's
	// state for the rest of the test.
	body := append([]byte(nil), rec.Body.Bytes()...)
	var out auditEventsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, string(body))
	}
	return out
}

func TestHandleAuditEvents_400ErrorEnvelopeIncludesCode(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		expectedCode string
	}{
		{name: "invalid cursor", query: "cursor=invalid", expectedCode: "INVALID_CURSOR"},
		{name: "invalid limit", query: "limit=abc", expectedCode: "INVALID_LIMIT"},
		{name: "invalid from", query: "from=garbage", expectedCode: "INVALID_FROM"},
		{name: "invalid to", query: "to=garbage", expectedCode: "INVALID_TO"},
		{
			name:         "inverted range",
			query:        "from=2020-01-01T00:00:00Z&to=2019-01-01T00:00:00Z",
			expectedCode: "INVALID_RANGE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			req := adminCtx(httptest.NewRequest(
				http.MethodGet,
				"/api/v1/audit/events?tenant=default&"+tc.query,
				nil,
			))
			rec := httptest.NewRecorder()

			s.handleListAuditEvents(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != tc.expectedCode {
				t.Fatalf("code=%q, want %q; body=%+v", body.Code, tc.expectedCode, body)
			}
			if body.Error == "" {
				t.Fatalf("error field is empty; body=%+v", body)
			}
		})
	}
}

// TestHandleAuditEvents_RequiresAuditReadPerm pins the legacy-role wall:
// when RBAC is not entitled, a viewer must be rejected by the legacyRoles
// gate even though the basic-role mapping grants audit.read. Matches the
// admin-only convention used by /audit/verify and /policy/audit.
func TestHandleAuditEvents_RequiresAuditReadPerm(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "viewer"}
	authCtx := &auth.AuthContext{Tenant: "default", Role: "viewer", PrincipalID: "v1"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=default", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()
	s.handleListAuditEvents(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleAuditEvents_TenantScoped pins that streams for tenants other
// than the caller's authenticated tenant are never returned, even when the
// auth provider rejects cross-tenant override. Mirrors handlers_audit_verify
// tenant scoping.
func TestHandleAuditEvents_TenantScoped(t *testing.T) {
	s, _, _ := newTestGateway(t)
	tenantA := []audit.SIEMEvent{
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "a1"},
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "a2"},
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "a3"},
	}
	tenantB := []audit.SIEMEvent{
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "b1"},
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "b2"},
	}
	seedAuditEvents(t, s, "tenant-a", tenantA)
	seedAuditEvents(t, s, "tenant-b", tenantB)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=tenant-a", nil))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	s.handleListAuditEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeAuditEventsResponse(t, rec)
	if len(resp.Items) != 3 {
		t.Fatalf("returned %d items, want 3 (tenant-a only); items=%+v", len(resp.Items), resp.Items)
	}
	for _, it := range resp.Items {
		if it.TenantID != "tenant-a" {
			t.Errorf("item tenant_id=%q, want tenant-a", it.TenantID)
		}
		if strings.HasPrefix(it.Action, "b") {
			t.Errorf("leaked tenant-b event into tenant-a response: %+v", it)
		}
	}
}

// TestHandleAuditEvents_CursorPaginationStable seeds 25 events and walks
// the cursor across pages of 10/10/5. Pin that pages are reverse-chrono
// (highest Seq first) and that the final page's cursor is empty.
func TestHandleAuditEvents_CursorPaginationStable(t *testing.T) {
	s, _, _ := newTestGateway(t)
	events := make([]audit.SIEMEvent, 25)
	for i := range events {
		events[i] = audit.SIEMEvent{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			Action:    "evt-" + itoa(i),
			JobID:     "job-" + itoa(i),
		}
	}
	seedAuditEvents(t, s, "default", events)

	// Page 1
	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=default&limit=10", nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page1 status=%d: %s", rec.Code, rec.Body.String())
	}
	page1 := decodeAuditEventsResponse(t, rec)
	if len(page1.Items) != 10 {
		t.Fatalf("page1 returned %d items, want 10", len(page1.Items))
	}
	if page1.NextCursor == "" {
		t.Fatalf("page1 missing next_cursor, want a non-empty cursor with 15 events remaining")
	}
	// Reverse-chronological: first item's Seq must be the highest (25).
	if page1.Items[0].Seq != 25 {
		t.Errorf("page1 first item Seq=%d, want 25 (reverse-chrono)", page1.Items[0].Seq)
	}
	if page1.Items[len(page1.Items)-1].Seq != 16 {
		t.Errorf("page1 last item Seq=%d, want 16 (reverse-chrono)", page1.Items[len(page1.Items)-1].Seq)
	}

	// Page 2
	rec = httptest.NewRecorder()
	req = adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/events?tenant=default&limit=10&cursor="+page1.NextCursor, nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page2 status=%d: %s", rec.Code, rec.Body.String())
	}
	page2 := decodeAuditEventsResponse(t, rec)
	if len(page2.Items) != 10 {
		t.Fatalf("page2 returned %d items, want 10", len(page2.Items))
	}
	if page2.Items[0].Seq != 15 || page2.Items[len(page2.Items)-1].Seq != 6 {
		t.Errorf("page2 seq range = [%d,%d], want [15,6]",
			page2.Items[0].Seq, page2.Items[len(page2.Items)-1].Seq)
	}

	// Page 3 — last 5
	rec = httptest.NewRecorder()
	req = adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/events?tenant=default&limit=10&cursor="+page2.NextCursor, nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page3 status=%d: %s", rec.Code, rec.Body.String())
	}
	page3 := decodeAuditEventsResponse(t, rec)
	if len(page3.Items) != 5 {
		t.Fatalf("page3 returned %d items, want 5", len(page3.Items))
	}
	if page3.NextCursor != "" {
		t.Errorf("page3 next_cursor=%q, want empty (end of stream)", page3.NextCursor)
	}
	if page3.Items[0].Seq != 5 || page3.Items[len(page3.Items)-1].Seq != 1 {
		t.Errorf("page3 seq range = [%d,%d], want [5,1]",
			page3.Items[0].Seq, page3.Items[len(page3.Items)-1].Seq)
	}

	// Page seq sets must be disjoint and union to {1..25}.
	seen := make(map[int64]bool)
	for _, p := range [][]auditEventResponseItem{page1.Items, page2.Items, page3.Items} {
		for _, it := range p {
			if seen[it.Seq] {
				t.Errorf("seq %d returned twice across pages", it.Seq)
			}
			seen[it.Seq] = true
		}
	}
	if len(seen) != 25 {
		t.Errorf("union of pages = %d seqs, want 25", len(seen))
	}
}

// TestHandleAuditEvents_EventTypeFilter pins ?event_type= narrows the
// returned set. Seed a mix of event types; query a single type; assert
// only matching are returned.
func TestHandleAuditEvents_EventTypeFilter(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedAuditEvents(t, s, "default", []audit.SIEMEvent{
		{EventType: audit.EventMCPToolInvocation, Severity: audit.SeverityInfo, Action: "a"},
		{EventType: audit.EventEdgePolicyDecision, Severity: audit.SeverityInfo, Action: "b"},
		{EventType: audit.EventMCPToolInvocation, Severity: audit.SeverityInfo, Action: "c"},
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "d"},
	})

	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/events?tenant=default&event_type="+audit.EventMCPToolInvocation, nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeAuditEventsResponse(t, rec)
	if len(resp.Items) != 2 {
		t.Fatalf("returned %d items, want 2 (mcp.tool_invocation only); items=%+v",
			len(resp.Items), resp.Items)
	}
	for _, it := range resp.Items {
		if it.EventType != audit.EventMCPToolInvocation {
			t.Errorf("item event_type=%q, want %q", it.EventType, audit.EventMCPToolInvocation)
		}
	}
}

// TestHandleAuditEvents_TimeRangeFilter pins ?from / ?to inclusively
// bound results by timestamp. Seed events spanning a 10ms window; query
// the middle 4ms; assert only the middle events surface.
func TestHandleAuditEvents_TimeRangeFilter(t *testing.T) {
	s, _, _ := newTestGateway(t)
	base := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	ev := make([]audit.SIEMEvent, 10)
	for i := range ev {
		ev[i] = audit.SIEMEvent{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			Action:    "x",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
	}
	seedAuditEvents(t, s, "default", ev)

	from := base.Add(3 * time.Minute).Format(time.RFC3339)
	to := base.Add(6 * time.Minute).Format(time.RFC3339)
	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/events?tenant=default&from="+from+"&to="+to, nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeAuditEventsResponse(t, rec)
	// Events at minute 3, 4, 5, 6 — inclusive.
	if len(resp.Items) != 4 {
		t.Errorf("returned %d items, want 4 (minutes 3..6 inclusive); items=%+v",
			len(resp.Items), resp.Items)
	}
}

// TestHandleAuditEvents_AuditChainerNotInstalled mirrors the verify
// handler's 503 fail-loud guard: when no chainer is configured, the
// endpoint must surface the misconfiguration rather than returning a
// false-green empty list.
func TestHandleAuditEvents_AuditChainerNotInstalled(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auditChainer = nil

	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=default", nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "audit_chainer_not_installed") &&
		!strings.Contains(body, "audit chainer not installed") {
		t.Errorf("body=%q, want audit_chainer_not_installed marker", body)
	}
}

// TestHandleAuditEvents_RedactsSecretsInExtra pins defense-in-depth
// redaction: known-secret keys (token, password, api_key, secret,
// private_key) are stripped from Extra before serialization, even when
// the emit site failed to redact. The wire response MUST NEVER contain
// the secret value anywhere.
func TestHandleAuditEvents_RedactsSecretsInExtra(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedAuditEvents(t, s, "default", []audit.SIEMEvent{
		{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			Action:    "leak-test",
			Extra: map[string]string{
				"token":       "sk-secret-xyz",
				"password":    "p@ssw0rd",
				"api_key":     "ak_live_123",
				"apiKey":      "AK-CAMEL",
				"private_key": "-----BEGIN PRIVATE KEY-----", // no-secret-lint — synthetic PEM marker used to assert redaction
				"SECRET":      "uppercase-secret",
				"safe_field":  "kept",
				"resource_id": "rid-9",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=default", nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeAuditEventsResponse(t, rec)
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(resp.Items))
	}
	extra := resp.Items[0].Extra
	for _, k := range []string{"token", "password", "api_key", "apiKey", "private_key", "SECRET"} {
		if _, present := extra[k]; present {
			t.Errorf("secret key %q leaked into response (value=%q)", k, extra[k])
		}
	}
	if v, ok := extra["safe_field"]; !ok || v != "kept" {
		t.Errorf("safe_field stripped; want kept, got %q", v)
	}
	if v, ok := extra["resource_id"]; !ok || v != "rid-9" {
		t.Errorf("resource_id stripped; want rid-9, got %q", v)
	}
	// Defense-in-depth body-level scan: secret values MUST NOT appear in
	// the wire payload anywhere, regardless of whether the key survived.
	for _, secret := range []string{
		"sk-secret-xyz", "p@ssw0rd", "ak_live_123", "AK-CAMEL",
		"-----BEGIN PRIVATE KEY-----", "uppercase-secret", // no-secret-lint — body-scan also expects the synthetic PEM marker not to leak
	} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("secret value %q leaked into response body", secret)
		}
	}
}

// TestAuditEvents_LimitCappedAtMax pins the hard cap on the audit-events
// allocation path end-to-end: the boundary clamp in parseAuditEventsQuery
// AND the store-level re-clamp in readAuditEventsPage must each refuse to
// honor a caller-supplied limit beyond MaxAuditEventsLimit, so a single
// over-eager (or malicious) request can never DoS the gateway by forcing
// an oversized slice allocation. Resolves CodeQL go/uncontrolled-allocation-size
// alerts #34-36 (GHAS #886/#889/#890) on PR #276.
func TestAuditEvents_LimitCappedAtMax(t *testing.T) {
	s, _, _ := newTestGateway(t)
	events := make([]audit.SIEMEvent, MaxAuditEventsLimit+25)
	for i := range events {
		events[i] = audit.SIEMEvent{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			Action:    "x",
		}
	}
	seedAuditEvents(t, s, "default", events)

	t.Run("http boundary clamps adversarial limit", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := adminCtx(httptest.NewRequest(http.MethodGet,
			"/api/v1/audit/events?tenant=default&limit=99999999", nil))
		s.handleListAuditEvents(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
		}
		resp := decodeAuditEventsResponse(t, rec)
		if len(resp.Items) > MaxAuditEventsLimit {
			t.Fatalf("returned %d items, exceeds MaxAuditEventsLimit=%d — the cap is what bounds the allocation",
				len(resp.Items), MaxAuditEventsLimit)
		}
		if len(resp.Items) != MaxAuditEventsLimit {
			t.Errorf("returned %d items, want exactly MaxAuditEventsLimit=%d (enough seeded events exist)",
				len(resp.Items), MaxAuditEventsLimit)
		}
		// next_cursor must be non-empty because more events remain past the
		// capped page; an empty cursor here would silently hide the rest of
		// the stream from a client that asked for everything.
		if resp.NextCursor == "" {
			t.Error("next_cursor is empty, want non-empty (seeded events exceed the cap)")
		}
		if resp.Returned != len(resp.Items) {
			t.Errorf("returned=%d but items=%d — wire envelope drift", resp.Returned, len(resp.Items))
		}
	})

	t.Run("store path direct call clamps adversarial limit", func(t *testing.T) {
		// Defense-in-depth: even if a future internal caller bypasses the
		// HTTP boundary (e.g. a job runner or sibling handler reuse) and
		// hands readAuditEventsPage an adversarial limit, the function must
		// refuse to over-allocate. 1<<30 is the static-analysis-recognised
		// adversarial MaxInt-class value mirroring TestClampListPageSize.
		streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
		items, nextCursor, err := readAuditEventsPage(
			context.Background(),
			s.redisClient(),
			streamKey,
			"",
			1<<30,
			auditEventsFilters{},
		)
		if err != nil {
			t.Fatalf("readAuditEventsPage(limit=1<<30) errored: %v — must not error on oversized limit, must clamp", err)
		}
		if len(items) > MaxAuditEventsLimit {
			t.Fatalf("items len = %d, want <= MaxAuditEventsLimit=%d", len(items), MaxAuditEventsLimit)
		}
		if len(items) != MaxAuditEventsLimit {
			t.Fatalf("items len = %d, want exactly MaxAuditEventsLimit=%d (enough seeded events exist)",
				len(items), MaxAuditEventsLimit)
		}
		if nextCursor == "" {
			t.Error("next_cursor empty after capped page, want non-empty (seeded events exceed the cap)")
		}
	})

	t.Run("store path zero limit returns no rows without error", func(t *testing.T) {
		// Below-zero/zero must be a no-op rather than a NIL-deref or an
		// OOM. The current implementation early-returns; pin it.
		streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
		items, nextCursor, err := readAuditEventsPage(
			context.Background(),
			s.redisClient(),
			streamKey,
			"",
			0,
			auditEventsFilters{},
		)
		if err != nil {
			t.Fatalf("readAuditEventsPage(limit=0) errored: %v", err)
		}
		if len(items) != 0 || nextCursor != "" {
			t.Fatalf("zero-limit returned items=%d cursor=%q, want empty/empty", len(items), nextCursor)
		}
	})
}

// TestHandleAuditEvents_NoEventsForFreshTenant pins that a tenant with a
// chainer installed but no events ever appended returns 200 with an
// empty page, NOT 404 or 500. The 503 fail-loud applies only when the
// chainer itself is uninstalled.
func TestHandleAuditEvents_NoEventsForFreshTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auditChainer = audit.NewChainer(s.redisClient(), "")

	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=default", nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", rec.Code, rec.Body.String())
	}
	resp := decodeAuditEventsResponse(t, rec)
	if len(resp.Items) != 0 {
		t.Errorf("got %d items, want 0 for fresh tenant", len(resp.Items))
	}
	if resp.NextCursor != "" {
		t.Errorf("next_cursor=%q, want empty for fresh tenant", resp.NextCursor)
	}
}

// TestHandleAuditEvents_MetaAudit pins the audit-of-audit closure: every
// /audit/events call appends one audit.read.events SIEMEvent to the
// tenant's stream. Without this, the read surface is invisible to the
// chain — a compliance hole.
func TestHandleAuditEvents_MetaAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedAuditEvents(t, s, "default", []audit.SIEMEvent{
		{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "seed"},
	})
	// Seed appended exactly 1 event.
	streamKey := audit.NewChainer(s.redisClient(), "").StreamKey("default")
	before, err := s.redisClient().XLen(context.Background(), streamKey).Result()
	if err != nil {
		t.Fatalf("xlen pre-call: %v", err)
	}
	if before != 1 {
		t.Fatalf("expected 1 seeded event, got %d", before)
	}

	rec := httptest.NewRecorder()
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?tenant=default", nil))
	s.handleListAuditEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}

	after, err := s.redisClient().XLen(context.Background(), streamKey).Result()
	if err != nil {
		t.Fatalf("xlen post-call: %v", err)
	}
	if after != before+1 {
		t.Errorf("stream grew by %d after call, want +1 (audit.read.events meta-event)",
			after-before)
	}
}

// TestHandleAuditEvents_HeavyFilterPagesForward pins the cursor-forward
// guarantee under heavy filter pressure: when a single XRevRange scan
// (limit * auditEventsFetchMultiplier * maxBatchRoundtrips entries)
// produces zero matches because every entry is dropped by the filter
// chain, the response must hand back a cursor that lets the client
// resume DEEPER in the stream. Returning an empty cursor here would
// silently lose any matching events older than the scan window.
//
// Regression for the adversarial-self-review finding: the original
// readAuditEventsPage only tracked lastEmittedID, so a heavily-filtered
// page exited with cursor="" and the client incorrectly stopped paging.
func TestHandleAuditEvents_HeavyFilterPagesForward(t *testing.T) {
	s, _, _ := newTestGateway(t)
	const pageLimit = 2
	// Seed enough non-matching entries to force the search to exit on
	// maxBatchRoundtrips without finding any matches in the first page:
	// scanWindow = pageLimit * auditEventsFetchMultiplier * maxBatchRoundtrips = 2 * 4 * 8 = 64.
	// Pad to 80 non-matching so the matches sit beyond the first page's window.
	const nonMatching = pageLimit*auditEventsFetchMultiplier*8 + 16
	events := make([]audit.SIEMEvent, 0, nonMatching+pageLimit)
	// Matching events (older) first — they were appended earlier.
	for i := 0; i < pageLimit; i++ {
		events = append(events, audit.SIEMEvent{
			EventType: audit.EventMCPToolInvocation,
			Severity:  audit.SeverityInfo,
			Action:    "matching-" + itoa(i),
		})
	}
	// Non-matching events newest (appended after matches).
	for i := 0; i < nonMatching; i++ {
		events = append(events, audit.SIEMEvent{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			Action:    "filler-" + itoa(i),
		})
	}
	seedAuditEvents(t, s, "default", events)

	cursor := ""
	seen := 0
	const maxPages = 30
	for page := 0; page < maxPages && seen < pageLimit; page++ {
		url := "/api/v1/audit/events?tenant=default&event_type=" +
			audit.EventMCPToolInvocation + "&limit=" + itoa(pageLimit)
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		rec := httptest.NewRecorder()
		req := adminCtx(httptest.NewRequest(http.MethodGet, url, nil))
		s.handleListAuditEvents(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status=%d body=%s", page, rec.Code, rec.Body.String())
		}
		resp := decodeAuditEventsResponse(t, rec)
		seen += len(resp.Items)
		if resp.NextCursor == "" {
			if seen < pageLimit {
				t.Fatalf("page %d returned empty cursor with %d/%d matches seen "+
					"— heavy-filter pagination dropped events (the bug this test guards)",
					page, seen, pageLimit)
			}
			break
		}
		if resp.NextCursor == cursor {
			t.Fatalf("page %d cursor did not advance (cursor=%q) — would loop forever",
				page, cursor)
		}
		cursor = resp.NextCursor
	}
	if seen != pageLimit {
		t.Errorf("saw %d matches across paginated scan, want %d", seen, pageLimit)
	}
}

// TestHandleAuditEvents_InvalidCursor_Returns400InvalidCursorCode pins the
// PR #276 Sub-H #29 finding: a malformed cursor must surface as a 400
// with a structured `code: "INVALID_CURSOR"` envelope, NOT as a 500 from
// the downstream XRevRangeN call and NOT as a plain-text 400 without the
// stable error code. Asserts the wire shape BEFORE decoding so a
// regression that downgrades the response to 500/200 is caught even if
// the JSON happens to decode into a partially-populated body.
//
// Sub-cases cover the three malformed shapes the parser must reject:
//   - "not-a-stream-id"        — letters in both halves
//   - "abc-def"                — letters around a single dash
//   - "12345"                  — no separator at all
//   - "-456"                   — empty ms half
//   - "456-"                   — empty seq half
//
// Each must produce 400 + code=INVALID_CURSOR. The raw cursor value MUST
// NOT echo into the response body (no log/leak surface for caller bugs).
func TestHandleAuditEvents_InvalidCursor_Returns400InvalidCursorCode(t *testing.T) {
	cases := []struct {
		name   string
		cursor string
	}{
		{"letters_in_both_halves", "not-a-stream-id"},
		{"letters_around_dash", "abc-def"},
		{"no_separator", "12345"},
		{"empty_ms_half", "-456"},
		{"empty_seq_half", "456-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, _, _ := newTestGateway(t)
			s.auditChainer = audit.NewChainer(s.redisClient(), "")

			req := adminCtx(httptest.NewRequest(http.MethodGet,
				"/api/v1/audit/events?tenant=default&limit=10&cursor="+tc.cursor, nil))
			rec := httptest.NewRecorder()
			s.handleListAuditEvents(rec, req)

			// HTTP status asserted BEFORE decoding the body so a future
			// regression that downgrades to 500/200 surfaces here, not
			// silently behind an empty-decode path.
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (invalid cursor); body=%s",
					rec.Code, rec.Body.String())
			}

			var body struct {
				Code   string `json:"code"`
				Error  string `json:"error"`
				Status int    `json:"status"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode 400 body: %v (body=%s)", err, rec.Body.String())
			}
			if body.Code != "INVALID_CURSOR" {
				t.Errorf("code = %q, want %q (stable INVALID_CURSOR contract)",
					body.Code, "INVALID_CURSOR")
			}
			if body.Status != http.StatusBadRequest {
				t.Errorf("status field = %d, want %d", body.Status, http.StatusBadRequest)
			}
			if body.Error == "" {
				t.Errorf("error field empty; want human-readable hint")
			}
			// The raw cursor value MUST NOT echo into the response body —
			// a caller-supplied string in an error reply is a classic
			// log-injection / reflected-XSS shape we refuse to ship.
			if strings.Contains(rec.Body.String(), tc.cursor) {
				t.Errorf("response body echoes raw cursor %q; want sanitized hint", tc.cursor)
			}
			// Defense-in-depth: response must not leak the generic 500
			// "internal error" string from writeInternalError.
			lower := strings.ToLower(rec.Body.String())
			if strings.Contains(lower, "internal error") {
				t.Errorf("body leaks generic 500 internal-error: %s", rec.Body.String())
			}
		})
	}
}

// itoa is a small i→string helper to keep test bodies readable without
// pulling strconv into every line.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
