// LLM-proxy event ingestion (Total Copilot governance, Phase 2).
//
// POST /api/v1/edge/llm/events accepts a bounded, content-bearing LLM
// interaction batch from a trusted enterprise LLM proxy that intercepts every
// model request/response (every chat turn). The gateway redacts the
// prompt/response content, classifies + records each turn as a LayerLLM
// AgentActionEvent through edge.Store.AppendEvents (so the chat appears in the
// Edge audit/session trail), and returns a per-event advisory decision
// (record | redact) plus the redacted content + finding types.
//
// Mandatory redaction + audit of every prompt/response is the network-layer
// backstop. The ALLOW/DENY POLICY decision (block a denied prompt) reuses the
// existing, layer-agnostic POST /api/v1/edge/evaluate path, so the safety
// kernel decision contract stays single-sourced. The endpoint is gated by
// CORDUM_EDGE_LLM_INGEST_ENABLED and returns 503 when unset.
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
	"github.com/cordum/cordum/core/edge/llmingest"
	"github.com/cordum/cordum/core/edge/runtimeingest"
)

// envLLMIngestEnabled gates the LLM ingest endpoint. Recognized truthy values:
// "true", "1", "yes" (case-insensitive). Default unset → 503.
const envLLMIngestEnabled = "CORDUM_EDGE_LLM_INGEST_ENABLED"

const llmIngestProxyRole = "llm_proxy"

// llmIngestResponse is the JSON response shape: the count of recorded events
// and the per-event advisory decisions the proxy enforces (redact/record).
type llmIngestResponse struct {
	AcceptedCount int                       `json:"accepted_count"`
	Decisions     []llmingest.EventDecision `json:"decisions,omitempty"`
	Replayed      bool                      `json:"replayed,omitempty"`
}

func llmIngestEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLLMIngestEnabled))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func (s *server) handleEdgeLLMIngest(w http.ResponseWriter, r *http.Request) {
	if !llmIngestEnabled() {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable,
			"edge llm ingestion is disabled", nil)
		return
	}
	proxyID, ok := s.requireLLMIngestProxy(w, r)
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
	// Per-route body cap. The maxBodyMiddleware enforces a global ceiling; LLM
	// ingest tightens it so a chatty proxy cannot eat the entitlement budget.
	r.Body = http.MaxBytesReader(w, r.Body, llmingest.MaxLLMBatchBodyBytes)
	batch, err := llmingest.DecodeBatch(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge,
				"llm ingest batch body too large", nil)
			return
		}
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest,
			"invalid llm ingest request", nil)
		return
	}
	if !validateLLMIngestSourceID(w, r, batch.Source.ID, proxyID) {
		return
	}
	// Tenant guard: every envelope's tenant_id must match the X-Tenant-ID
	// header. A fail-closed gate here keeps the error envelope stable and
	// avoids touching Redis on doomed batches.
	for _, env := range batch.Events {
		if strings.TrimSpace(env.TenantID) != "" && strings.TrimSpace(env.TenantID) != tenantID {
			writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeTenantMismatch,
				"tenant_id in body does not match X-Tenant-ID header", nil)
			return
		}
	}
	for i := range batch.Events {
		batch.Events[i].TenantID = tenantID
	}
	adapter := llmingest.NewAdapter(llmingest.AdapterOptions{})
	result, err := adapter.Map(batch)
	if err != nil {
		writeLLMIngestAdapterError(w, r, err)
		return
	}
	// Parent validation: the referenced session/execution must exist within
	// this tenant and the execution must be an llm-proxy execution. Unlike the
	// runtime sidecar, the proxy is shared tenant-scoped infrastructure — it is
	// NOT bound to the session's principal, but it MAY only annotate executions
	// it created under the llm-proxy adapter (so it cannot pollute hook/MCP
	// executions).
	parentValidator := newLLMIngestParentValidator(store)
	for _, ev := range result.Events {
		if err := parentValidator.validate(r.Context(), ev); err != nil {
			if errors.Is(err, errLLMIngestParentDenied) {
				writeEdgeForbidden(w, r, err)
				return
			}
			writeEdgeEventStoreError(w, r, err, "validate llm event parents")
			return
		}
	}
	proceed, reserved := s.checkLLMIngestReplay(w, r, tenantID, proxyID, batch.Nonce)
	if !proceed {
		return
	}
	if len(result.Events) > 0 {
		if _, err := store.AppendEvents(r.Context(), result.Events); err != nil {
			if reserved {
				s.releaseLLMIngestReplayReservation(r.Context(), tenantID, proxyID, batch.Nonce)
			}
			writeEdgeEventStoreError(w, r, err, "append llm events")
			return
		}
	}
	writeLLMIngestResponse(w, http.StatusCreated, llmIngestResponse{
		AcceptedCount: len(result.Events),
		Decisions:     result.Decisions,
	})
}

func writeLLMIngestResponse(w http.ResponseWriter, status int, resp llmIngestResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("json encode llm ingest response failed", "error", err)
	}
}

var errLLMIngestParentDenied = errors.New("llm proxy is not authorized for session or execution")

func (s *server) requireLLMIngestProxy(w http.ResponseWriter, r *http.Request) (string, bool) {
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		writeEdgeForbidden(w, r, errors.New("llm proxy authentication required"))
		return "", false
	}
	proxyID := strings.TrimSpace(authCtx.PrincipalID)
	if proxyID == "" {
		writeEdgeForbidden(w, r, errors.New("llm proxy principal required"))
		return "", false
	}
	if !s.llmIngestPermissionAllowed(r, authCtx.Role) {
		writeEdgeForbidden(w, r, errors.New("llm ingest proxy permission required"))
		return "", false
	}
	if !s.requireLicensePermission(w, r, auth.PermLLMIngest) {
		return "", false
	}
	return proxyID, true
}

func (s *server) llmIngestPermissionAllowed(r *http.Request, role string) bool {
	role = auth.NormalizeRole(role)
	if role == "" {
		return false
	}
	if s != nil && s.rbacStore != nil && auth.RBACEntitled(s.currentEntitlements()) {
		perms, err := s.rbacStore.ResolvePermissions(r.Context(), role)
		if err != nil {
			return false
		}
		return hasLLMIngestPermission(perms)
	}
	return s != nil && s.requireRole(r, llmIngestProxyRole) == nil
}

func hasLLMIngestPermission(perms []string) bool {
	for _, perm := range perms {
		switch strings.TrimSpace(perm) {
		case auth.PermLLMIngest, "edge.llm.*", "edge.*":
			return true
		}
	}
	return false
}

func validateLLMIngestSourceID(w http.ResponseWriter, r *http.Request, sourceID, proxyID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "llm source_id is required", nil)
		return false
	}
	if sourceID != strings.TrimSpace(proxyID) {
		writeEdgeForbidden(w, r, errors.New("llm source_id does not match authenticated proxy"))
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Parent validation
// ---------------------------------------------------------------------------

type llmIngestParentKey struct {
	tenantID string
	id       string
}

type llmIngestParentValidator struct {
	store      edgecore.Store
	sessions   map[llmIngestParentKey]*edgecore.EdgeSession
	executions map[llmIngestParentKey]*edgecore.AgentExecution
}

func newLLMIngestParentValidator(store edgecore.Store) *llmIngestParentValidator {
	return &llmIngestParentValidator{
		store:      store,
		sessions:   make(map[llmIngestParentKey]*edgecore.EdgeSession),
		executions: make(map[llmIngestParentKey]*edgecore.AgentExecution),
	}
}

func (v *llmIngestParentValidator) validate(ctx context.Context, ev edgecore.AgentActionEvent) error {
	if _, err := v.session(ctx, ev.TenantID, ev.SessionID); err != nil {
		return err
	}
	execution, err := v.execution(ctx, ev.TenantID, ev.ExecutionID)
	if err != nil {
		return err
	}
	if execution.SessionID != ev.SessionID {
		return edgeEventRequestError{status: http.StatusBadRequest, message: "event session_id does not match execution"}
	}
	if execution.Adapter != edgecore.AdapterLLMProxy {
		return errLLMIngestParentDenied
	}
	return nil
}

func (v *llmIngestParentValidator) session(ctx context.Context, tenantID, sessionID string) (*edgecore.EdgeSession, error) {
	key := llmIngestParentKey{tenantID: tenantID, id: sessionID}
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

func (v *llmIngestParentValidator) execution(ctx context.Context, tenantID, executionID string) (*edgecore.AgentExecution, error) {
	key := llmIngestParentKey{tenantID: tenantID, id: executionID}
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

// ---------------------------------------------------------------------------
// Replay window — reuses the proven runtimeingest.ReplayWindow with an LLM
// collector namespace so the keyspace never collides with runtime telemetry.
// AppendEvents does not dedup by EventID, so this is what makes a proxy retry
// (same nonce) idempotent. Nonce is optional for LLM (one synchronous call per
// chat turn); when absent, dedup is skipped.
// ---------------------------------------------------------------------------

func llmReplayCollector(proxyID string) string { return "llm:" + strings.TrimSpace(proxyID) }

func (s *server) llmIngestReplayWindow() *runtimeingest.ReplayWindow {
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

func (s *server) checkLLMIngestReplay(w http.ResponseWriter, r *http.Request, tenantID, proxyID, nonce string) (bool, bool) {
	if strings.TrimSpace(nonce) == "" {
		return true, false
	}
	window := s.llmIngestReplayWindow()
	if window == nil {
		writeEdgeInternalError(w, r, "llm replay-window unavailable", errors.New("llm replay window unavailable"))
		return false, false
	}
	accepted, err := window.Reserve(r.Context(), tenantID, llmReplayCollector(proxyID), nonce)
	if err != nil {
		if errors.Is(err, runtimeingest.ErrReplayWindowFull) {
			writeEdgeError(w, r, http.StatusTooManyRequests, edgeErrCodeReplayWindowFull,
				"replay window cardinality exceeded", nil)
			return false, false
		}
		writeEdgeInternalError(w, r, "llm replay-window check", err)
		return false, false
	}
	if !accepted {
		writeLLMIngestResponse(w, http.StatusOK, llmIngestResponse{Replayed: true})
		return false, false
	}
	return true, true
}

func (s *server) releaseLLMIngestReplayReservation(ctx context.Context, tenantID, proxyID, nonce string) {
	window := s.llmIngestReplayWindow()
	if window == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := window.Release(releaseCtx, tenantID, llmReplayCollector(proxyID), nonce); err != nil {
		slog.Warn("release llm replay reservation failed",
			"tenant_id", tenantID, "proxy_id", proxyID, "error", err)
	}
}

// writeLLMIngestAdapterError maps Adapter.Map errors to the standard Edge
// envelope. ErrLLMBatchTooLarge → 413; ErrInvalidBatch and ErrInvalidEnvelope
// → 400; anything else is an internal error.
func writeLLMIngestAdapterError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, llmingest.ErrLLMBatchTooLarge):
		writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge,
			"llm ingest batch exceeds size or count cap", nil)
	case errors.Is(err, llmingest.ErrInvalidBatch),
		errors.Is(err, llmingest.ErrInvalidEnvelope):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest,
			"invalid llm ingest request", nil)
	default:
		writeEdgeInternalError(w, r, "map llm ingest batch", err)
	}
}
