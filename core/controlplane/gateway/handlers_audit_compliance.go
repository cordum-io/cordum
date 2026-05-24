package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

// complianceExportMaxRangeSpread bounds how much wall-clock time a
// single export request can cover. A year is more than enough for
// quarterly SOC2 reviews; operators who need a larger span paginate
// by calling the endpoint repeatedly with non-overlapping from/to.
const complianceExportMaxRangeSpread = 366 * 24 * time.Hour

// defaultComplianceExportClockSkew lets the caller specify `to` a few
// seconds in the future without the handler rejecting the range.
// Clients with slightly skewed clocks shouldn't fail the request.
const defaultComplianceExportClockSkew = 2 * time.Minute

const complianceExportGenericError = "export failed"

// complianceExportEntitledMaxEvents returns the upper bound on events
// per export call for the caller's current entitlement. Enterprise
// licences get the audit.DefaultComplianceExportMaxEvents ceiling;
// teams get a 10k cap to keep community usage bounded.
func complianceExportEntitledMaxEvents(entitlements licensing.Entitlements) int {
	if entitlements.FeatureEnabled("siem_export") {
		return audit.DefaultComplianceExportMaxEvents
	}
	return 10_000
}

// handleAuditExport implements GET /api/v1/audit/export.
//
// Query parameters:
//
//	format (optional, default json)  — json | csv
//	from   (required)                — RFC 3339 lower bound (inclusive)
//	to     (required)                — RFC 3339 upper bound (inclusive)
//	excel  (optional, default false) — emit a UTF-8 BOM in CSV mode
//
// Response:
//
//	Content-Type: application/x-ndjson  (json)
//	           or text/csv; charset=utf-8 (csv)
//	Content-Disposition: attachment; filename=cordum-audit-<tenant>-<fromYYYYMMDD>-<toYYYYMMDD>.<ext>
//	X-Cordum-Export-Format: json|csv
//	X-Cordum-Tenant:       <tenant>
//
// Tenant is always resolved from the caller's auth context, never from
// a query param. Admin role is required. Entitlement gates the feature
// the same way /api/v1/audit/export/health does; non-entitled callers
// receive the licensing.TierLimitHTTPError payload for consistency.
func (s *server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermAuditExport, []string{"admin"}, s.configSvc) {
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	entitlements := s.currentEntitlements()
	if !entitlements.FeatureEnabled("siem_export") && !entitlements.FeatureEnabled("audit_export") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(licensing.TierLimitHTTPError{
			Code:       "tier_limit_exceeded",
			Message:    "compliance audit export requires an Enterprise license",
			Limit:      "siem_export",
			UpgradeURL: licensing.DefaultUpgradeURL,
		})
		observeAuditExport("unknown", "forbidden", 0)
		return
	}

	opts, httpErr := parseComplianceExportQuery(r)
	if httpErr != nil {
		writeErrorJSON(w, httpErr.status, httpErr.message)
		observeAuditExport(string(opts.Format), "bad_request", 0)
		return
	}
	opts.TenantID = tenant

	// Retention-aware lower bound: reject windows whose lower edge is
	// older than the operator-configured retention plus skew. Prevents
	// a request for a year-old audit window on a tenant with 30-day
	// retention from silently returning empty.
	retention := auditChainRetention()
	oldestAllowed := time.Now().Add(-(retention + defaultComplianceExportClockSkew))
	if opts.From.Before(oldestAllowed) {
		writeErrorJSON(w, http.StatusBadRequest,
			fmt.Sprintf("from %s is older than retention window %s", opts.From.Format(time.RFC3339), retention))
		observeAuditExport(string(opts.Format), "bad_request", 0)
		return
	}

	// Tighten MaxEvents to the entitlement ceiling.
	entitledCap := complianceExportEntitledMaxEvents(entitlements)
	if opts.MaxEvents == 0 || opts.MaxEvents > entitledCap {
		opts.MaxEvents = entitledCap
	}

	// Wire the chain + bundle plumbing from the live server state.
	client := s.redisClient()
	if client == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "audit stream unavailable")
		observeAuditExport(string(opts.Format), "unavailable", 0)
		return
	}
	opts.StreamKey = audit.NewChainer(client, "").StreamKey(tenant)
	opts.BundleLookup = func(ctx context.Context, _ string, from, to time.Time) ([]audit.SignedBundleSnapshot, error) {
		return s.listSignedBundleSnapshots(ctx, from, to)
	}
	opts.SOC2Mapping = audit.LoadSOC2MappingFromEnv()
	opts.SOC2Legend = audit.DefaultSOC2Legend()

	// Headers BEFORE the body so a streaming client sees them even if
	// the first byte takes a moment. Filename uses the YYYYMMDD span
	// so operators glance-verify the time range from the download.
	ext := "ndjson"
	if opts.Format == audit.ComplianceExportFormatCSV {
		ext = "csv"
	}
	filename := fmt.Sprintf("cordum-audit-%s-%s-%s.%s",
		sanitiseFilenameSegment(tenant),
		opts.From.UTC().Format("20060102"),
		opts.To.UTC().Format("20060102"),
		ext,
	)
	switch opts.Format {
	case audit.ComplianceExportFormatCSV:
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/x-ndjson")
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filename))
	w.Header().Set("X-Cordum-Export-Format", string(opts.Format))
	w.Header().Set("X-Cordum-Tenant", tenant)
	// Echo the applied row filter so a client/proxy can confirm the artifact
	// is scoped without parsing the manifest. category/severity come from
	// fixed allow-lists; event_type is sanitised against header injection.
	if opts.Category != "" {
		w.Header().Set("X-Cordum-Export-Filter-Category", opts.Category)
	}
	if opts.Severity != "" {
		w.Header().Set("X-Cordum-Export-Filter-Severity", opts.Severity)
	}
	if opts.EventType != "" {
		w.Header().Set("X-Cordum-Export-Filter-Event-Type", headerSafe(opts.EventType))
	}
	// Flushing is important for client progress bars; the handler is
	// explicitly one-shot and streams rather than buffers.
	flusher, _ := w.(http.Flusher)

	manifest, werr := audit.WriteComplianceExport(r.Context(), &exportFlushWriter{w: w, f: flusher}, client, opts)
	if werr != nil {
		// Headers already written; best we can do is stream an error
		// footer the downstream parser can recognise.
		if opts.Format == audit.ComplianceExportFormatJSON {
			_ = writeJSONLineErr(w)
		} else {
			_, _ = fmt.Fprintln(w, "# cordum-error: "+complianceExportGenericError)
		}
		slog.Error("compliance export failed",
			"tenant", tenant,
			"format", opts.Format,
			"events", manifestEventCount(manifest),
			"error", werr,
		)
		observeAuditExport(string(opts.Format), "error", manifestEventCount(manifest))
		return
	}
	slog.Info("compliance export",
		"tenant", tenant,
		"format", opts.Format,
		"events", manifestEventCount(manifest),
		"filter_category", opts.Category,
		"filter_event_type", opts.EventType,
		"filter_severity", opts.Severity,
		"row_filter_applied", manifestRowFilterApplied(manifest),
	)
	observeAuditExport(string(opts.Format), "ok", manifestEventCount(manifest))
}

// manifestEventCount protects against nil manifest on error paths.
func manifestEventCount(m *audit.ExportManifest) int {
	if m == nil {
		return 0
	}
	return m.EventCount
}

// manifestRowFilterApplied is nil-safe for the slog success line.
func manifestRowFilterApplied(m *audit.ExportManifest) bool {
	return m != nil && m.RowFilterApplied
}

// parseComplianceExportQuery parses + validates the ?from ?to ?format
// ?excel query params. Returns a fully populated options struct (less
// TenantID/StreamKey/BundleLookup, which the handler wires after
// tenant resolution) or an HTTP error with the exact status and
// human-readable message.
func parseComplianceExportQuery(r *http.Request) (audit.ComplianceExportOptions, *verifyHTTPError) {
	q := r.URL.Query()
	opts := audit.ComplianceExportOptions{}

	format := strings.ToLower(strings.TrimSpace(q.Get("format")))
	switch format {
	case "", "json":
		opts.Format = audit.ComplianceExportFormatJSON
	case "csv":
		opts.Format = audit.ComplianceExportFormatCSV
	default:
		return opts, &verifyHTTPError{http.StatusBadRequest, "format must be json or csv"}
	}

	fromRaw := strings.TrimSpace(q.Get("from"))
	toRaw := strings.TrimSpace(q.Get("to"))
	if fromRaw == "" || toRaw == "" {
		return opts, &verifyHTTPError{http.StatusBadRequest, "from and to are required (RFC 3339)"}
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		return opts, &verifyHTTPError{http.StatusBadRequest, "from must be RFC 3339"}
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		return opts, &verifyHTTPError{http.StatusBadRequest, "to must be RFC 3339"}
	}
	if !from.Before(to) {
		return opts, &verifyHTTPError{http.StatusBadRequest, "from must be strictly before to"}
	}
	if to.Sub(from) > complianceExportMaxRangeSpread {
		return opts, &verifyHTTPError{http.StatusBadRequest,
			fmt.Sprintf("range exceeds maximum (%s)", complianceExportMaxRangeSpread)}
	}
	if to.After(time.Now().Add(defaultComplianceExportClockSkew)) {
		return opts, &verifyHTTPError{http.StatusBadRequest, "to must be at or before now"}
	}
	opts.From = from.UTC()
	opts.To = to.UTC()

	if v := strings.TrimSpace(q.Get("excel")); v != "" {
		b, perr := strconv.ParseBool(v)
		if perr != nil {
			return opts, &verifyHTTPError{http.StatusBadRequest, "excel must be a bool"}
		}
		opts.Excel = b
	}

	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n <= 0 {
			return opts, &verifyHTTPError{http.StatusBadRequest, "limit must be a positive integer"}
		}
		opts.MaxEvents = n
	}

	// Row filters (event_type / severity / category). These constrain the
	// EMITTED ROWS only; the chain verifier always runs over the full range
	// (enforced in core/audit/export_compliance.go). event_type is matched
	// case-insensitively and accepted loosely (any non-empty string filters by
	// exact eq-fold); severity must be a known level; category must resolve to
	// the governance/routine taxonomy.
	if v := strings.TrimSpace(q.Get("event_type")); v != "" {
		opts.EventType = v
	}
	if v := strings.TrimSpace(q.Get("severity")); v != "" {
		if !isKnownSeverity(v) {
			return opts, &verifyHTTPError{http.StatusBadRequest, "invalid severity"}
		}
		opts.Severity = v
	}
	if v := strings.TrimSpace(q.Get("category")); v != "" {
		cat := strings.ToLower(v)
		if cat != audit.CategoryGovernance && cat != audit.CategoryRoutine {
			return opts, &verifyHTTPError{http.StatusBadRequest, "invalid category"}
		}
		opts.Category = cat
	}
	return opts, nil
}

// isKnownSeverity reports whether s (case-insensitive) is one of the audit
// severity levels. Used to 400 a malformed ?severity= rather than silently
// returning an empty export.
func isKnownSeverity(s string) bool {
	switch strings.ToUpper(s) {
	case audit.SeverityCritical, audit.SeverityHigh, audit.SeverityMedium, audit.SeverityLow, audit.SeverityInfo:
		return true
	default:
		return false
	}
}

// headerSafe strips CR/LF and other control characters from a caller-supplied
// value before it is echoed into a response header, so a crafted ?event_type=
// can never inject additional headers (defense-in-depth atop net/http's own
// header-value guard).
func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// sanitiseFilenameSegment strips characters that would break the
// Content-Disposition header or the filesystem a downloader writes to.
// Over-sanitises in favour of safety — a weird-looking tenant id in
// the filename is acceptable; a path-traversal leak is not.
func sanitiseFilenameSegment(in string) string {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-' || c == '_' || c == '.':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

// writeJSONLineErr emits a footer-shaped error line so pipelines can
// distinguish "ok export with 0 events" from "export aborted". Not
// a full manifest because by the time we're here the stream has
// already started and we just need to signal the trailing state.
func writeJSONLineErr(w http.ResponseWriter) error {
	payload := map[string]any{
		"type":  "error",
		"error": complianceExportGenericError,
		"at":    time.Now().UTC(),
	}
	b, mErr := json.Marshal(payload)
	if mErr != nil {
		return mErr
	}
	b = append(b, '\n')
	_, err := w.Write(b)
	return err
}

// exportFlushWriter wraps an http.ResponseWriter so every Write immediately
// flushes to the client. Without this, the underlying buffered writer
// may hold multiple chunks before the browser sees them — bad for
// download-progress UX on multi-GB exports.
type exportFlushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw *exportFlushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err != nil {
		return n, err
	}
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, nil
}

// observeAuditExport is the Prometheus hook. Real counter + histogram
// registration lives in metrics.go (see step 10); this placeholder
// keeps the handler code self-contained and compilable until the
// metrics file is written. Replaced in-place when step 10 lands.
var observeAuditExport = func(format, status string, eventCount int) {
	// no-op fallback. Real implementation is bound in initAuditExportMetrics.
	_ = format
	_ = status
	_ = eventCount
}
