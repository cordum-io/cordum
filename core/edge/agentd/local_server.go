package agentd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

const (
	defaultMaxHookBodyBytes = 1 << 20
	agentdNonceHeader       = "X-Cordum-Agentd-Nonce"
)

type LocalServerConfig struct {
	BindURL      string
	Nonce        string
	MaxBodyBytes int64
	Evaluator    claude.AgentdClient
	State        SessionState
	EventWriter  EventWriter
	// Recorder captures EDGE-014-style metrics emitted by the local hook
	// handler (currently only the EDGE-059 response-write-abort counter).
	// Defaults to NoopRecorder when nil so existing callers are unaffected.
	Recorder edgecore.Recorder
}

type LocalServer struct {
	bindURL      string
	path         string
	nonce        string
	maxBodyBytes int64
	evaluator    claude.AgentdClient
	state        SessionState
	eventWriter  EventWriter
	recorder     edgecore.Recorder
}

type EventWriter interface {
	WriteEvent(context.Context, edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error)
}

type EventBatchWriter interface {
	WriteEvents(context.Context, []edgecore.AgentActionEvent) ([]edgecore.AgentActionEvent, error)
}

type EventBatchIdempotencyWriter interface {
	WriteEventsWithIdempotency(context.Context, []edgecore.AgentActionEvent, string) ([]edgecore.AgentActionEvent, error)
}

type bufferedHookEvaluator interface {
	EvaluateHookWithEventWriter(context.Context, claude.AgentdRequest, EventWriter) (claude.AgentdDecision, error)
}

func NewLocalServer(cfg LocalServerConfig) (*LocalServer, error) {
	bindURL := strings.TrimSpace(cfg.BindURL)
	if bindURL == "" {
		bindURL = defaultAgentdBindURL
	}
	if err := validateLocalBindURL(bindURL); err != nil {
		return nil, err
	}
	u, err := url.Parse(bindURL)
	if err != nil {
		return nil, fmt.Errorf("invalid agentd bind URL: %w", err)
	}
	nonce := strings.TrimSpace(cfg.Nonce)
	if nonce == "" {
		generated, err := generateNonce()
		if err != nil {
			return nil, err
		}
		nonce = generated
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxHookBodyBytes
	}
	recorder := cfg.Recorder
	if recorder == nil {
		recorder = edgecore.NewNoopRecorder()
	}
	return &LocalServer{
		bindURL:      bindURL,
		path:         u.Path,
		nonce:        nonce,
		maxBodyBytes: maxBody,
		evaluator:    cfg.Evaluator,
		state:        cfg.State,
		eventWriter:  cfg.EventWriter,
		recorder:     recorder,
	}, nil
}

func (s *LocalServer) Handler() http.Handler {
	mux := http.NewServeMux()
	path := s.path
	if path == "" {
		path = defaultAgentdHookPath
	}
	mux.HandleFunc(path, s.handleHook)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			writeLocalError(w, http.StatusNotFound, "not found")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *LocalServer) EndpointURL() string {
	if s == nil {
		return ""
	}
	return s.bindURL
}

func (s *LocalServer) Nonce() string {
	if s == nil {
		return ""
	}
	return s.nonce
}

func (s *LocalServer) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLocalError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	nonce := requestNonce(r)
	if s == nil || subtleMismatch(nonce, s.nonce) {
		writeLocalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	maxBody := s.maxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxHookBodyBytes
	}
	body := http.MaxBytesReader(w, r.Body, maxBody)
	defer func() { _ = body.Close() }()
	var req claude.AgentdRequest
	dec := json.NewDecoder(body)
	if err := dec.Decode(&req); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeLocalError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeLocalError(w, http.StatusBadRequest, "invalid hook request")
		return
	}
	if !s.requestMatchesState(req) {
		writeLocalError(w, http.StatusConflict, "hook session does not match active agentd session")
		return
	}
	buffer := newHookEventBuffer(s.hookEventAt(req, time.Now().UTC()))
	decision := claude.AgentdDecision{
		Decision: claude.DecisionDeny,
		Reason:   "Cordum Edge agentd is not ready to evaluate hooks yet; denying by fail-closed local boundary",
	}
	if s.evaluator != nil {
		got, err := s.evaluateHookWithBuffer(r.Context(), req, buffer)
		if err != nil {
			writeLocalError(w, http.StatusServiceUnavailable, "agentd evaluator unavailable")
			return
		}
		decision = got
	} else if err := s.appendDecisionEvent(buffer, req, decision); err != nil {
		writeLocalError(w, http.StatusServiceUnavailable, "agentd event evidence unavailable")
		return
	}
	bufferedEvents := buffer.Events()
	if err := s.flushHookEventBatch(r.Context(), bufferedEvents, s.hookBatchIdempotencyKey(req, bufferedEvents)); err != nil {
		writeLocalError(w, http.StatusServiceUnavailable, "agentd event writer unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(decision); err != nil {
		// EDGE-059 — surface response-write aborts (slow-loris guard) as a
		// bounded metric so operators can detect goroutine-pool DoS attempts.
		// http.Server.WriteTimeout firing is the headline case (manifests as
		// a network-write deadline exceeded error from the encoder); other
		// write errors (client disconnect, RST) are also captured but
		// distinguished via the bounded reason label.
		reason := "write_error"
		if isWriteTimeoutError(err) {
			reason = "write_timeout"
		}
		s.recorder.RecordAgentdResponseWriteAborted(reason)
	}
}

// isWriteTimeoutError reports whether err is the net/http
// "i/o timeout" error that http.Server.WriteTimeout produces when a slow-
// reading client triggers the connection-write deadline. Distinguishing
// this from generic write errors lets the operator tell a slow-loris from
// a normal client disconnect in the bounded metric.
func isWriteTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// net/http wraps the deadline error as os.ErrDeadlineExceeded under
	// some Go versions; check both for portability across the support window.
	return errors.Is(err, os.ErrDeadlineExceeded)
}

func (s *LocalServer) requestMatchesState(req claude.AgentdRequest) bool {
	if s == nil {
		return false
	}
	if s.state.SessionID != "" && req.SessionID != "" && req.SessionID != s.state.SessionID {
		return false
	}
	if s.state.ExecutionID != "" && req.ExecutionID != "" && req.ExecutionID != s.state.ExecutionID {
		return false
	}
	return true
}

func (s *LocalServer) hookEventAt(req claude.AgentdRequest, receivedAt time.Time) edgecore.AgentActionEvent {
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	labels := edgecore.Labels{
		"source": "cordum-agentd",
	}
	if s.state.TraceID != "" {
		labels["trace_id"] = s.state.TraceID
	}
	for k, v := range req.Labels {
		if !isSensitiveMetadataKey(k) {
			labels[boundMetadataString(k)] = boundMetadataString(redactSecretLike(v))
		}
	}
	if req.ActionHash != "" {
		labels["action_hash"] = req.ActionHash
	}
	input := safeInputRedacted(req.InputRedacted)
	if len(input) == 0 {
		input = map[string]any{
			"event_name": req.EventName,
			"tool_name":  req.ToolName,
		}
	}
	return edgecore.AgentActionEvent{
		EventID:        "agentd-" + randomHex(16),
		SessionID:      nonEmpty(req.SessionID, s.state.SessionID),
		ExecutionID:    nonEmpty(req.ExecutionID, s.state.ExecutionID),
		TenantID:       nonEmpty(req.TenantID, s.state.TenantID),
		PrincipalID:    nonEmpty(req.PrincipalID, s.state.PrincipalID),
		Timestamp:      receivedAt.UTC(),
		Layer:          edgecore.LayerHook,
		Kind:           hookEventKind(req.EventName),
		AgentProduct:   "claude-code",
		ToolName:       boundMetadataString(req.ToolName),
		ToolUseID:      boundMetadataString(req.ToolUseID),
		ActionName:     "claude." + strings.ToLower(strings.TrimSpace(req.EventName)),
		Capability:     boundMetadataString(req.Capability),
		RiskTags:       append([]string(nil), req.RiskTags...),
		InputRedacted:  input,
		InputHash:      boundMetadataString(req.InputHash),
		Decision:       edgecore.DecisionRecorded,
		DecisionReason: "received by cordum-agentd; evaluation not ready",
		PolicySnapshot: s.state.PolicySnapshot,
		DurationMS:     req.DurationMS,
		Status:         edgecore.ActionStatusDegraded,
		Labels:         labels,
	}
}

func (s *LocalServer) evaluateHookWithBuffer(ctx context.Context, req claude.AgentdRequest, buffer *hookEventBuffer) (claude.AgentdDecision, error) {
	evalCtx, cancel := context.WithTimeout(ctx, defaultHookTimeout)
	defer cancel()
	if evaluator, ok := s.evaluator.(bufferedHookEvaluator); ok {
		before := buffer.Len()
		decision, err := evaluator.EvaluateHookWithEventWriter(evalCtx, req, buffer)
		if err != nil {
			s.recordHookTimeoutIfDeadline(err, evalCtx)
			return decision, err
		}
		if buffer.Len() == before {
			return decision, s.appendDecisionEvent(buffer, req, decision)
		}
		return decision, nil
	}
	decision, err := s.evaluator.EvaluateHook(evalCtx, req)
	if err != nil {
		s.recordHookTimeoutIfDeadline(err, evalCtx)
		return decision, err
	}
	return decision, s.appendDecisionEvent(buffer, req, decision)
}

func (s *LocalServer) recordHookTimeoutIfDeadline(err error, ctx context.Context) {
	if s == nil || s.recorder == nil {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.recorder.RecordHookTimeout("kernel")
	}
}

func (s *LocalServer) appendDecisionEvent(buffer *hookEventBuffer, req claude.AgentdRequest, decision claude.AgentdDecision) error {
	event, err := BuildDecisionEvidenceEvent(DecisionEvidence{
		State:      s.state,
		Request:    req,
		Response:   s.evaluateResponseFromDecision(decision),
		DurationMS: req.DurationMS,
	})
	if err != nil {
		return fmt.Errorf("build decision evidence: %w", err)
	}
	_, err = buffer.WriteEvent(context.Background(), event)
	return err
}

func (s *LocalServer) evaluateResponseFromDecision(decision claude.AgentdDecision) EvaluateResponse {
	return EvaluateResponse{
		Decision:                 string(edgeDecisionFromClaude(decision.Decision)),
		Reason:                   decision.Reason,
		PolicySnapshot:           s.state.PolicySnapshot,
		ApprovalRef:              decision.ApprovalRef,
		PermissionDecision:       string(decision.Decision),
		PermissionDecisionReason: decision.Reason,
		UpdatedInput:             decision.UpdatedInput,
	}
}

func (s *LocalServer) flushHookEventBatch(ctx context.Context, events []edgecore.AgentActionEvent, idempotencyKey string) error {
	if len(events) == 0 || s.eventWriter == nil {
		return nil
	}
	var (
		written []edgecore.AgentActionEvent
		err     error
	)
	if writer, ok := s.eventWriter.(EventBatchIdempotencyWriter); ok {
		written, err = writer.WriteEventsWithIdempotency(ctx, events, idempotencyKey)
	} else if writer, ok := s.eventWriter.(EventBatchWriter); ok {
		written, err = writer.WriteEvents(ctx, events)
	} else {
		return errors.New("agentd event writer does not support atomic event batches")
	}
	if err != nil {
		return fmt.Errorf("write hook event batch: %w", err)
	}
	if len(written) != len(events) {
		return fmt.Errorf("write hook event batch: wrote %d of %d events", len(written), len(events))
	}
	return nil
}

func (s *LocalServer) hookBatchIdempotencyKey(req claude.AgentdRequest, events []edgecore.AgentActionEvent) string {
	h := sha256.New()
	writeHashPart := func(value string) {
		_, _ = h.Write([]byte(boundMetadataString(value)))
		_, _ = h.Write([]byte{0})
	}
	writeHashPart("agentd-hook-events-v1")
	writeHashPart(nonEmpty(req.SessionID, s.state.SessionID))
	writeHashPart(nonEmpty(req.ExecutionID, s.state.ExecutionID))
	writeHashPart(req.EventName)
	writeHashPart(req.ToolUseID)
	writeHashPart(req.ActionHash)
	writeHashPart(req.InputHash)
	if redacted := safeInputRedacted(req.InputRedacted); len(redacted) > 0 {
		if encoded, err := json.Marshal(redacted); err == nil {
			_, _ = h.Write(encoded)
		}
	}
	// Approval-flow stateful retries (initial REQUIRE_APPROVAL → consume
	// allow → terminal already-consumed deny) all share the same request
	// shape (session/execution/event_name/tool_use_id/action_hash/input_hash)
	// but produce different decision-evidence events. Without including the
	// per-event identifiers, the second call would replay the first key,
	// the Gateway would see the same idempotency key with a different
	// RequestHash, and AppendEventsWithIdempotency would 409 with
	// edgeErrCodeIdempotencyConflict (store_redis.go:957). Hashing the
	// per-event identifiers + decisions makes each call's flush land on a
	// unique key while keeping retries of the SAME call (same events with
	// same EventIDs) idempotent for network-drop replay.
	for _, event := range events {
		writeHashPart(event.EventID)
		writeHashPart(string(event.Kind))
		writeHashPart(string(event.Decision))
		writeHashPart(event.RuleID)
		writeHashPart(event.ApprovalRef)
	}
	sum := h.Sum(nil)
	return "agentd-hook-" + hex.EncodeToString(sum[:16])
}

type hookEventBuffer struct {
	events []edgecore.AgentActionEvent
}

func newHookEventBuffer(events ...edgecore.AgentActionEvent) *hookEventBuffer {
	return &hookEventBuffer{events: append([]edgecore.AgentActionEvent(nil), events...)}
}

func (b *hookEventBuffer) WriteEvent(_ context.Context, event edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	b.events = append(b.events, event)
	return event, nil
}

func (b *hookEventBuffer) Events() []edgecore.AgentActionEvent {
	if b == nil {
		return nil
	}
	return append([]edgecore.AgentActionEvent(nil), b.events...)
}

func (b *hookEventBuffer) Len() int {
	if b == nil {
		return 0
	}
	return len(b.events)
}

func hookEventKind(eventName string) edgecore.EventKind {
	switch eventName {
	case "PreToolUse":
		return edgecore.EventKindHookPreToolUse
	case "PostToolUse":
		return edgecore.EventKindHookPostToolUse
	case "PostToolUseFailure":
		return edgecore.EventKindHookPostToolUseFailure
	case "UserPromptSubmit":
		return edgecore.EventKindHookUserPromptSubmit
	case "ConfigChange":
		return edgecore.EventKindHookConfigChange
	case "FileChanged":
		return edgecore.EventKindHookFileChanged
	default:
		return edgecore.EventKindHookPolicyDecision
	}
}

func safeInputRedacted(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if isSensitiveMetadataKey(k) {
			continue
		}
		out[k] = redactAny(v)
	}
	return out
}

func redactAny(v any) any {
	switch x := v.(type) {
	case string:
		return redactSecretLike(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactAny(item)
		}
		return out
	case map[string]any:
		return safeInputRedacted(x)
	default:
		return v
	}
}

func writeLocalError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func requestNonce(r *http.Request) string {
	if r == nil {
		return ""
	}
	if value := r.Header.Get(agentdNonceHeader); strings.TrimSpace(value) != "" {
		return value
	}
	return ""
}

func subtleMismatch(got, want string) bool {
	if len(got) != len(want) || got == "" || want == "" {
		return true
	}
	var diff byte
	for i := range got {
		diff |= got[i] ^ want[i]
	}
	return diff != 0
}

func generateNonce() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate agentd nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

func PrepareUnixSocketPath(_ context.Context, socketPath string) error {
	if strings.TrimSpace(socketPath) == "" {
		return errors.New("socket path is required")
	}
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to remove non-socket path %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat socket path: %w", err)
	}
	return nil
}

func statPathMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}
