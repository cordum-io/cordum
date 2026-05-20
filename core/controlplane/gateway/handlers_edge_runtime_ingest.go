// EDGE-144 — Runtime event ingestion adapter design + skeleton.
//
// POST /api/v1/edge/runtime/events accepts a bounded, redacted runtime
// telemetry batch from a trusted sidecar (Tetragon, Falco, an in-cluster
// eBPF collector, etc.) and persists the mapped AgentActionEvent records
// through the existing edge.Store.AppendEvents path. The endpoint is
// gated by CORDUM_EDGE_RUNTIME_INGEST_ENABLED and returns 503
// service_unavailable when unset — production behavior for this
// milestone is "route exists, no writes."
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/runtimeingest"
)

// envRuntimeIngestEnabled gates the runtime ingest endpoint. Recognized
// truthy values: "true", "1", "yes" (case-insensitive). Default unset → 503.
const envRuntimeIngestEnabled = "CORDUM_EDGE_RUNTIME_INGEST_ENABLED"

const runtimeIngestCollectorRole = "runtime_collector"

// runtimeIngestResponse is the JSON response shape. Counts and per-drop
// reasons are bounded so a malicious source cannot inflate the response.
type runtimeIngestResponse struct {
	AcceptedCount int                        `json:"accepted_count"`
	DroppedCount  int                        `json:"dropped_count"`
	Dropped       []runtimeingest.DropReport `json:"dropped,omitempty"`
	Replayed      bool                       `json:"replayed,omitempty"`
}

// runtimeIngestEnabled returns true when the operator has explicitly
// enabled runtime ingestion. Anything other than the canonical truthy
// strings keeps the endpoint disabled.
func runtimeIngestEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envRuntimeIngestEnabled))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func (s *server) handleEdgeRuntimeIngest(w http.ResponseWriter, r *http.Request) {
	if !runtimeIngestEnabled() {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable,
			"edge runtime ingestion is disabled", nil)
		return
	}
	collectorID, ok := s.requireRuntimeIngestCollector(w, r)
	if !ok {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	// Per-route body cap. The maxBodyMiddleware enforces a global ceiling;
	// runtime ingest tightens it further so a chatty source cannot eat the
	// whole entitlement budget on one batch.
	r.Body = http.MaxBytesReader(w, r.Body, runtimeingest.MaxRuntimeBatchBodyBytes)
	batch, err := runtimeingest.DecodeBatch(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge,
				"runtime ingest batch body too large", nil)
			return
		}
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest,
			"invalid runtime ingest request", nil)
		return
	}
	if !validateRuntimeIngestSourceID(w, r, batch.Source.ID, collectorID) {
		return
	}
	// Tenant guard: every envelope's tenant_id must match the X-Tenant-ID
	// header. The body must not be able to claim a different tenant —
	// validateEdgeEventParents would catch foreign session/execution IDs
	// later, but a fail-closed tenant gate here keeps the error envelope
	// stable and avoids touching Redis on doomed batches.
	for _, env := range batch.Events {
		if strings.TrimSpace(env.TenantID) != "" && strings.TrimSpace(env.TenantID) != tenantID {
			writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeTenantMismatch,
				"tenant_id in body does not match X-Tenant-ID header", nil)
			return
		}
	}
	// Stamp tenant from the header onto every envelope so the adapter
	// never has to guess and the mapped events are guaranteed same-tenant.
	for i := range batch.Events {
		batch.Events[i].TenantID = tenantID
	}
	adapter := runtimeingest.NewAdapter(runtimeingest.AdapterOptions{})
	result, err := adapter.Map(batch)
	if err != nil {
		writeRuntimeIngestAdapterError(w, r, err)
		return
	}
	// Parent validation must run before any append. Reuse the events
	// pipeline's helper so missing-session and execution-session-mismatch
	// errors land in the same envelope shape the rest of /api/v1/edge/*
	// emits.
	parentValidator := newRuntimeIngestParentValidator(store)
	for _, ev := range result.Events {
		if err := parentValidator.validate(r.Context(), ev, collectorID); err != nil {
			if errors.Is(err, errRuntimeIngestCollectorParentDenied) {
				writeEdgeForbidden(w, r, err)
				return
			}
			writeEdgeEventStoreError(w, r, err, "validate runtime event parents")
			return
		}
	}
	proceed, reserved := s.checkRuntimeIngestReplay(w, r, tenantID, collectorID, batch.Nonce)
	if !proceed {
		return
	}
	if len(result.Events) > 0 {
		if _, err := store.AppendEvents(r.Context(), result.Events); err != nil {
			if reserved {
				s.releaseRuntimeIngestReplayReservation(r.Context(), tenantID, collectorID, batch.Nonce)
			}
			writeEdgeEventStoreError(w, r, err, "append runtime events")
			return
		}
	}
	if reserved {
		s.recordRuntimeReplayFirstSeen(tenantID, collectorID)
	}
	resp := runtimeIngestResponse{
		AcceptedCount: len(result.Events),
		DroppedCount:  len(result.Dropped),
		Dropped:       result.Dropped,
	}
	writeRuntimeIngestResponse(w, http.StatusCreated, resp)
}

func (s *server) checkRuntimeIngestReplay(
	w http.ResponseWriter,
	r *http.Request,
	tenantID string,
	collectorID string,
	nonce string,
) (bool, bool) {
	if strings.TrimSpace(nonce) == "" {
		return true, false
	}
	window := s.runtimeIngestReplayWindow()
	if window == nil {
		writeEdgeInternalError(w, r, "runtime replay-window unavailable", errRuntimeReplayWindowUnavailable)
		return false, false
	}
	accepted, err := window.Reserve(r.Context(), tenantID, collectorID, nonce)
	if err != nil {
		if errors.Is(err, runtimeingest.ErrReplayWindowFull) {
			s.recordRuntimeReplayWindowFull(tenantID, collectorID)
			writeEdgeError(w, r, http.StatusTooManyRequests, edgeErrCodeReplayWindowFull,
				"replay window cardinality exceeded", nil)
			return false, false
		}
		writeEdgeInternalError(w, r, "runtime replay-window check", err)
		return false, false
	}
	if !accepted {
		s.recordRuntimeReplayReplayed(tenantID, collectorID)
		writeRuntimeIngestResponse(w, http.StatusOK, runtimeIngestResponse{Replayed: true})
		return false, false
	}
	return true, true
}

func (s *server) runtimeIngestReplayWindow() *runtimeingest.ReplayWindow {
	if s == nil {
		return nil
	}
	if s.runtimeReplayWindow != nil {
		return s.runtimeReplayWindow
	}
	if s.jobStore == nil || s.jobStore.Client() == nil {
		return nil
	}
	return runtimeingest.NewReplayWindow(
		s.jobStore.Client(),
		runtimeingest.ReplayWindowTTL,
		runtimeingest.MaxReplayWindowCardinality,
	)
}

func (s *server) releaseRuntimeIngestReplayReservation(ctx context.Context, tenantID, collectorID, nonce string) {
	window := s.runtimeIngestReplayWindow()
	if window == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := window.Release(releaseCtx, tenantID, collectorID, nonce); err != nil {
		slog.Warn("release runtime replay reservation failed",
			"tenant_id", tenantID,
			"collector_id", collectorID,
			"error", err)
	}
}

func (s *server) recordRuntimeReplayFirstSeen(tenantID, collectorID string) {
	if s != nil && s.edgeRecorder != nil {
		s.edgeRecorder.RecordRuntimeReplayFirstSeen(tenantID, collectorID)
	}
}

func (s *server) recordRuntimeReplayReplayed(tenantID, collectorID string) {
	if s != nil && s.edgeRecorder != nil {
		s.edgeRecorder.RecordRuntimeReplayReplayed(tenantID, collectorID)
	}
}

func (s *server) recordRuntimeReplayWindowFull(tenantID, collectorID string) {
	if s != nil && s.edgeRecorder != nil {
		s.edgeRecorder.RecordRuntimeReplayWindowFull(tenantID, collectorID)
	}
}

func writeRuntimeIngestResponse(w http.ResponseWriter, status int, resp runtimeIngestResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("json encode runtime ingest response failed", "error", err)
	}
}

var errRuntimeIngestCollectorParentDenied = errors.New("runtime collector is not authorized for session or execution")
var errRuntimeReplayWindowUnavailable = errors.New("runtime replay window unavailable")

func (s *server) requireRuntimeIngestCollector(w http.ResponseWriter, r *http.Request) (string, bool) {
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		writeEdgeForbidden(w, r, errors.New("runtime collector authentication required"))
		return "", false
	}
	collectorID := strings.TrimSpace(authCtx.PrincipalID)
	if collectorID == "" {
		writeEdgeForbidden(w, r, errors.New("runtime collector principal required"))
		return "", false
	}
	if !s.runtimeIngestPermissionAllowed(r, authCtx.Role) {
		writeEdgeForbidden(w, r, errors.New("runtime ingest collector permission required"))
		return "", false
	}
	if !s.requireLicensePermission(w, r, auth.PermRuntimeIngest) {
		return "", false
	}
	return collectorID, true
}

func (s *server) runtimeIngestPermissionAllowed(r *http.Request, role string) bool {
	role = auth.NormalizeRole(role)
	if role == "" {
		return false
	}
	if s != nil && s.rbacStore != nil && auth.RBACEntitled(s.currentEntitlements()) {
		perms, err := s.rbacStore.ResolvePermissions(r.Context(), role)
		if err != nil {
			return false
		}
		return hasRuntimeIngestPermission(perms)
	}
	return s != nil && s.requireRole(r, runtimeIngestCollectorRole) == nil
}

func hasRuntimeIngestPermission(perms []string) bool {
	for _, perm := range perms {
		switch strings.TrimSpace(perm) {
		case auth.PermRuntimeIngest, "edge.runtime.*", "edge.*":
			return true
		}
	}
	return false
}

func validateRuntimeIngestSourceID(w http.ResponseWriter, r *http.Request, sourceID, collectorID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "runtime source_id is required", nil)
		return false
	}
	if sourceID != strings.TrimSpace(collectorID) {
		writeEdgeForbidden(w, r, errors.New("runtime source_id does not match authenticated collector"))
		return false
	}
	return true
}

type runtimeIngestParentKey struct {
	tenantID string
	id       string
}

type runtimeIngestParentValidator struct {
	store      edgecore.Store
	sessions   map[runtimeIngestParentKey]*edgecore.EdgeSession
	executions map[runtimeIngestParentKey]*edgecore.AgentExecution
}

func newRuntimeIngestParentValidator(store edgecore.Store) *runtimeIngestParentValidator {
	return &runtimeIngestParentValidator{
		store:      store,
		sessions:   make(map[runtimeIngestParentKey]*edgecore.EdgeSession),
		executions: make(map[runtimeIngestParentKey]*edgecore.AgentExecution),
	}
}

func (v *runtimeIngestParentValidator) validate(
	ctx context.Context,
	ev edgecore.AgentActionEvent,
	collectorID string,
) error {
	collectorID = strings.TrimSpace(collectorID)
	if collectorID == "" {
		return errRuntimeIngestCollectorParentDenied
	}
	session, err := v.session(ctx, ev.TenantID, ev.SessionID)
	if err != nil {
		return err
	}
	execution, err := v.execution(ctx, ev.TenantID, ev.ExecutionID)
	if err != nil {
		return err
	}
	if execution.SessionID != ev.SessionID {
		return edgeEventRequestError{status: http.StatusBadRequest, message: "event session_id does not match execution"}
	}
	if strings.TrimSpace(session.PrincipalID) != collectorID {
		return errRuntimeIngestCollectorParentDenied
	}
	if strings.TrimSpace(execution.WorkerID) != collectorID {
		return errRuntimeIngestCollectorParentDenied
	}
	return nil
}

func (v *runtimeIngestParentValidator) session(ctx context.Context, tenantID, sessionID string) (*edgecore.EdgeSession, error) {
	key := runtimeIngestParentKey{tenantID: tenantID, id: sessionID}
	if session, ok := v.sessions[key]; ok {
		return session, nil
	}
	session, found, err := v.store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	if !found || session == nil {
		return nil, edgecore.ErrNotFound
	}
	v.sessions[key] = session
	return session, nil
}

func (v *runtimeIngestParentValidator) execution(ctx context.Context, tenantID, executionID string) (*edgecore.AgentExecution, error) {
	key := runtimeIngestParentKey{tenantID: tenantID, id: executionID}
	if execution, ok := v.executions[key]; ok {
		return execution, nil
	}
	execution, found, err := v.store.GetExecution(ctx, tenantID, executionID)
	if err != nil {
		return nil, err
	}
	if !found || execution == nil {
		return nil, edgecore.ErrNotFound
	}
	v.executions[key] = execution
	return execution, nil
}

// writeRuntimeIngestAdapterError maps Adapter.Map errors to the standard
// Edge envelope. ErrRuntimeBatchTooLarge → 413; ErrInvalidBatch and
// ErrInvalidEnvelope → 400. Anything else is an internal error and is
// logged via writeEdgeInternalError so unexpected adapter failures are
// surfaced to the operator without leaking detail to the client.
func writeRuntimeIngestAdapterError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, runtimeingest.ErrRuntimeBatchTooLarge):
		writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge,
			"runtime ingest batch exceeds size or count cap", nil)
	case errors.Is(err, runtimeingest.ErrInvalidBatch),
		errors.Is(err, runtimeingest.ErrInvalidEnvelope):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest,
			"invalid runtime ingest request", nil)
	default:
		writeEdgeInternalError(w, r, "map runtime ingest batch", err)
	}
}
