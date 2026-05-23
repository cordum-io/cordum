package agentd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/keychain"
)

type GatewayClient struct {
	baseURL     string
	apiKey      string
	tenant      string
	principalID string
	timeout     time.Duration
	client      httpDoer
}

var (
	keychainRedactionMarkerPattern = regexp.MustCompile(`\[REDACTED:[^\]]+\]`)
	agentdApprovalURLPattern       = regexp.MustCompile(`/edge/approvals/[A-Za-z0-9_-]+`)
)

func NewGatewayClient(cfg GatewayClientConfig) (*GatewayClient, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("gateway base URL is required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid gateway base URL: %w", err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultGatewayTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		httpClient := &http.Client{Timeout: timeout}
		if caFile := strings.TrimSpace(cfg.TLSCAFile); caFile != "" {
			pem, err := os.ReadFile(caFile) // #nosec G304 -- operator-configured TLS CA file path set at agentd startup; not request-derived
			if err != nil {
				return nil, fmt.Errorf("read TLS CA file %q: %w", caFile, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("TLS CA file %q contains no valid PEM certificates", caFile)
			}
			httpClient.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			}
		}
		client = httpClient
	}
	return &GatewayClient{
		baseURL:     base,
		apiKey:      strings.TrimSpace(cfg.APIKey),
		tenant:      strings.TrimSpace(cfg.TenantID),
		principalID: strings.TrimSpace(cfg.PrincipalID),
		timeout:     timeout,
		client:      client,
	}, nil
}

func (c *GatewayClient) CreateSession(ctx context.Context, req CreateSessionRequest) (CreateSessionResponse, error) {
	req = boundedCreateSessionRequest(req)
	var out CreateSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/edge/sessions", req, &out); err != nil {
		return CreateSessionResponse{}, err
	}
	return out, nil
}

func (c *GatewayClient) Heartbeat(ctx context.Context, sessionID string) (HeartbeatResponse, error) {
	var out HeartbeatResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/edge/sessions/"+url.PathEscape(sessionID)+"/heartbeat", map[string]any{}, &out); err != nil {
		return HeartbeatResponse{}, err
	}
	return out, nil
}

func (c *GatewayClient) EndExecution(ctx context.Context, executionID string, req EndExecutionRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/edge/executions/"+url.PathEscape(executionID)+"/end", req, nil)
}

func (c *GatewayClient) EndSession(ctx context.Context, sessionID string, req EndSessionRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/edge/sessions/"+url.PathEscape(sessionID)+"/end", req, nil)
}

func (c *GatewayClient) WriteEvent(ctx context.Context, event edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	var out edgecore.AgentActionEvent
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/edge/events", event, &out); err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	return out, nil
}

func (c *GatewayClient) WriteEvents(ctx context.Context, events []edgecore.AgentActionEvent) ([]edgecore.AgentActionEvent, error) {
	return c.WriteEventsWithIdempotency(ctx, events, "")
}

func (c *GatewayClient) WriteEventsWithIdempotency(ctx context.Context, events []edgecore.AgentActionEvent, idempotencyKey string) ([]edgecore.AgentActionEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	var out struct {
		Items []edgecore.AgentActionEvent `json:"items"`
	}
	req := struct {
		Events []edgecore.AgentActionEvent `json:"events"`
	}{Events: append([]edgecore.AgentActionEvent(nil), events...)}
	headers := map[string]string{}
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		headers["Idempotency-Key"] = key
	}
	if err := c.doJSONWithHeaders(ctx, http.MethodPost, "/api/v1/edge/events/batch", req, headers, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *GatewayClient) MarkSessionDegraded(ctx context.Context, state SessionState, reason string) (edgecore.AgentActionEvent, error) {
	cleanReason := boundMetadataString(redactSecretLike(reason))
	event := edgecore.AgentActionEvent{
		EventID:        "agentd-" + randomHex(16),
		SessionID:      state.SessionID,
		ExecutionID:    state.ExecutionID,
		TenantID:       state.TenantID,
		PrincipalID:    state.PrincipalID,
		Timestamp:      time.Now().UTC(),
		Layer:          edgecore.LayerSystem,
		Kind:           edgecore.EventKindSessionDegraded,
		AgentProduct:   "cordum-agentd",
		ActionName:     "agentd.degraded",
		Capability:     "edge.session.lifecycle",
		InputRedacted:  map[string]any{"reason": cleanReason},
		Decision:       edgecore.DecisionRecorded,
		DecisionReason: cleanReason,
		PolicySnapshot: state.PolicySnapshot,
		Status:         edgecore.ActionStatusDegraded,
		Labels:         edgecore.Labels{"source": "cordum-agentd"},
	}
	return c.WriteEvent(ctx, event)
}

func (c *GatewayClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	return c.doJSONWithHeaders(ctx, method, path, body, nil, out)
}

func (c *GatewayClient) doJSONWithHeaders(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
	if c == nil || c.client == nil {
		return errors.New("gateway client not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return fmt.Errorf("encode gateway request: %w", err)
		}
		reader = buf
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create gateway request: %w", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		httpReq.Header.Set("X-API-Key", c.apiKey)
	}
	if c.tenant != "" {
		httpReq.Header.Set("X-Tenant-ID", c.tenant)
	}
	if c.principalID != "" {
		httpReq.Header.Set("X-Principal-Id", c.principalID)
	}
	for key, value := range headers {
		if key = strings.TrimSpace(key); key != "" {
			httpReq.Header.Set(key, strings.TrimSpace(value))
		}
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return c.wrapTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		msg = c.redactSecrets(msg)
		if readErr != nil {
			return fmt.Errorf("gateway status %d: %s (read error: %w)", resp.StatusCode, msg, readErr)
		}
		return fmt.Errorf("gateway status %d: %s", resp.StatusCode, msg)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode gateway response: %w", err)
	}
	return nil
}

func (c *GatewayClient) wrapTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrGatewayTimeout, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%w: %v", ErrGatewayTimeout, err)
	}
	return fmt.Errorf("gateway request failed: %w", err)
}

func (c *GatewayClient) redactSecrets(message string) string {
	out := redactSecretLike(message)
	if c != nil && c.apiKey != "" {
		out = strings.ReplaceAll(out, c.apiKey, "[REDACTED]")
	}
	return out
}

func boundedCreateSessionRequest(req CreateSessionRequest) CreateSessionRequest {
	if req.PrincipalType == "" {
		req.PrincipalType = edgecore.PrincipalTypeUnknown
	}
	if req.AgentProduct == "" {
		req.AgentProduct = "claude-code"
	}
	if req.Mode == "" {
		req.Mode = edgecore.SessionModeLocalDev
	}
	if req.PolicyMode == "" {
		req.PolicyMode = edgecore.PolicyModeObserve
	}
	if req.EnforcementLayers == nil {
		req.EnforcementLayers = edgecore.EnforcementLayers{"hook": true, "agentd": true}
	}
	req.TenantID = boundMetadataString(req.TenantID)
	req.PrincipalID = boundMetadataString(req.PrincipalID)
	req.AgentProduct = boundMetadataString(req.AgentProduct)
	req.AgentVersion = boundMetadataString(req.AgentVersion)
	req.Repo = boundMetadataString(req.Repo)
	req.GitRemote = boundMetadataString(req.GitRemote)
	req.GitBranch = boundMetadataString(req.GitBranch)
	req.GitSHA = boundMetadataString(req.GitSHA)
	req.CWD = boundMetadataString(req.CWD)
	req.HostID = boundMetadataString(req.HostID)
	req.DeviceID = boundMetadataString(req.DeviceID)
	req.TraceID = boundMetadataString(req.TraceID)
	req.WorkflowRunID = boundMetadataString(req.WorkflowRunID)
	req.JobID = boundMetadataString(req.JobID)
	req.PolicySnapshot = boundMetadataString(req.PolicySnapshot)
	req.Labels = boundedLabels(req.Labels)
	return req
}

func boundMetadataString(value string) string {
	if len(value) <= MaxGatewayMetadataValueBytes {
		return value
	}
	const marker = "…"
	limit := MaxGatewayMetadataValueBytes - len(marker)
	if limit < 0 {
		limit = MaxGatewayMetadataValueBytes
	}
	return value[:limit] + marker
}

func boundedLabels(labels edgecore.Labels) edgecore.Labels {
	if len(labels) == 0 {
		return nil
	}
	out := make(edgecore.Labels, len(labels))
	for k, v := range labels {
		out[boundMetadataString(k)] = boundMetadataString(v)
	}
	return out
}

func redactSecretLike(value string) string {
	protectedValue, restore := protectAgentdApprovalURLs(value)
	out := keychain.RedactSecretLike(protectedValue)
	out = restore(out)
	out = strings.ReplaceAll(out, "[REDACTED:bearer]", "Bearer [REDACTED]")
	return keychainRedactionMarkerPattern.ReplaceAllString(out, "[REDACTED]")
}

func protectAgentdApprovalURLs(value string) (string, func(string) string) {
	var protected []string
	out := agentdApprovalURLPattern.ReplaceAllStringFunc(value, func(match string) string {
		protected = append(protected, match)
		return fmt.Sprintf("\ue000CORDUM_APPROVAL_URL_%d\ue000", len(protected)-1)
	})
	return out, func(redacted string) string {
		for i, original := range protected {
			redacted = strings.ReplaceAll(redacted, fmt.Sprintf("\ue000CORDUM_APPROVAL_URL_%d\ue000", i), original)
		}
		return redacted
	}
}

func randomHex(n int) string {
	if n <= 0 {
		n = 16
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
