// EDGE-151-DASHBOARD — Binary integrity audit ingest + list handlers.
//
// Persists install-time binary-verify outcomes through the existing
// audit-bus (audit.Chainer.Append) so dashboards and SIEM exporters can
// pivot on per-endpoint integrity events without a parallel store.
//
// Ingest (POST /api/v1/edge/binary-integrity/events): operators upload
// captured install-script stderr (JSON-line per outcome). The handler
// re-validates every event against the install.sh emit_audit contract
// (model.BinaryVerifyEvent.Validate), converts to audit.SIEMEvent, and
// emits via the standard audit chain.
//
// List (GET /api/v1/edge/binary-integrity/events): scans the tenant
// audit stream, keeps only binary-verify-{ok,fail} entries, applies the
// sig_scheme / event / endpoint filters, and returns a bounded page in
// the same envelope shape as /api/v1/audit/events.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// MaxBinaryVerifyEventsPerRequest caps a single POST body. The install
// script emits one event per binary verified, so a realistic operator
// batch is ~10-100 events. 1000 leaves headroom for "uploading a full
// fleet's daily integrity log" without enabling unbounded uploads —
// CodeQL-friendly because the make() capacity is a compile-time constant
// (see MaxBinaryVerifyListLimit comment for the same pattern on read).
const MaxBinaryVerifyEventsPerRequest = 1000

// MaxBinaryVerifyRequestBodyBytes caps the POST body byte size before
// JSON decoding. 1000 events * ~512 bytes/event = 512 KB; allow 2 MB
// for indentation + comments + future field growth without enabling
// memory-exhaustion uploads.
const MaxBinaryVerifyRequestBodyBytes = int64(2 * 1024 * 1024)

// MaxBinaryVerifyListLimit caps GET ?limit=. Mirrors MaxAuditEventsLimit
// from handlers_audit_events.go; the make() allocation uses this constant
// directly so CodeQL's bounded-alloc tracker can prove the cap.
const MaxBinaryVerifyListLimit = 200

// defaultBinaryVerifyListLimit is the page size when ?limit is omitted.
const defaultBinaryVerifyListLimit = 100

// binaryVerifyAuditAction is the canonical Action value on the persisted
// SIEMEvent. Dashboards filter on EventType (binary-verify-ok / -fail)
// rather than Action; Action is the human-readable label.
const binaryVerifyAuditAction = "binary-verify"

// extra-map keys used to round-trip the structured fields through
// SIEMEvent.Extra. The 7 install-script fields collapse into:
//   - EventType ← Event
//   - Reason    ← Reason
//   - Extra[hash|path|sig_scheme|fingerprint|exit_code] ← the rest
//
// Pin the literal keys here so the read pipeline can recover the
// original BinaryVerifyEvent shape without a parallel store.
const (
	binaryVerifyExtraHash        = "binary_verify_hash"
	binaryVerifyExtraPath        = "binary_verify_path"
	binaryVerifyExtraSigScheme   = "binary_verify_sig_scheme"
	binaryVerifyExtraFingerprint = "binary_verify_fingerprint"
	binaryVerifyExtraExitCode    = "binary_verify_exit_code"
	binaryVerifyExtraEndpoint    = "binary_verify_endpoint"
)

// binaryVerifyIngestRequest wraps the array. Wrapping in an envelope
// lets us add request-level fields later (endpoint, batch_id, ...)
// without breaking wire shape; an empty `endpoint` is fine — operators
// often run installs from individual hosts and tag with a request header
// or the auth principal's host context.
type binaryVerifyIngestRequest struct {
	Endpoint string                    `json:"endpoint,omitempty"`
	Events   []model.BinaryVerifyEvent `json:"events"`
}

// binaryVerifyIngestResponse echoes back the accepted count plus any
// per-event validation errors so operator tooling can surface partial
// failures without re-uploading the whole batch.
type binaryVerifyIngestResponse struct {
	Accepted int                          `json:"accepted"`
	Rejected int                          `json:"rejected"`
	Errors   []binaryVerifyIngestRejected `json:"errors,omitempty"`
}

type binaryVerifyIngestRejected struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// binaryVerifyListResponse mirrors auditEventsResponse so the dashboard
// can reuse its render logic. Items carry the original BinaryVerifyEvent
// shape plus a server-side timestamp + endpoint label.
type binaryVerifyListResponse struct {
	Items      []binaryVerifyListItem `json:"items"`
	NextCursor string                 `json:"next_cursor"`
	Returned   int                    `json:"returned"`
}

type binaryVerifyListItem struct {
	Timestamp time.Time `json:"timestamp"`
	TenantID  string    `json:"tenant_id"`
	Endpoint  string    `json:"endpoint,omitempty"`
	model.BinaryVerifyEvent
}

// handleIngestBinaryVerify accepts a JSON envelope of structured install
// outcomes and persists each via audit.Chainer.Append. Per-event
// validation errors are reported in the response body; a single hard
// failure (decode error, audit unavailable) returns 4xx/5xx with no
// partial persist.
func (s *server) handleIngestBinaryVerify(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if client == nil {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "redis store unavailable", nil)
		return
	}
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin") {
		return
	}
	tenant, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	if s.auditChainer == nil {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable,
			"binary-verify events cannot be persisted", nil)
		return
	}

	body := http.MaxBytesReader(w, r.Body, MaxBinaryVerifyRequestBodyBytes)
	defer func() { _ = body.Close() }()
	raw, readErr := io.ReadAll(body)
	if readErr != nil {
		if isBinaryVerifyRequestTooLarge(readErr) {
			writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge,
				fmt.Sprintf("body exceeds %d bytes", MaxBinaryVerifyRequestBodyBytes), nil)
			return
		}
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "could not read request body", nil)
		return
	}

	var req binaryVerifyIngestRequest
	if jsonErr := json.Unmarshal(raw, &req); jsonErr != nil {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidJSON, "invalid JSON body", nil)
		return
	}
	if len(req.Events) == 0 {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "events array must contain at least one event", nil)
		return
	}
	if len(req.Events) > MaxBinaryVerifyEventsPerRequest {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest,
			fmt.Sprintf("events array exceeds cap of %d", MaxBinaryVerifyEventsPerRequest), nil)
		return
	}

	endpoint := sanitiseBinaryVerifyEndpoint(req.Endpoint)

	rejected := make([]binaryVerifyIngestRejected, 0, MaxBinaryVerifyEventsPerRequest)
	now := time.Now().UTC()

	for i, ev := range req.Events {
		if validationErr := ev.Validate(); validationErr != nil {
			rejected = append(rejected, binaryVerifyIngestRejected{
				Index: i,
				Error: validationErr.Error(),
			})
			continue
		}
		siem := binaryVerifyToSIEMEvent(ev, tenant, endpoint, now)
		if appendErr := s.auditChainer.Append(r.Context(), &siem); appendErr != nil {
			rejected = append(rejected, binaryVerifyIngestRejected{
				Index: i,
				Error: "audit append failed",
			})
			slog.Error("binary-verify ingest: audit append failed",
				"tenant", tenant, "index", i, "err", appendErr)
		}
	}

	accepted := len(req.Events) - len(rejected)
	slog.Info("binary-verify ingest",
		"tenant", tenant,
		"endpoint", endpoint,
		"submitted", len(req.Events),
		"accepted", accepted,
		"rejected", len(rejected),
	)

	status := http.StatusAccepted
	if accepted == 0 {
		details := map[string]any{
			"accepted": accepted,
			"rejected": len(rejected),
		}
		if len(rejected) > 0 {
			details["errors"] = rejected
		}
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest,
			"all binary-verify events rejected", details)
		return
	}
	resp := binaryVerifyIngestResponse{Accepted: accepted, Rejected: len(rejected)}
	if len(rejected) > 0 {
		resp.Errors = rejected
	}
	writeJSONWithStatus(w, status, resp)
}

// handleListBinaryVerify queries the tenant audit stream for
// binary-verify-{ok,fail} events with optional filters, returns a
// bounded page in the same envelope shape as /api/v1/audit/events.
func (s *server) handleListBinaryVerify(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if client == nil {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "redis store unavailable", nil)
		return
	}
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}
	tenant, ok := s.edgeTenantFromRequest(w, r, strings.TrimSpace(r.URL.Query().Get("tenant")))
	if !ok {
		return
	}
	if s.auditChainer == nil {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable,
			"binary-verify events cannot be served", nil)
		return
	}

	limit, cursor, filters, parseErr := parseBinaryVerifyListQuery(r)
	if parseErr != nil {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, parseErr.Error(), nil)
		return
	}

	streamKey := s.auditChainer.StreamKey(tenant)
	items, nextCursor, fetchErr := readBinaryVerifyEventsPage(
		r.Context(), client, streamKey, cursor, limit, filters,
	)
	if fetchErr != nil {
		writeEdgeInternalError(w, r, "binary-verify list: read stream", fetchErr)
		return
	}

	slog.Info("binary-verify events listed",
		"tenant", tenant,
		"limit", limit,
		"returned", len(items),
		"next_cursor_present", nextCursor != "",
		"filter_event", filters.event,
		"filter_sig_scheme", filters.sigScheme,
		"filter_endpoint", filters.endpoint,
	)

	writeJSON(w, binaryVerifyListResponse{
		Items:      items,
		NextCursor: nextCursor,
		Returned:   len(items),
	})
}

type binaryVerifyListFilters struct {
	event     string // "binary-verify-ok" | "binary-verify-fail" | ""
	sigScheme string // gpg|codesign|authenticode|dev|""
	endpoint  string
}

func parseBinaryVerifyListQuery(r *http.Request) (int, string, binaryVerifyListFilters, error) {
	q := r.URL.Query()

	limit := defaultBinaryVerifyListLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, "", binaryVerifyListFilters{}, errors.New("invalid limit: must be a positive integer")
		}
		limit = parsed
	}
	if limit > MaxBinaryVerifyListLimit {
		limit = MaxBinaryVerifyListLimit
	}

	cursor := strings.TrimSpace(q.Get("cursor"))
	if cursor != "" {
		sep := strings.IndexByte(cursor, '-')
		if sep <= 0 || sep == len(cursor)-1 {
			return 0, "", binaryVerifyListFilters{}, errors.New("invalid cursor: must be stream id '<ms>-<seq>'")
		}
	}

	filters := binaryVerifyListFilters{
		event:     strings.TrimSpace(q.Get("event")),
		sigScheme: strings.TrimSpace(q.Get("sig_scheme")),
		endpoint:  strings.TrimSpace(q.Get("endpoint")),
	}
	// Normalise: short-form ?event=ok and ?event=fail map to full event names.
	switch filters.event {
	case "":
		// no event filter
	case "ok", model.BinaryVerifyEventOK:
		filters.event = model.BinaryVerifyEventOK
	case "fail", model.BinaryVerifyEventFail:
		filters.event = model.BinaryVerifyEventFail
	default:
		return 0, "", binaryVerifyListFilters{}, fmt.Errorf("invalid event: must be ok|fail|%q|%q",
			model.BinaryVerifyEventOK, model.BinaryVerifyEventFail)
	}
	switch filters.sigScheme {
	case "",
		model.BinaryVerifySigSchemeGPG,
		model.BinaryVerifySigSchemeCodesign,
		model.BinaryVerifySigSchemeAuthenticode,
		model.BinaryVerifySigSchemeDev:
	default:
		return 0, "", binaryVerifyListFilters{}, errors.New("invalid sig_scheme: must be one of gpg|codesign|authenticode|dev")
	}
	if len(filters.endpoint) > 256 {
		return 0, "", binaryVerifyListFilters{}, errors.New("invalid endpoint: must not exceed 256 chars")
	}
	return limit, cursor, filters, nil
}

// readBinaryVerifyEventsPage walks the tenant audit stream in reverse
// chronological order and selects entries with EventType matching the
// binary-verify family. Mirrors readAuditEventsPage's batch-and-retry
// pattern (handlers_audit_events.go) — we expect dense filtering in
// production (binary-verify is a small slice of total audit traffic),
// so the fetch multiplier is high.
func readBinaryVerifyEventsPage(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	cursor string,
	limit int,
	filters binaryVerifyListFilters,
) ([]binaryVerifyListItem, string, error) {
	if limit <= 0 {
		return nil, "", nil
	}
	if limit > MaxBinaryVerifyListLimit {
		limit = MaxBinaryVerifyListLimit
	}
	out := make([]binaryVerifyListItem, 0, MaxBinaryVerifyListLimit)

	maxID := "+"
	if cursor != "" {
		maxID = xRevBefore(cursor)
	}
	const minID = "-"

	batchSize := int64(limit * 8) // binary-verify is rare in mixed-traffic streams
	const maxBatchRoundtrips = 8

	lastEmittedID := ""
	oldestSeenID := ""
	streamDrained := false

	for round := 0; round < maxBatchRoundtrips && len(out) < limit; round++ {
		entries, err := client.XRevRangeN(ctx, streamKey, maxID, minID, batchSize).Result()
		if err != nil {
			return nil, "", err
		}
		if len(entries) == 0 {
			streamDrained = true
			break
		}
		oldestSeenID = entries[len(entries)-1].ID

		for _, msg := range entries {
			payload, ok := msg.Values["event"].(string)
			if !ok || payload == "" {
				continue
			}
			var ev audit.SIEMEvent
			if jsonErr := json.Unmarshal([]byte(payload), &ev); jsonErr != nil {
				continue
			}
			if ev.EventType != model.BinaryVerifyEventOK && ev.EventType != model.BinaryVerifyEventFail {
				continue
			}
			if filters.event != "" && ev.EventType != filters.event {
				continue
			}
			if filters.sigScheme != "" && ev.Extra[binaryVerifyExtraSigScheme] != filters.sigScheme {
				continue
			}
			if filters.endpoint != "" && ev.Extra[binaryVerifyExtraEndpoint] != filters.endpoint {
				continue
			}
			item, recoverErr := binaryVerifyFromSIEMEvent(ev)
			if recoverErr != nil {
				continue
			}
			item.Timestamp = ev.Timestamp
			out = append(out, item)
			lastEmittedID = msg.ID
			if len(out) >= limit {
				break
			}
		}

		if int64(len(entries)) < batchSize {
			streamDrained = true
			break
		}
		maxID = xRevBefore(entries[len(entries)-1].ID)
	}

	if streamDrained && len(out) < limit {
		return out, "", nil
	}
	if lastEmittedID != "" {
		return out, lastEmittedID, nil
	}
	return out, oldestSeenID, nil
}

// binaryVerifyToSIEMEvent maps the install-script event to the canonical
// audit-bus event shape. EventType uses the binary-verify-{ok,fail}
// literals so dashboards + SIEM exporters can filter directly.
func binaryVerifyToSIEMEvent(e model.BinaryVerifyEvent, tenant, endpoint string, ts time.Time) audit.SIEMEvent {
	severity := "info"
	decision := "allow"
	if e.IsFailure() {
		severity = "warning"
		decision = "deny"
	}
	extra := map[string]string{
		binaryVerifyExtraHash:        e.Hash,
		binaryVerifyExtraPath:        e.Path,
		binaryVerifyExtraSigScheme:   e.SigScheme,
		binaryVerifyExtraFingerprint: e.Fingerprint,
		binaryVerifyExtraExitCode:    strconv.Itoa(e.ExitCode),
	}
	if endpoint != "" {
		extra[binaryVerifyExtraEndpoint] = endpoint
	}
	return audit.SIEMEvent{
		Timestamp: ts,
		EventType: e.Event,
		Severity:  severity,
		TenantID:  tenant,
		Action:    binaryVerifyAuditAction,
		Decision:  decision,
		Reason:    e.Reason,
		Extra:     extra,
	}
}

// binaryVerifyFromSIEMEvent recovers the original BinaryVerifyEvent
// shape from a persisted SIEMEvent. Returns an error if the audit row
// lacks the binary-verify extra fields (which means a producer wrote
// the event without going through binaryVerifyToSIEMEvent).
func binaryVerifyFromSIEMEvent(ev audit.SIEMEvent) (binaryVerifyListItem, error) {
	if ev.Extra == nil {
		return binaryVerifyListItem{}, errors.New("audit event missing extra map")
	}
	exitCode, exitErr := strconv.Atoi(ev.Extra[binaryVerifyExtraExitCode])
	if exitErr != nil {
		return binaryVerifyListItem{}, fmt.Errorf("exit_code: %w", exitErr)
	}
	return binaryVerifyListItem{
		TenantID: ev.TenantID,
		Endpoint: ev.Extra[binaryVerifyExtraEndpoint],
		BinaryVerifyEvent: model.BinaryVerifyEvent{
			Event:       ev.EventType,
			Hash:        ev.Extra[binaryVerifyExtraHash],
			Path:        ev.Extra[binaryVerifyExtraPath],
			SigScheme:   ev.Extra[binaryVerifyExtraSigScheme],
			Fingerprint: ev.Extra[binaryVerifyExtraFingerprint],
			Reason:      ev.Reason,
			ExitCode:    exitCode,
		},
	}, nil
}

// sanitiseBinaryVerifyEndpoint clamps the operator-provided endpoint
// label so a malicious uploader can't bloat the audit index with a
// massive string. Empty input passes through (endpoint is optional).
func sanitiseBinaryVerifyEndpoint(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 256 {
		return s[:256]
	}
	return s
}

func isBinaryVerifyRequestTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

// writeJSONWithStatus is a small helper for handlers that need a non-200
// success status (here, 202 Accepted for partial ingest).
func writeJSONWithStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writeJSONWithStatus: encode failed", "err", err)
	}
}
