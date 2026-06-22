package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

// seedComplianceEvents appends n events to the tenant's chain stream
// using audit.Chainer so the resulting stream is indistinguishable from
// production state. Returns the emitted events for post-hoc assertions.
func seedComplianceEvents(t *testing.T, client redis.UniversalClient, tenant string, n int) []audit.SIEMEvent {
	t.Helper()
	chainer := audit.NewChainer(client, "")
	events := make([]audit.SIEMEvent, 0, n)
	types := []string{
		audit.EventSafetyDecision,
		audit.EventSafetyApproval,
		audit.EventPolicyChange,
		audit.EventSystemAuth,
		audit.EventMCPToolApproval,
	}
	decisions := []string{"allow", "deny"}
	for i := 0; i < n; i++ {
		ev := audit.SIEMEvent{
			Timestamp:   time.Now().UTC(),
			EventType:   types[i%len(types)],
			Severity:    audit.SeverityInfo,
			TenantID:    tenant,
			AgentID:     fmt.Sprintf("agent-%d", i),
			Action:      fmt.Sprintf("action-%d", i),
			Decision:    decisions[i%len(decisions)],
			MatchedRule: fmt.Sprintf("rule-%d", i),
			Reason:      fmt.Sprintf("reason %d", i),
		}
		if err := chainer.Append(context.Background(), &ev); err != nil {
			t.Fatalf("seed append[%d]: %v", i, err)
		}
		events = append(events, ev)
	}
	return events
}

func grantExportEntitlement(t *testing.T, s *server) {
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(ent *licensing.Entitlements) {
		if ent.Features == nil {
			ent.Features = map[string]bool{}
		}
		ent.Features["siem_export"] = true
		ent.Features["audit_export"] = true
	})
}

// rangeQS returns from/to query fragments spanning the last hour so the
// export window bracketing every seeded event works even on fast test
// machines that append events in a few milliseconds.
func rangeQS() string {
	now := time.Now().UTC()
	from := now.Add(-1 * time.Hour).Format(time.RFC3339)
	to := now.Add(30 * time.Second).Format(time.RFC3339)
	return fmt.Sprintf("&from=%s&to=%s", from, to)
}

// TestHandleAuditExport_JSONHappyPath asserts the full NDJSON wire
// shape: Content-Type, manifest-first ordering, per-event soc2_controls
// injection, and the trailing footer.
func TestHandleAuditExport_JSONHappyPath(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)
	seedComplianceEvents(t, s.redisClient(), "default", 5)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=json"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", got)
	}
	if got := rec.Header().Get("X-Cordum-Export-Format"); got != "json" {
		t.Errorf("X-Cordum-Export-Format = %q, want json", got)
	}
	if got := rec.Header().Get("X-Cordum-Tenant"); got != "default" {
		t.Errorf("X-Cordum-Tenant = %q, want default", got)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "cordum-audit-default-") || !strings.Contains(cd, ".ndjson") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	scanner := bufio.NewScanner(rec.Body)
	// Raise buffer so a manifest with big legends doesn't hit the
	// default 64k scan limit.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// First line is the manifest.
	if !scanner.Scan() {
		t.Fatalf("empty body")
	}
	var manifest map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	if manifest["type"] != "manifest" {
		t.Errorf("first line type = %v, want manifest", manifest["type"])
	}
	if manifest["tenant_id"] != "default" {
		t.Errorf("tenant = %v", manifest["tenant_id"])
	}
	if _, ok := manifest["soc2_legend"]; !ok {
		t.Errorf("manifest missing soc2_legend")
	}

	// Subsequent lines are events (or the footer).
	eventLines := 0
	sawFooter := false
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("line unmarshal: %v", err)
		}
		switch line["type"] {
		case "event":
			eventLines++
			if _, ok := line["soc2_controls"]; !ok {
				t.Errorf("event line missing soc2_controls: %v", line)
			}
		case "footer":
			sawFooter = true
			if fc, _ := line["event_count"].(float64); int(fc) != 5 {
				t.Errorf("footer event_count = %v, want 5", line["event_count"])
			}
		default:
			t.Errorf("unexpected type: %v", line["type"])
		}
	}
	if eventLines != 5 {
		t.Errorf("expected 5 event lines, got %d", eventLines)
	}
	if !sawFooter {
		t.Errorf("missing trailing footer")
	}
}

// TestHandleAuditExport_CSVHappyPath asserts the CSV flow including
// the `# cordum-manifest: ...` comment line and the column contract.
func TestHandleAuditExport_CSVHappyPath(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)
	seedComplianceEvents(t, s.redisClient(), "default", 3)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=csv"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	// No BOM without excel=true.
	if strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Errorf("unexpected BOM without excel=true")
	}
	// First line must be the manifest comment.
	firstLine, rest, found := strings.Cut(body, "\n")
	if !found || !strings.HasPrefix(firstLine, "# cordum-manifest: ") {
		t.Fatalf("expected manifest comment prefix, got %q", firstLine)
	}
	// Manifest JSON must parse.
	manifestJSON := strings.TrimPrefix(firstLine, "# cordum-manifest: ")
	var manifest map[string]any
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		t.Errorf("manifest JSON malformed: %v", err)
	}
	// Remainder is RFC 4180 CSV.
	rdr := csv.NewReader(strings.NewReader(rest))
	rdr.FieldsPerRecord = -1
	rows, err := rdr.ReadAll()
	if err != nil {
		t.Fatalf("csv.ReadAll: %v", err)
	}
	// task-c8d4b056 appended 11 humanized columns AFTER the frozen 21 legacy
	// columns (additive — legacy positions unchanged, edge.export.v1 unbumped).
	const legacyCols = 21
	humanCols := []string{
		"human_summary", "actor_label", "agent_label", "resource_label",
		"session_id", "execution_id", "resource_id", "input_preview",
		"output_preview", "trace_id", "artifact_id",
	}
	wantCols := legacyCols + len(humanCols)
	if len(rows) < 1 {
		t.Fatalf("missing CSV header row")
	}
	if len(rows[0]) != wantCols {
		t.Fatalf("header has wrong column count: got %d want %d: %v", len(rows[0]), wantCols, rows[0])
	}
	// Legacy columns must stay in their original positions.
	if rows[0][0] != "timestamp" || rows[0][19] != "soc2_controls" || rows[0][20] != "extra_json" {
		t.Errorf("legacy header columns unexpected: %v", rows[0])
	}
	// Humanized columns must be appended after the legacy set, in order.
	for i, h := range humanCols {
		if rows[0][legacyCols+i] != h {
			t.Errorf("appended column %d = %q, want %q", i, rows[0][legacyCols+i], h)
		}
	}
	if len(rows) != 1+3 {
		t.Errorf("expected 3 data rows, got %d", len(rows)-1)
	}
}

func TestComplianceExport_DoesNotLeakInternalErrors(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			grantExportEntitlement(t, s)
			client := s.redisClient()
			if client == nil {
				t.Fatal("redis client is nil")
			}
			if err := client.Close(); err != nil {
				t.Fatalf("close redis client: %v", err)
			}

			req := adminCtx(httptest.NewRequest(http.MethodGet,
				"/api/v1/audit/export?format="+format+rangeQS(), nil))
			rec := httptest.NewRecorder()
			s.handleAuditExport(rec, req)

			assertComplianceExportInternalErrorRedacted(t, rec.Body.String())
		})
	}
}

func assertComplianceExportInternalErrorRedacted(t *testing.T, body string) {
	t.Helper()
	forbidden := []string{
		"10.0.0.5",
		"6379",
		"dial tcp",
		"audit:chain:",
		"redis:",
		"client is closed",
		"/var/lib/redis/dump.rdb",
		`C:\Redis\dump.rdb`,
	}
	for _, token := range forbidden {
		if strings.Contains(body, token) {
			t.Fatalf("response body leaked %q:\n%s", token, body)
		}
	}
	if !strings.Contains(body, "export failed") {
		t.Fatalf("response body missing generic export failure marker:\n%s", body)
	}
}

// TestHandleAuditExport_ExcelModeAddsBOM verifies the Excel toggle.
func TestHandleAuditExport_ExcelModeAddsBOM(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)
	seedComplianceEvents(t, s.redisClient(), "default", 1)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=csv&excel=true"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) {
		t.Errorf("expected UTF-8 BOM with excel=true")
	}
}

// TestHandleAuditExport_CSVInjectionNeutralised seeds an event whose
// Reason starts with a formula trigger and asserts the exported cell
// is prefixed with an apostrophe.
func TestHandleAuditExport_CSVInjectionNeutralised(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)

	// Seed a single hand-crafted event via the chainer so the Reason
	// field carries the dangerous `=cmd|...` prefix.
	chainer := audit.NewChainer(s.redisClient(), "")
	ev := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSafetyDecision,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "malicious",
		Decision:  "deny",
		Reason:    `=cmd|'/c calc'!A1`,
	}
	if err := chainer.Append(context.Background(), &ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=csv"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"'=cmd`) && !strings.Contains(body, `'=cmd`) {
		t.Errorf("reason cell not neutralised:\n%s", body)
	}
}

// TestHandleAuditExport_RejectsBadRange covers from>=to and excessive
// spread.
func TestHandleAuditExport_RejectsBadRange(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)

	now := time.Now().UTC()

	// from == to
	eq := now.Format(time.RFC3339)
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/export?format=json&from="+eq+"&to="+eq, nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("from==to: status = %d, want 400", rec.Code)
	}

	// Spread >366 days.
	req = adminCtx(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/audit/export?format=json&from=%s&to=%s",
			now.Add(-400*24*time.Hour).Format(time.RFC3339),
			now.Format(time.RFC3339)), nil))
	rec = httptest.NewRecorder()
	s.handleAuditExport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("spread>366d: status = %d, want 400", rec.Code)
	}
}

// TestHandleAuditExport_RejectsWithoutEntitlement pins the 403 + tier
// limit body shape.
func TestHandleAuditExport_RejectsWithoutEntitlement(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Explicit community plan — no siem_export / audit_export.
	setTestEntitlements(t, s, licensing.PlanCommunity, func(e *licensing.Entitlements) {
		if e.Features == nil {
			e.Features = map[string]bool{}
		}
		e.Features["siem_export"] = false
		e.Features["audit_export"] = false
	})

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=json"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "tier_limit_exceeded" {
		t.Errorf("code = %v, want tier_limit_exceeded", body["code"])
	}
}

// TestHandleAuditExport_LimitAndTruncation: set --limit=1, seed 3 events,
// the manifest's footer reports truncated_at_max=true.
func TestHandleAuditExport_LimitAndTruncation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)
	seedComplianceEvents(t, s.redisClient(), "default", 3)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=json&limit=1"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	scanner := bufio.NewScanner(rec.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var truncated bool
	var eventCount int
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if line["type"] == "footer" {
			eventCount = int(line["event_count"].(float64))
			truncated, _ = line["truncated"].(bool)
		}
	}
	if eventCount != 1 {
		t.Errorf("event_count = %d, want 1", eventCount)
	}
	if !truncated {
		t.Errorf("footer.truncated should be true when limit reached")
	}
}

// TestSanitiseFilenameSegment_BlocksPathTraversal guards the
// Content-Disposition header against tenant IDs carrying slashes or
// other separators.
func TestSanitiseFilenameSegment_BlocksPathTraversal(t *testing.T) {
	cases := map[string]string{
		"default": "default",
		// Path traversal: both slashes replaced; dot allowed (legitimate
		// in subdomain-style tenant IDs) but cannot reconstruct a ".."
		// traversal because the surrounding slashes are gone.
		"../etc":      ".._etc",
		"tenant-1":    "tenant-1",
		"a/b\\c":      "a_b_c",
		"":            "unknown",
		"weird space": "weird_space",
	}
	for in, want := range cases {
		if got := sanitiseFilenameSegment(in); got != want {
			t.Errorf("sanitiseFilenameSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseComplianceExportQuery_FormatValidation covers the small
// type-coercion branches inline — they're easier to pin here than
// through the full handler harness.
func TestParseComplianceExportQuery_FormatValidation(t *testing.T) {
	for _, fmtVal := range []string{"exotic", "xml"} {
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/v1/audit/export?format=%s&from=2026-04-17T00:00:00Z&to=2026-04-18T00:00:00Z", fmtVal), nil)
		_, err := parseComplianceExportQuery(req)
		if err == nil {
			t.Errorf("expected error for format=%q", fmtVal)
		}
	}
}

// TestParseComplianceExportQuery_FilterParams covers the new
// event_type/severity/category parsing: governance/routine accepted
// (case-insensitive, normalised lower), unknown category + unknown severity
// rejected with 400, event_type accepted loosely.
func TestParseComplianceExportQuery_FilterParams(t *testing.T) {
	base := "/api/v1/audit/export?format=json&from=2026-04-17T00:00:00Z&to=2026-04-18T00:00:00Z"

	t.Run("category governance", func(t *testing.T) {
		opts, err := parseComplianceExportQuery(httptest.NewRequest(http.MethodGet, base+"&category=governance", nil))
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if opts.Category != "governance" {
			t.Errorf("Category = %q, want governance", opts.Category)
		}
	})
	t.Run("category mixed-case normalised", func(t *testing.T) {
		opts, err := parseComplianceExportQuery(httptest.NewRequest(http.MethodGet, base+"&category=Routine", nil))
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if opts.Category != "routine" {
			t.Errorf("Category = %q, want routine", opts.Category)
		}
	})
	t.Run("category unknown rejected", func(t *testing.T) {
		_, err := parseComplianceExportQuery(httptest.NewRequest(http.MethodGet, base+"&category=bogus", nil))
		if err == nil || err.status != http.StatusBadRequest {
			t.Fatalf("category=bogus: err = %+v, want 400", err)
		}
	})
	t.Run("severity case-insensitive accepted", func(t *testing.T) {
		opts, err := parseComplianceExportQuery(httptest.NewRequest(http.MethodGet, base+"&severity=high", nil))
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if opts.Severity != "high" {
			t.Errorf("Severity = %q, want high", opts.Severity)
		}
	})
	t.Run("severity unknown rejected", func(t *testing.T) {
		_, err := parseComplianceExportQuery(httptest.NewRequest(http.MethodGet, base+"&severity=spicy", nil))
		if err == nil || err.status != http.StatusBadRequest {
			t.Fatalf("severity=spicy: err = %+v, want 400", err)
		}
	})
	t.Run("event_type accepted loosely", func(t *testing.T) {
		opts, err := parseComplianceExportQuery(httptest.NewRequest(http.MethodGet, base+"&event_type=safety.decision", nil))
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if opts.EventType != "safety.decision" {
			t.Errorf("EventType = %q, want safety.decision", opts.EventType)
		}
	})
}

// TestHandleAuditExport_CategoryGovernanceFilter is the handler-level E2E:
// category=governance emits ONLY governance rows and echoes the applied filter
// in the response header + manifest.
func TestHandleAuditExport_CategoryGovernanceFilter(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)
	// 5 seeded types cycle safety.decision, safety.approval, safety.policy_change,
	// system.auth (ROUTINE), mcp.tool_approval -> 4 governance, 1 routine.
	seedComplianceEvents(t, s.redisClient(), "default", 5)

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/export?format=json&category=governance"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Cordum-Export-Filter-Category"); got != "governance" {
		t.Errorf("filter-category header = %q, want governance", got)
	}

	scanner := bufio.NewScanner(rec.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	events := 0
	var manifestRowFilterApplied bool
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		switch line["type"] {
		case "manifest":
			manifestRowFilterApplied, _ = line["row_filter_applied"].(bool)
		case "event":
			events++
			et, _ := line["event_type"].(string)
			if audit.CategoryFor(et) != audit.CategoryGovernance {
				t.Errorf("emitted non-governance event_type %q", et)
			}
		}
	}
	if events != 4 {
		t.Errorf("emitted %d events, want 4 governance", events)
	}
	if !manifestRowFilterApplied {
		t.Error("manifest row_filter_applied = false, want true")
	}
}

// TestHandleAuditExport_CategoryGovernanceFilterCSV is the literal DoD-6 E2E:
// GET /api/v1/audit/export?category=governance&format=csv returns ONLY
// governance rows, the manifest comment records row_filter, and
// chain_verification still attests the full range (not the filtered subset).
func TestHandleAuditExport_CategoryGovernanceFilterCSV(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)
	// 5 seeded types: safety.decision, safety.approval, safety.policy_change,
	// system.auth (ROUTINE), mcp.tool_approval -> 4 governance, 1 routine.
	seedComplianceEvents(t, s.redisClient(), "default", 5)

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/export?format=csv&category=governance"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Cordum-Export-Filter-Category"); got != "governance" {
		t.Errorf("filter-category header = %q, want governance", got)
	}

	firstLine, rest, found := strings.Cut(rec.Body.String(), "\n")
	if !found || !strings.HasPrefix(firstLine, "# cordum-manifest: ") {
		t.Fatalf("missing manifest comment: %q", firstLine)
	}
	var manifest struct {
		RowFilterApplied bool `json:"row_filter_applied"`
		RowFilter        *struct {
			Category string `json:"category"`
		} `json:"row_filter"`
		ChainVerification *struct {
			TotalEvents int    `json:"total_events"`
			Status      string `json:"status"`
		} `json:"chain_verification"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(firstLine, "# cordum-manifest: ")), &manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if !manifest.RowFilterApplied || manifest.RowFilter == nil || manifest.RowFilter.Category != "governance" {
		t.Errorf("manifest row_filter not recorded: applied=%v filter=%+v", manifest.RowFilterApplied, manifest.RowFilter)
	}
	// chain_verification must reflect the FULL range (all 5 seeded), not the
	// filtered 4 — proving the filter never reached the verifier. (The CSV
	// comment manifest is written BEFORE the walk, so its event_count is
	// structurally 0 there; the authoritative post-filter count is the CSV
	// data-row count asserted below.)
	if manifest.ChainVerification == nil || manifest.ChainVerification.TotalEvents != 5 {
		t.Errorf("chain_verification not full-range: %+v (want total_events=5)", manifest.ChainVerification)
	}

	// Every CSV data row must be a governance event (event_type column index 1),
	// and there must be exactly 4 — the post-filter count for this seed.
	rdr := csv.NewReader(strings.NewReader(rest))
	rdr.FieldsPerRecord = -1
	rows, err := rdr.ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(rows) != 1+4 {
		t.Fatalf("got %d CSV rows (incl header), want 5 (header + 4 governance)", len(rows))
	}
	for _, row := range rows[1:] {
		if audit.CategoryFor(row[1]) != audit.CategoryGovernance {
			t.Errorf("CSV row emitted non-governance event_type %q", row[1])
		}
	}
}

// TestHandleAuditExport_RejectsUnknownCategory pins the 400 on a bad category.
func TestHandleAuditExport_RejectsUnknownCategory(t *testing.T) {
	s, _, _ := newTestGateway(t)
	grantExportEntitlement(t, s)

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/export?format=json&category=bogus"+rangeQS(), nil))
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
