package agentd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
}

type LocalServer struct {
	bindURL      string
	path         string
	nonce        string
	maxBodyBytes int64
	evaluator    claude.AgentdClient
	state        SessionState
	eventWriter  EventWriter
}

type EventWriter interface {
	WriteEvent(context.Context, edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error)
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
	return &LocalServer{
		bindURL:      bindURL,
		path:         u.Path,
		nonce:        nonce,
		maxBodyBytes: maxBody,
		evaluator:    cfg.Evaluator,
		state:        cfg.State,
		eventWriter:  cfg.EventWriter,
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

func (s *LocalServer) HookURLWithNonce() string {
	if s == nil || s.bindURL == "" || s.nonce == "" {
		return ""
	}
	u, err := url.Parse(s.bindURL)
	if err != nil {
		return s.bindURL
	}
	q := u.Query()
	q.Set("nonce", s.nonce)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *LocalServer) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeLocalError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s == nil || subtleMismatch(requestNonce(r), s.nonce) {
		writeLocalError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	maxBody := s.maxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxHookBodyBytes
	}
	body := http.MaxBytesReader(w, r.Body, maxBody)
	defer body.Close()
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
	if s.eventWriter != nil {
		_, _ = s.eventWriter.WriteEvent(r.Context(), s.hookEvent(req))
	}
	decision := claude.AgentdDecision{
		Decision: claude.DecisionDeny,
		Reason:   "Cordum Edge agentd is not ready to evaluate hooks yet; denying by fail-closed local boundary",
	}
	if s.evaluator != nil {
		evalCtx, cancel := context.WithTimeout(r.Context(), defaultHookTimeout)
		defer cancel()
		got, err := s.evaluator.EvaluateHook(evalCtx, req)
		if err != nil {
			writeLocalError(w, http.StatusServiceUnavailable, "agentd evaluator unavailable")
			return
		}
		decision = got
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(decision)
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

func (s *LocalServer) hookEvent(req claude.AgentdRequest) edgecore.AgentActionEvent {
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
		Timestamp:      time.Now().UTC(),
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
	return r.URL.Query().Get("nonce")
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

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
