package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

// MaxAuditEventsLimit caps a single /api/v1/audit/events page. A tenant
// with millions of events can otherwise hit Redis hard if a client asks
// for an unbounded fetch; clamp early so the limit is a hard contract
// rather than a soft suggestion.
const MaxAuditEventsLimit = 200

// AuditErrCodeInvalid* values are stable machine-readable 400 codes that
// dashboard / SIEM clients can pin against without message-string matching.
const (
	AuditErrCodeInvalidCursor   = "INVALID_CURSOR"
	AuditErrCodeInvalidLimit    = "INVALID_LIMIT"
	AuditErrCodeInvalidFrom     = "INVALID_FROM"
	AuditErrCodeInvalidTo       = "INVALID_TO"
	AuditErrCodeInvalidRange    = "INVALID_RANGE"
	AuditErrCodeInvalidCategory = "INVALID_CATEGORY"
	AuditErrCodeInvalidQuery    = "INVALID_QUERY"
)

// These sentinel parse errors let the handler choose stable code fields while
// preserving sanitized human-readable messages. The messages intentionally do
// not echo caller-supplied query values.
var (
	errInvalidAuditCursor = errors.New(
		"invalid cursor: must be redis stream id '<unsigned-ms>-<unsigned-seq>'",
	)
	errInvalidAuditLimit    = errors.New("invalid limit: must be a positive integer")
	errInvalidAuditFrom     = errors.New("invalid from: must be RFC3339 timestamp")
	errInvalidAuditTo       = errors.New("invalid to: must be RFC3339 timestamp")
	errInvalidAuditRange    = errors.New("invalid range: to must not precede from")
	errInvalidAuditCategory = errors.New("invalid category: must be governance or routine")
)

// defaultAuditEventsLimit is the page size when the caller omits ?limit.
// Mirrors the dashboard's default render budget so the first request
// fills a screen without forcing a second round-trip.
const defaultAuditEventsLimit = 100

// auditEventsFetchMultiplier widens the underlying Redis fetch so the
// filter pipeline still produces a full page when many events are dropped
// by event_type / severity / from-to / search. Cap so a worst-case "all
// rows dropped" filter still costs at most this many round-trips per page.
const auditEventsFetchMultiplier = 4

// extraSecretKeyPattern matches Extra-map keys that smell like secret
// material. Stripping them defense-in-depth protects against an
// emit-site that forgot to redact: even if a producer puts a token into
// Extra, the read surface refuses to surface it. Match is
// case-insensitive over the whole key, not anchored, so apiKey /
// API_KEY / privateKey / authToken / clientSecret all hit.
var extraSecretKeyPattern = regexp.MustCompile(`(?i)(secret|token|password|api[_-]?key|private[_-]?key)`)

// auditEventResponseItem is the wire shape returned by /api/v1/audit/events.
// Mirrors audit.SIEMEvent (exporter.go:172) including chain fields so
// forensic consumers can re-verify via the chain integrity surface. The
// only mutation we apply is Extra-key redaction (see redactExtraSecrets).
type auditEventResponseItem struct {
	ID            string            `json:"id"`
	Seq           int64             `json:"seq"`
	Timestamp     time.Time         `json:"timestamp"`
	EventType     string            `json:"event_type"`
	Severity      string            `json:"severity"`
	TenantID      string            `json:"tenant_id"`
	AgentID       string            `json:"agent_id,omitempty"`
	AgentName     string            `json:"agent_name,omitempty"`
	AgentRiskTier string            `json:"agent_risk_tier,omitempty"`
	JobID         string            `json:"job_id,omitempty"`
	Action        string            `json:"action"`
	Decision      string            `json:"decision,omitempty"`
	MatchedRule   string            `json:"matched_rule,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	RiskTags      []string          `json:"risk_tags,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	PolicyVersion string            `json:"policy_version,omitempty"`
	Identity      string            `json:"identity,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
	EventHash     string            `json:"event_hash,omitempty"`
	PrevHash      string            `json:"prev_hash,omitempty"`
}

// auditEventsResponse is the envelope. next_cursor is the opaque Redis
// Stream ID to feed back on the next request; empty means end-of-stream.
type auditEventsResponse struct {
	Items      []auditEventResponseItem `json:"items"`
	NextCursor string                   `json:"next_cursor"`
	Returned   int                      `json:"returned"`
}

// auditEventsFilters captures the parsed query string. All fields are
// zero-value-default so missing query params disable the corresponding
// filter without further branching.
type auditEventsFilters struct {
	eventType string
	severity  string
	category  string
	from      time.Time
	to        time.Time
	search    string
	hasFrom   bool
	hasTo     bool
}

// handleListAuditEvents serves GET /api/v1/audit/events — the SIEM-feed
// read surface for the dashboard Audit Log. Unlike /policy/audit (a
// policy-bundle audit subset), this endpoint walks the per-tenant Redis
// Stream populated by the chainer, so every chained event — MCP, edge,
// worker, output policy, delegation, license, etc. — is reachable from
// a single read.
func (s *server) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermAuditRead, []string{"admin"}, client) {
		return
	}

	tenant, err := s.resolveTenant(r, strings.TrimSpace(r.URL.Query().Get("tenant")))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	limit, cursor, filters, parseErr := parseAuditEventsQuery(r)
	if parseErr != nil {
		writeJSONError(w, http.StatusBadRequest,
			auditEventsParseErrorCode(parseErr), parseErr.Error())
		return
	}

	if s.auditChainer == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable,
			"audit_chainer_not_installed; audit events cannot be served. "+
				"Check gateway boot logs for 'audit chain enabled'.")
		return
	}

	streamKey := s.auditChainer.StreamKey(tenant)
	items, nextCursor, fetchErr := readAuditEventsPage(
		r.Context(), client, streamKey, cursor, limit, filters,
	)
	if fetchErr != nil {
		writeInternalError(w, r, "audit events: read stream", fetchErr)
		return
	}

	for i := range items {
		items[i].Extra = redactExtraSecrets(items[i].Extra)
	}

	slog.Info("audit events listed",
		"tenant", tenant,
		"limit", limit,
		"returned", len(items),
		"next_cursor_present", nextCursor != "",
		"filter_event_type", filters.eventType,
		"filter_severity", filters.severity,
		"filter_category", filters.category,
		"filter_from", filters.from.Format(time.RFC3339),
		"filter_to", filters.to.Format(time.RFC3339),
		"filter_search", filters.search != "",
	)

	emitAuditReadMetaEvent(r.Context(), s.auditChainer, tenant, len(items), filters)

	writeJSON(w, auditEventsResponse{
		Items:      items,
		NextCursor: nextCursor,
		Returned:   len(items),
	})
}

func auditEventsParseErrorCode(err error) string {
	switch {
	case errors.Is(err, errInvalidAuditCursor):
		return AuditErrCodeInvalidCursor
	case errors.Is(err, errInvalidAuditLimit):
		return AuditErrCodeInvalidLimit
	case errors.Is(err, errInvalidAuditFrom):
		return AuditErrCodeInvalidFrom
	case errors.Is(err, errInvalidAuditTo):
		return AuditErrCodeInvalidTo
	case errors.Is(err, errInvalidAuditRange):
		return AuditErrCodeInvalidRange
	case errors.Is(err, errInvalidAuditCategory):
		return AuditErrCodeInvalidCategory
	default:
		return AuditErrCodeInvalidQuery
	}
}

// parseAuditEventsQuery parses the query string into a (limit, cursor,
// filters) tuple. Invalid values produce a 400 error rather than silent
// defaults — a malformed ?from= probably means a client bug worth
// surfacing.
func parseAuditEventsQuery(r *http.Request) (int, string, auditEventsFilters, error) {
	q := r.URL.Query()

	limit := defaultAuditEventsLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, "", auditEventsFilters{}, errInvalidAuditLimit
		}
		limit = parsed
	}
	if limit > MaxAuditEventsLimit {
		limit = MaxAuditEventsLimit
	}

	cursor := strings.TrimSpace(q.Get("cursor"))
	if cursor != "" {
		// Cursors are Redis Stream IDs (`<unsigned-ms>-<unsigned-seq>`).
		// We refuse anything else so a malformed value surfaces as 400
		// here instead of a 500 from XRevRangeN downstream. A shape-only
		// check (single dash, both halves non-empty) is not enough: a
		// value like "abc-def" passes that filter and only fails at the
		// Redis call, leaking the upstream error envelope through
		// writeInternalError. Parse both halves as base-10 unsigned ints
		// — matches the redis stream id grammar exactly — and return the
		// typed sentinel so the handler can attach the stable
		// INVALID_CURSOR error code.
		dashes := strings.Count(cursor, "-")
		if dashes != 1 {
			return 0, "", auditEventsFilters{}, errInvalidAuditCursor
		}
		sep := strings.IndexByte(cursor, '-')
		msPart := cursor[:sep]
		seqPart := cursor[sep+1:]
		if msPart == "" || seqPart == "" {
			return 0, "", auditEventsFilters{}, errInvalidAuditCursor
		}
		if _, err := strconv.ParseUint(msPart, 10, 64); err != nil {
			return 0, "", auditEventsFilters{}, errInvalidAuditCursor
		}
		if _, err := strconv.ParseUint(seqPart, 10, 64); err != nil {
			return 0, "", auditEventsFilters{}, errInvalidAuditCursor
		}
	}

	filters := auditEventsFilters{
		eventType: strings.TrimSpace(q.Get("event_type")),
		severity:  strings.TrimSpace(q.Get("severity")),
		search:    strings.ToLower(strings.TrimSpace(q.Get("search"))),
	}
	// category is additive to event_type/severity/search. Reject an unknown
	// value (400) rather than silently returning an empty page; accept the
	// governance/routine taxonomy case-insensitively, normalised to lower.
	if raw := strings.TrimSpace(q.Get("category")); raw != "" {
		cat := strings.ToLower(raw)
		if cat != audit.CategoryGovernance && cat != audit.CategoryRoutine {
			return 0, "", auditEventsFilters{}, errInvalidAuditCategory
		}
		filters.category = cat
	}
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return 0, "", auditEventsFilters{}, errInvalidAuditFrom
		}
		filters.from = t
		filters.hasFrom = true
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return 0, "", auditEventsFilters{}, errInvalidAuditTo
		}
		filters.to = t
		filters.hasTo = true
	}
	if filters.hasFrom && filters.hasTo && filters.to.Before(filters.from) {
		return 0, "", auditEventsFilters{}, errInvalidAuditRange
	}
	return limit, cursor, filters, nil
}

// readAuditEventsPage walks the tenant's Redis Stream in reverse-chrono
// order, applies the in-process filters, and returns at most `limit`
// items. The cursor is the opaque Redis Stream ID of the last event the
// previous page returned — we pass it as the inclusive upper bound for
// the next XRevRange page minus one tick. End-of-stream returns empty
// next_cursor.
func readAuditEventsPage(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	cursor string,
	limit int,
	filters auditEventsFilters,
) ([]auditEventResponseItem, string, error) {
	if limit <= 0 {
		return nil, "", nil
	}
	// Defense-in-depth: parseAuditEventsQuery already clamps to
	// MaxAuditEventsLimit, but readAuditEventsPage is exported within
	// the package and CodeQL's taint tracker flags `make(..., limit)`
	// as user-influenced even when an upstream guard is in place. Use
	// MaxAuditEventsLimit as the allocation capacity directly — it's a
	// compile-time constant the analyzer can prove is bounded — and
	// keep the working `limit` value re-clamped so the rest of the
	// function logic stays correct without re-flowing the taint.
	if limit > MaxAuditEventsLimit {
		limit = MaxAuditEventsLimit
	}
	out := make([]auditEventResponseItem, 0, MaxAuditEventsLimit)

	maxID := "+"
	if cursor != "" {
		// XRevRangeN inclusive; subtract one stream-ID tick so we don't
		// re-emit the cursor entry itself on the next page.
		maxID = xRevBefore(cursor)
	}
	minID := "-"

	// Fetch a generous batch so filter drops don't starve the page.
	batchSize := int64(limit * auditEventsFetchMultiplier)
	const maxBatchRoundtrips = 8

	// streamDrained becomes true when an XRevRangeN call returns
	// strictly fewer entries than asked for. Distinguishing "page
	// filled" from "stream exhausted" matters: the former wants a
	// cursor for the next page, the latter wants an empty cursor.
	streamDrained := false
	lastEmittedID := ""
	// oldestSeenID tracks the deepest stream ID we visited across all
	// batches (regardless of filter match). When the page fills via
	// emitted matches the cursor IS lastEmittedID, but under heavy
	// filtering we may exhaust maxBatchRoundtrips before the page
	// fills — and without a forward marker, the client would see an
	// empty cursor and incorrectly stop paginating. oldestSeenID is
	// that forward marker so the next page picks up from the right spot.
	oldestSeenID := ""
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
			// "event" is the Redis Stream field name used by the audit
			// Chainer for the canonical JSON payload (chain.go:58
			// chainStreamFieldEvent — unexported, so we mirror the
			// literal here rather than reach into the package).
			payload, ok := msg.Values["event"].(string)
			if !ok || payload == "" {
				// Skip malformed entries — they belong to the chain
				// verification surface, not the read surface.
				continue
			}
			var ev audit.SIEMEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			if !matchesFilters(&ev, filters) {
				continue
			}
			out = append(out, toAuditEventResponseItem(msg.ID, &ev))
			lastEmittedID = msg.ID
			if len(out) >= limit {
				break
			}
		}

		if int64(len(entries)) < batchSize {
			streamDrained = true
			break
		}
		// Continue from just before the oldest entry we saw.
		maxID = xRevBefore(entries[len(entries)-1].ID)
	}

	if streamDrained && len(out) < limit {
		// Drained the stream without filling the page — no more pages.
		return out, "", nil
	}
	if lastEmittedID != "" {
		return out, lastEmittedID, nil
	}
	// Search exhausted maxBatchRoundtrips with zero matches but the
	// stream had more entries we never reached. Hand the client a
	// forward cursor (oldest entry we walked past) so the next page
	// resumes deeper instead of looping at the top.
	return out, oldestSeenID, nil
}

// matchesFilters applies the in-process predicate set. Returns true when
// the event should be kept.
func matchesFilters(ev *audit.SIEMEvent, f auditEventsFilters) bool {
	if f.eventType != "" && !strings.EqualFold(ev.EventType, f.eventType) {
		return false
	}
	if f.severity != "" && !strings.EqualFold(ev.Severity, f.severity) {
		return false
	}
	if f.category != "" && audit.CategoryFor(ev.EventType) != f.category {
		return false
	}
	if f.hasFrom && ev.Timestamp.Before(f.from) {
		return false
	}
	if f.hasTo && ev.Timestamp.After(f.to) {
		return false
	}
	if f.search != "" {
		combined := strings.ToLower(
			ev.Action + " " + ev.EventType + " " + ev.AgentID + " " +
				ev.JobID + " " + ev.Identity + " " + ev.Reason,
		)
		if !strings.Contains(combined, f.search) {
			return false
		}
	}
	return true
}

// toAuditEventResponseItem converts the in-memory SIEMEvent into the
// wire shape. Stream message ID becomes the item ID so consumers have a
// stable, opaque per-event handle without needing knowledge of the
// underlying stream encoding.
func toAuditEventResponseItem(id string, ev *audit.SIEMEvent) auditEventResponseItem {
	extra := make(map[string]string, len(ev.Extra))
	for k, v := range ev.Extra {
		extra[k] = v
	}
	return auditEventResponseItem{
		ID:            id,
		Seq:           ev.Seq,
		Timestamp:     ev.Timestamp,
		EventType:     ev.EventType,
		Severity:      ev.Severity,
		TenantID:      ev.TenantID,
		AgentID:       ev.AgentID,
		AgentName:     ev.AgentName,
		AgentRiskTier: ev.AgentRiskTier,
		JobID:         ev.JobID,
		Action:        ev.Action,
		Decision:      ev.Decision,
		MatchedRule:   ev.MatchedRule,
		Reason:        ev.Reason,
		RiskTags:      append([]string(nil), ev.RiskTags...),
		Capabilities:  append([]string(nil), ev.Capabilities...),
		PolicyVersion: ev.PolicyVersion,
		Identity:      ev.Identity,
		Extra:         extra,
		EventHash:     ev.EventHash,
		PrevHash:      ev.PrevHash,
	}
}

// redactExtraSecrets strips Extra-map keys that match the secret-key
// pattern. We delete the key entirely rather than masking the value so
// the response surface gives no signal that a secret was present —
// neither key nor value can leak through downstream log aggregators.
func redactExtraSecrets(extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return extra
	}
	for k := range extra {
		if extraSecretKeyPattern.MatchString(k) {
			delete(extra, k)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

// emitAuditReadMetaEvent appends an audit.read.events SIEMEvent so the
// read surface is itself audited. Without this, an attacker who learned
// the endpoint could mass-exfiltrate the audit feed without leaving a
// trace in the very chain meant to detect tampering.
//
// Failures are logged but never returned: a misconfigured Redis must
// not 500 the read path. Compliance reviews care that the meta-event
// is attempted on every successful call, not that it must persist.
func emitAuditReadMetaEvent(
	ctx context.Context,
	chainer *audit.Chainer,
	tenant string,
	returned int,
	filters auditEventsFilters,
) {
	if chainer == nil {
		return
	}
	extra := map[string]string{
		"returned": strconv.Itoa(returned),
	}
	if filters.eventType != "" {
		extra["filter_event_type"] = filters.eventType
	}
	if filters.severity != "" {
		extra["filter_severity"] = filters.severity
	}
	if filters.category != "" {
		extra["filter_category"] = filters.category
	}
	if filters.hasFrom {
		extra["filter_from"] = filters.from.Format(time.RFC3339)
	}
	if filters.hasTo {
		extra["filter_to"] = filters.to.Format(time.RFC3339)
	}
	ev := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: "audit.read.events",
		Severity:  audit.SeverityInfo,
		TenantID:  tenant,
		Action:    "audit_read",
		Extra:     extra,
	}
	if err := chainer.Append(ctx, &ev); err != nil {
		slog.Warn("audit meta-event append failed",
			"tenant", tenant, "error", err)
	}
}

// xRevBefore returns the Redis Stream ID one tick below the given ID,
// suitable as the inclusive upper bound for the NEXT page of an
// XRevRange walk. Stream IDs are ms-time + "-" + sequence; we
// decrement the sequence (or step the ms-time when the sequence is 0).
//
// NOTE: a similar helper exists in core/audit/chain_verify.go used for
// the cross-window verification bootstrap; that one is unexported. We
// duplicate the few lines here rather than wire a back-channel just to
// keep the gateway → audit dependency one-directional and the helper
// scoped to its use site.
func xRevBefore(id string) string {
	sepIdx := strings.IndexByte(id, '-')
	if sepIdx < 0 {
		return id
	}
	msPart := id[:sepIdx]
	seqPart := id[sepIdx+1:]
	seq, err := strconv.ParseUint(seqPart, 10, 64)
	if err != nil {
		return id
	}
	if seq > 0 {
		return msPart + "-" + strconv.FormatUint(seq-1, 10)
	}
	ms, err := strconv.ParseInt(msPart, 10, 64)
	if err != nil || ms <= 0 {
		return id
	}
	return strconv.FormatInt(ms-1, 10) + "-18446744073709551615"
}
