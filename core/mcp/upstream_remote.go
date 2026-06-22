package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/edge"
)

const (
	remoteUpstreamDefaultTimeout = 30 * time.Second
	// remoteMaxResponseBytes caps a single upstream response so a hostile or
	// runaway upstream cannot exhaust memory in the gateway process.
	remoteMaxResponseBytes = 8 << 20 // 8 MiB
	mcpSessionIDHeader     = "Mcp-Session-Id"
	mcpInitializedMethod   = "notifications/initialized"
)

// RemoteUpstreamConfig is the resolved dialing configuration for one upstream MCP
// server. Build it from a registry record via ResolveUpstreamConfig.
type RemoteUpstreamConfig struct {
	// Endpoint is the remote MCP server URL (http/https).
	Endpoint string
	// AuthHeader is sent verbatim as the Authorization header when non-empty.
	// Resolved from the upstream AuthSecretRef; NEVER logged.
	AuthHeader string
	// AllowedHosts optionally restricts the outbound host (SSRF allowlist).
	AllowedHosts []string
	// AllowPrivateHosts disables private-IP rejection (loopback/test only).
	AllowPrivateHosts bool
	// ProtocolVersion is sent on initialize; defaults to DefaultProtocolVersion.
	ProtocolVersion string
	// ClientInfo identifies this proxy to the upstream on initialize.
	ClientInfo Implementation
	// HTTPClient, when set, is used as-is and the SSRF-pinned client is skipped
	// (tests inject an httptest client).
	HTTPClient *http.Client
	// Timeout bounds each upstream round-trip.
	Timeout time.Duration
}

// RemoteUpstream proxies tools/list and tools/call to a remote MCP server over
// streamable-HTTP (handling both plain application/json and text/event-stream
// responses). It implements ToolService (so an MCPServer can serve a remote
// catalog through the scope filter) and UpstreamToolCaller (so WithPolicyGate
// forwards ALLOW'd calls to it). It is general-purpose: no upstream-specific
// knowledge lives here — a concrete upstream (e.g. cordum.monday) is configuration.
type RemoteUpstream struct {
	endpoint   string
	authHeader string
	protocol   string
	clientInfo Implementation
	httpClient *http.Client
	timeout    time.Duration

	mu        sync.Mutex
	sessionID string // cached MCP session id; "" until initialized
}

var (
	_ ToolService        = (*RemoteUpstream)(nil)
	_ UpstreamToolCaller = (*RemoteUpstream)(nil)
)

// ResolveUpstreamConfig builds dialing config from a registry record, resolving
// the auth secret ref via resolve. It fails closed on an unsupported transport or
// an unresolved secret so a misconfigured upstream never reaches the dial path.
func ResolveUpstreamConfig(srv *edge.UpstreamServer, resolve SecretResolver) (RemoteUpstreamConfig, error) {
	if srv == nil {
		return RemoteUpstreamConfig{}, errors.New("mcp: nil upstream record")
	}
	switch strings.ToLower(strings.TrimSpace(srv.Transport)) {
	case "http", "sse":
	default:
		return RemoteUpstreamConfig{}, fmt.Errorf("mcp: unsupported remote transport %q", srv.Transport)
	}
	if strings.TrimSpace(srv.Endpoint) == "" {
		return RemoteUpstreamConfig{}, errors.New("mcp: remote upstream endpoint required")
	}
	cfg := RemoteUpstreamConfig{Endpoint: strings.TrimSpace(srv.Endpoint)}
	if ref := strings.TrimSpace(srv.AuthSecretRef); ref != "" {
		if resolve == nil {
			return RemoteUpstreamConfig{}, errors.New("mcp: secret resolver required for authenticated upstream")
		}
		token, err := resolve(ref)
		if err != nil {
			return RemoteUpstreamConfig{}, err
		}
		cfg.AuthHeader = token
	}
	return cfg, nil
}

// NewRemoteUpstream constructs a proxy from resolved config. It fails closed when
// the endpoint is missing or resolves to an unsafe (private/metadata) address so a
// misconfigured upstream never degrades to an unguarded dial.
func NewRemoteUpstream(ctx context.Context, cfg RemoteUpstreamConfig) (*RemoteUpstream, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("mcp: remote upstream endpoint required")
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("mcp: invalid upstream endpoint: %w", err)
	}
	client := cfg.HTTPClient
	if client == nil {
		ips, verr := validateAndResolveOutboundURL(ctx, target, cfg.AllowedHosts, cfg.AllowPrivateHosts)
		if verr != nil {
			return nil, fmt.Errorf("mcp: upstream endpoint rejected: %w", verr)
		}
		client = &http.Client{
			Timeout: nonZeroTimeout(cfg.Timeout),
			Transport: &http.Transport{
				DialContext:         pinnedDialer(ips),
				TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
				ForceAttemptHTTP2:   true,
				MaxIdleConns:        4,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		}
	}
	info := cfg.ClientInfo
	if strings.TrimSpace(info.Name) == "" {
		info = Implementation{Name: "cordum-mcp-gateway", Version: "1"}
	}
	return &RemoteUpstream{
		endpoint:   endpoint,
		authHeader: cfg.AuthHeader,
		protocol:   firstNonEmpty(cfg.ProtocolVersion, DefaultProtocolVersion),
		clientInfo: info,
		httpClient: client,
		timeout:    nonZeroTimeout(cfg.Timeout),
	}, nil
}

// ListTools returns the upstream catalog. Fail-closed: any transport or decode
// error yields an empty slice (the caller sees no tools) rather than a panic; the
// failure is observable on the audited tools/call path.
func (u *RemoteUpstream) ListTools(ctx context.Context) []Tool {
	raw, err := u.rpc(ctx, MethodToolsList, nil)
	if err != nil {
		// Fail closed to an empty catalog, but make the cause observable: a
		// silent empty list is indistinguishable from "upstream has no tools",
		// which made an upstream-throttle/dial failure impossible to diagnose.
		slog.WarnContext(ctx, "mcp: remote upstream tools/list failed; serving empty catalog",
			"endpoint", u.endpoint, "error", err.Error())
		return []Tool{}
	}
	var res ToolListResult
	if err := json.Unmarshal(raw, &res); err != nil || res.Tools == nil {
		slog.WarnContext(ctx, "mcp: remote upstream tools/list decode failed; serving empty catalog",
			"endpoint", u.endpoint)
		return []Tool{}
	}
	return res.Tools
}

// Call forwards a tools/call to the upstream (ToolService).
func (u *RemoteUpstream) Call(ctx context.Context, name string, args json.RawMessage) (*ToolCallResult, error) {
	return u.Invoke(ctx, ToolCallParams{Name: name, Arguments: args})
}

// Invoke forwards an ALLOW'd tools/call to the upstream and returns its result
// (UpstreamToolCaller). InvokeToolWithPolicy reaches this ONLY on ALLOW, so a
// DENY decision never dials the upstream.
func (u *RemoteUpstream) Invoke(ctx context.Context, params ToolCallParams) (*ToolCallResult, error) {
	if strings.TrimSpace(params.Name) == "" {
		return nil, errors.New("mcp: tool name required")
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal tool call: %w", err)
	}
	raw, err := u.rpc(ctx, MethodToolsCall, payload)
	if err != nil {
		return nil, err
	}
	var res ToolCallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, errors.New("mcp: malformed upstream tool result")
	}
	return &res, nil
}

// rpc performs one JSON-RPC method call, establishing the MCP session on first
// use and re-initializing once if the upstream reports the session invalid.
func (u *RemoteUpstream) rpc(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	sessionID, err := u.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	raw, sessionErr, err := u.dispatch(ctx, sessionID, method, params)
	if err == nil || !sessionErr {
		return raw, err
	}
	u.resetSession(sessionID)
	sessionID, err = u.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	raw, _, err = u.dispatch(ctx, sessionID, method, params)
	return raw, err
}

// dispatch posts a single request method and parses the JSON-RPC envelope. The
// returned bool reports a probable session error so rpc can re-initialize once.
func (u *RemoteUpstream) dispatch(ctx context.Context, sessionID, method string, params json.RawMessage) (json.RawMessage, bool, error) {
	body, _, status, err := u.post(ctx, sessionID, jsonrpcRequest(2, method, params))
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusNotFound || (status == http.StatusBadRequest && looksLikeSessionError(body)) {
		return nil, true, fmt.Errorf("mcp: upstream session invalid (status %d)", status)
	}
	if status < 200 || status >= 300 {
		return nil, false, fmt.Errorf("mcp: upstream %s failed: status %d", method, status)
	}
	result, rpcErr, perr := parseJSONRPCBody(body)
	if perr != nil {
		return nil, false, perr
	}
	if rpcErr != nil {
		// Surface only the numeric code (our own method name); the upstream
		// message is withheld to avoid leaking remote internals to the client.
		return nil, false, fmt.Errorf("mcp: upstream %s returned error %d", method, rpcErr.Code)
	}
	return result, false, nil
}

// ensureSession returns the cached session id, performing the initialize handshake
// on first use. The lock is held across the one-time handshake so concurrent
// first-callers share a single session.
func (u *RemoteUpstream) ensureSession(ctx context.Context) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sessionID != "" {
		return u.sessionID, nil
	}
	sid, err := u.initialize(ctx)
	if err != nil {
		return "", err
	}
	u.sessionID = sid
	return sid, nil
}

func (u *RemoteUpstream) resetSession(stale string) {
	u.mu.Lock()
	if u.sessionID == stale {
		u.sessionID = ""
	}
	u.mu.Unlock()
}

// initialize runs initialize -> capture Mcp-Session-Id -> notifications/initialized.
func (u *RemoteUpstream) initialize(ctx context.Context) (string, error) {
	params, _ := json.Marshal(InitializeParams{
		ProtocolVersion: u.protocol,
		Capabilities:    map[string]any{},
		ClientInfo:      &u.clientInfo,
	})
	body, sid, status, err := u.post(ctx, "", jsonrpcRequest(1, MethodInitialize, params))
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("mcp: upstream initialize failed: status %d", status)
	}
	if _, rpcErr, perr := parseJSONRPCBody(body); perr != nil {
		return "", perr
	} else if rpcErr != nil {
		return "", fmt.Errorf("mcp: upstream initialize returned error %d", rpcErr.Code)
	}
	if strings.TrimSpace(sid) == "" {
		return "", errors.New("mcp: upstream did not return a session id")
	}
	// notifications/initialized is a notification (no response expected). Some
	// servers require it before tools/*; failures here are non-fatal.
	_, _, _, _ = u.post(ctx, sid, jsonrpcNotification(mcpInitializedMethod))
	return sid, nil
}

// post issues one HTTP POST to the upstream and returns the (size-capped) body,
// any Mcp-Session-Id response header, and the status code.
func (u *RemoteUpstream) post(ctx context.Context, sessionID string, body []byte) ([]byte, string, int, error) {
	if u.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, u.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if u.authHeader != "" {
		req.Header.Set("Authorization", u.authHeader)
	}
	if sessionID != "" {
		req.Header.Set(mcpSessionIDHeader, sessionID)
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, errors.New("mcp: upstream request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := io.ReadAll(io.LimitReader(resp.Body, remoteMaxResponseBytes))
	if err != nil {
		return nil, "", resp.StatusCode, errors.New("mcp: reading upstream response failed")
	}
	return read, resp.Header.Get(mcpSessionIDHeader), resp.StatusCode, nil
}

// parseJSONRPCBody extracts the JSON-RPC result/error from an upstream response,
// handling both a plain application/json body and a text/event-stream body whose
// payload rides on SSE `data:` lines.
func parseJSONRPCBody(body []byte) (json.RawMessage, *JSONRPCError, error) {
	payload := body
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\ndata:")) {
		extracted, ok := extractSSEData(body)
		if !ok {
			return nil, nil, errors.New("mcp: no JSON-RPC data event in upstream stream")
		}
		payload = extracted
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *JSONRPCError   `json:"error"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, nil, errors.New("mcp: malformed upstream response")
	}
	return env.Result, env.Error, nil
}

// extractSSEData returns the concatenated data lines of the last SSE event that
// parses as a JSON-RPC envelope carrying a result or error.
func extractSSEData(body []byte) (json.RawMessage, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), remoteMaxResponseBytes)
	var current strings.Builder
	var last json.RawMessage
	found := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		candidate := json.RawMessage(current.String())
		var probe struct {
			Result json.RawMessage `json:"result"`
			Error  *JSONRPCError   `json:"error"`
		}
		if json.Unmarshal(candidate, &probe) == nil && (probe.Result != nil || probe.Error != nil) {
			last = candidate
			found = true
		}
		current.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			current.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return last, found
}

func looksLikeSessionError(body []byte) bool {
	return bytes.Contains(bytes.ToLower(body), []byte("session"))
}

func jsonrpcRequest(id int, method string, params json.RawMessage) []byte {
	msg := map[string]any{"jsonrpc": JSONRPCVersion, "id": id, "method": method}
	if len(params) > 0 {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	return b
}

func jsonrpcNotification(method string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": JSONRPCVersion, "method": method})
	return b
}

func nonZeroTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return remoteUpstreamDefaultTimeout
	}
	return d
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
