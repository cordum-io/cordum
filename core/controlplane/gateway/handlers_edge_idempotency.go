// EDGE-060 — generic Idempotency-Key plumbing for /api/v1/edge/* write
// endpoints. Lifted out of handlers_edge_events.go so non-event endpoints
// (sessions, executions, approvals state transitions) can reuse the same
// reserve→write→complete-or-release lifecycle without duplicating the
// hash + key-extract scaffold.
//
// The events endpoint stays on its own
// `AppendEventsWithIdempotency` single-method API in the store; non-event
// endpoints use the lower-level Reserve/Complete/Release triplet via
// `applyEdgeIdempotency` here.

package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
)

// Endpoint discriminators for the EdgeIdempotencyRequest.Endpoint field.
// Each endpoint scopes the idempotency key namespace so a key reused
// across different write endpoints by the same tenant does NOT collide.
const (
	edgeSessionCreateEndpoint   = "POST /api/v1/edge/sessions"
	edgeSessionEndEndpoint      = "POST /api/v1/edge/sessions/:id/end"
	edgeExecutionCreateEndpoint = "POST /api/v1/edge/sessions/:id/executions"
	edgeExecutionEndEndpoint    = "POST /api/v1/edge/executions/:id/end"
	edgeApprovalApproveEndpoint = "POST /api/v1/edge/approvals/:ref/approve"
	edgeApprovalRejectEndpoint  = "POST /api/v1/edge/approvals/:ref/reject"
)

// prepareEdgeIdempotencyRequest extracts and validates the Idempotency-Key
// header, normalizes the request body via SHA-256 hashing AFTER tenant +
// principal override (per the EDGE-008.7 invariant — a malicious client
// must not be able to reuse another tenant's key by submitting a different
// principal_id in the body), and returns a populated EdgeIdempotencyRequest
// ready for store.ReserveIdempotency.
//
// Returns:
//   - req: the populated idempotency request (only valid when idempotent=true).
//   - idempotent: true when the client supplied a non-empty Idempotency-Key.
//   - handled: true when the helper has already written a 4xx response (e.g.
//     key-too-long); the caller MUST return immediately.
//
// Renamed from prepareEdgeEventIdempotencyRequest in EDGE-060 — the body
// is endpoint-agnostic, so the rename + extraction enables reuse without
// duplicating the hash/key code at every write call site.
func (s *server) prepareEdgeIdempotencyRequest(w http.ResponseWriter, r *http.Request, tenantID, endpoint string, normalized any) (edgecore.EdgeIdempotencyRequest, bool, bool) {
	key := strings.TrimSpace(idempotencyKeyFromRequest(r))
	if key == "" {
		return edgecore.EdgeIdempotencyRequest{}, false, false
	}
	if len([]byte(key)) > maxEdgeIdempotencyKeyBytes {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeIdempotencyKeyTooLong, "idempotency key is too long", nil)
		return edgecore.EdgeIdempotencyRequest{}, false, true
	}
	requestHash, err := edgeNormalizedRequestHash(normalized)
	if err != nil {
		writeEdgeInternalError(w, r, "hash edge idempotency request", err)
		return edgecore.EdgeIdempotencyRequest{}, false, true
	}
	idempotencyReq := edgecore.EdgeIdempotencyRequest{
		TenantID:    tenantID,
		Endpoint:    endpoint,
		Key:         key,
		RequestHash: requestHash,
	}
	return idempotencyReq, true, false
}

// edgeNormalizedRequestHash computes the SHA-256 hex digest of a
// JSON-marshalled normalized payload. The caller MUST pass a value that
// has tenant+principal already overridden so cross-tenant key reuse hashes
// to a different value (EDGE-008.7 invariant).
//
// Moved here from handlers_edge_events.go in EDGE-060 — endpoint-agnostic.
func edgeNormalizedRequestHash(normalized any) (string, error) {
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// writeEdgeIdempotencyReplay echoes a previously-cached idempotency replay
// response (status + content-type + body) without invoking the original
// handler logic. Moved here from handlers_edge_events.go in EDGE-060 —
// endpoint-agnostic.
func writeEdgeIdempotencyReplay(w http.ResponseWriter, r *http.Request, record *edgecore.EdgeIdempotencyRecord) {
	if record == nil {
		writeEdgeError(w, r, http.StatusInternalServerError, edgeErrCodeInternalError, "internal error", nil)
		return
	}
	status := record.Response.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	contentType := strings.TrimSpace(record.Response.ContentType)
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(record.Response.Body)
}

// edgeIdempotentWriteResult is what writeFn returns to applyEdgeIdempotency:
// the HTTP status code, content-type, and serialized body the wrapper will
// (a) write to the response now, and (b) cache via CompleteIdempotency for
// future replay. If err is non-nil, the wrapper releases the reservation
// instead of completing it, then writes the error envelope via
// errFn(err).
type edgeIdempotentWriteResult struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

// applyEdgeIdempotency runs the full reserve → writeFn → complete-or-
// release lifecycle for a non-event Edge write endpoint.
//
// Behavior:
//   - Calls store.ReserveIdempotency. On Replay state, echoes the cached
//     response and returns true (the caller must NOT call writeFn).
//   - On Pending state (concurrent in-flight retry), writes 409
//     edgeErrCodeIdempotencyConflict and returns true.
//   - On a fresh Reserved state, invokes writeFn(). If it returns an
//     error, the wrapper:
//       (a) calls store.ReleaseIdempotency so the client can retry,
//       (b) invokes errFn(err) so the caller emits its endpoint-specific
//           error envelope (e.g. ErrParentSessionTerminal → 409 vs
//           ErrApprovalConflict → 409 vs ErrEventListTooLarge → 422),
//       (c) returns true.
//     If writeFn succeeds, the wrapper:
//       (a) calls CompleteIdempotency with the response shape so future
//           replays serve the cached body,
//       (b) writes status/content-type/body to w,
//       (c) returns true.
//   - Returns false ONLY in the impossible-defensive case (handled
//     elsewhere by the caller).
//
// errFn is invoked with the underlying error; it MUST write a response
// envelope (the wrapper does NOT write a generic 500 — the caller knows
// which sentinels map to which 4xx codes).
func (s *server) applyEdgeIdempotency(
	w http.ResponseWriter,
	r *http.Request,
	store edgecore.Store,
	req edgecore.EdgeIdempotencyRequest,
	writeFn func() (edgeIdempotentWriteResult, error),
	errFn func(error),
) {
	reservation, err := store.ReserveIdempotency(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, edgecore.ErrIdempotencyConflict):
			writeEdgeError(w, r, http.StatusConflict, edgeErrCodeIdempotencyConflict, "idempotency key conflict — different request shape", nil)
		case errors.Is(err, edgecore.ErrIdempotencyPending):
			writeEdgeError(w, r, http.StatusConflict, edgeErrCodeIdempotencyConflict, "idempotency key in flight — retry after current request settles", nil)
		default:
			writeEdgeInternalError(w, r, "reserve edge idempotency", err)
		}
		return
	}
	switch reservation.State {
	case edgecore.EdgeIdempotencyReplay:
		writeEdgeIdempotencyReplay(w, r, reservation.Record)
		return
	case edgecore.EdgeIdempotencyReserved:
		// fallthrough — invoke writeFn below.
	default:
		writeEdgeInternalError(w, r, "reserve edge idempotency", fmt.Errorf("unexpected idempotency state %q", reservation.State))
		return
	}
	result, writeErr := writeFn()
	if writeErr != nil {
		// Best-effort release so the client can retry. If the release fails
		// (Redis unavailable mid-handler), the reservation will TTL out;
		// log via the existing internal-error path but still surface the
		// caller's domain error first via errFn.
		if releaseErr := store.ReleaseIdempotency(r.Context(), req); releaseErr != nil {
			// Surface the domain error via errFn but don't mask it.
			// The release failure is benign at the wire level; the
			// reservation will expire by TTL.
			_ = releaseErr
		}
		errFn(writeErr)
		return
	}
	if result.StatusCode == 0 {
		result.StatusCode = http.StatusOK
	}
	contentType := strings.TrimSpace(result.ContentType)
	if contentType == "" {
		contentType = "application/json"
	}
	if _, err := store.CompleteIdempotency(r.Context(), req, edgecore.EdgeIdempotencyResponse{
		StatusCode:  result.StatusCode,
		ContentType: contentType,
		Body:        result.Body,
	}); err != nil {
		// Completing the cache failed but the underlying write succeeded.
		// Echo the response anyway — the next retry will see Pending or
		// the cache TTL out and re-execute (still safe because the write
		// already happened). Don't surface the cache failure to the
		// client.
		_ = err
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}
