package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

const mondayStreamableHTTPProtocolVersion = "2024-11-05"

// fakeUpstreamServer emulates a remote streamable-HTTP MCP server (the shape
// Monday's mcp.monday.com exposes): initialize -> Mcp-Session-Id header ->
// notifications/initialized -> tools/list | tools/call. framing selects whether
// responses are plain application/json or text/event-stream (SSE `data:` lines).
type fakeUpstreamServer struct {
	framing       string // "sse" | "json"
	tools         []Tool
	failToolCall  bool
	omitSessionID bool

	mu           sync.Mutex
	methods      []string
	accepts      []string        // Accept header per request (parallel to methods)
	contentTypes []string        // Content-Type header per request
	sessions     []string        // Mcp-Session-Id header per request ("" when absent)
	initParams   json.RawMessage // params of the initialize request
	lastAuth     string
	lastSession  string
	lastArgs     json.RawMessage
	toolCalls    int
}

func (f *fakeUpstreamServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &msg)
		f.mu.Lock()
		f.methods = append(f.methods, msg.Method)
		f.accepts = append(f.accepts, r.Header.Get("Accept"))
		f.contentTypes = append(f.contentTypes, r.Header.Get("Content-Type"))
		f.sessions = append(f.sessions, r.Header.Get(mcpSessionIDHeader))
		f.lastAuth = r.Header.Get("Authorization")
		if msg.Method == MethodInitialize {
			f.initParams = msg.Params
		}
		if sid := r.Header.Get(mcpSessionIDHeader); sid != "" {
			f.lastSession = sid
		}
		f.mu.Unlock()

		switch msg.Method {
		case MethodInitialize:
			if !f.omitSessionID {
				w.Header().Set(mcpSessionIDHeader, "sess-xyz")
			}
			f.write(w, msg.ID, map[string]any{
				"protocolVersion": mondayStreamableHTTPProtocolVersion,
				"serverInfo":      map[string]any{"name": "fake-monday", "version": "1.0.0"},
				"capabilities":    map[string]any{},
			}, nil)
		case mcpInitializedMethod:
			w.WriteHeader(http.StatusOK)
		case MethodToolsList:
			f.write(w, msg.ID, map[string]any{"tools": f.tools}, nil)
		case MethodToolsCall:
			var p ToolCallParams
			_ = json.Unmarshal(msg.Params, &p)
			f.mu.Lock()
			f.toolCalls++
			f.lastArgs = p.Arguments
			f.mu.Unlock()
			if f.failToolCall {
				f.write(w, msg.ID, nil, &JSONRPCError{Code: -32004, Message: "SECRET upstream detail must not leak"})
				return
			}
			f.write(w, msg.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok:" + string(p.Arguments)}},
				"isError": false,
			}, nil)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func (f *fakeUpstreamServer) write(w http.ResponseWriter, id json.RawMessage, result any, rpcErr *JSONRPCError) {
	env := map[string]any{"jsonrpc": JSONRPCVersion, "id": id}
	if rpcErr != nil {
		env["error"] = rpcErr
	} else {
		env["result"] = result
	}
	body, _ := json.Marshal(env)
	if f.framing == "sse" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message\ndata: " + string(body) + "\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func newTestUpstream(t *testing.T, f *fakeUpstreamServer) (*RemoteUpstream, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	up, err := NewRemoteUpstream(context.Background(), RemoteUpstreamConfig{
		Endpoint:   srv.URL,
		AuthHeader: "test-token-123",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewRemoteUpstream: %v", err)
	}
	return up, srv
}

func mondayLikeTools() []Tool {
	return []Tool{
		{Name: "get_board_items_page", Description: "paged item read"},
		{Name: "all_monday_api", Description: "generic GraphQL passthrough"},
	}
}

func TestRemoteUpstream_ListTools_And_Invoke(t *testing.T) {
	for _, framing := range []string{"sse", "json"} {
		t.Run(framing, func(t *testing.T) {
			f := &fakeUpstreamServer{framing: framing, tools: mondayLikeTools()}
			up, _ := newTestUpstream(t, f)

			tools := up.ListTools(context.Background())
			got := []string{}
			for _, tl := range tools {
				got = append(got, tl.Name)
			}
			want := "get_board_items_page,all_monday_api"
			if strings.Join(got, ",") != want {
				t.Fatalf("ListTools names = %q, want %q", strings.Join(got, ","), want)
			}

			args := json.RawMessage(`{"board_id":"5097518101"}`)
			res, err := up.Invoke(context.Background(), ToolCallParams{Name: "get_board_items_page", Arguments: args})
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if res == nil || len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, `"board_id":"5097518101"`) {
				t.Fatalf("Invoke result did not echo forwarded args: %+v", res)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if string(f.lastArgs) != `{"board_id":"5097518101"}` {
				t.Fatalf("upstream received args %q, want forwarded board_id", string(f.lastArgs))
			}
			if f.lastAuth != "test-token-123" {
				t.Fatalf("upstream Authorization = %q, want resolved token", f.lastAuth)
			}
			if f.lastSession != "sess-xyz" {
				t.Fatalf("upstream session header = %q, want sess-xyz from initialize", f.lastSession)
			}
		})
	}
}

func TestRemoteUpstream_Invoke_UpstreamErrorSanitized(t *testing.T) {
	f := &fakeUpstreamServer{framing: "sse", tools: mondayLikeTools(), failToolCall: true}
	up, _ := newTestUpstream(t, f)
	_, err := up.Invoke(context.Background(), ToolCallParams{Name: "all_monday_api", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected error from upstream JSON-RPC error response")
	}
	if strings.Contains(err.Error(), "SECRET upstream detail") {
		t.Fatalf("upstream error message leaked to caller: %v", err)
	}
	if !strings.Contains(err.Error(), "-32004") {
		t.Fatalf("expected sanitized error to carry the numeric code, got %v", err)
	}
}

func TestRemoteUpstream_Initialize_RequiresSessionID(t *testing.T) {
	f := &fakeUpstreamServer{framing: "json", tools: mondayLikeTools(), omitSessionID: true}
	up, _ := newTestUpstream(t, f)
	// Fail-closed: no Mcp-Session-Id from initialize -> ListTools sees no tools.
	if got := up.ListTools(context.Background()); len(got) != 0 {
		t.Fatalf("ListTools should be empty when session id is missing, got %d", len(got))
	}
	_, err := up.Invoke(context.Background(), ToolCallParams{Name: "get_board_items_page"})
	if err == nil {
		t.Fatal("Invoke should fail closed when the upstream returns no session id")
	}
}

func TestResolveUpstreamConfig(t *testing.T) {
	resolve := func(ref string) (string, error) {
		if ref == "secret://monday-token" {
			return "resolved-token", nil
		}
		return "", errNoSecret
	}
	cfg, err := ResolveUpstreamConfig(&edge.UpstreamServer{
		Name: "cordum.monday", Transport: "http", Endpoint: "https://mcp.monday.com/mcp",
		AuthSecretRef: "secret://monday-token",
	}, resolve)
	if err != nil {
		t.Fatalf("ResolveUpstreamConfig: %v", err)
	}
	if cfg.AuthHeader != "resolved-token" || cfg.Endpoint != "https://mcp.monday.com/mcp" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}

	// stdio transport is not a remote http/sse upstream -> rejected.
	if _, err := ResolveUpstreamConfig(&edge.UpstreamServer{Transport: "stdio", Command: []string{"x"}}, resolve); err == nil {
		t.Fatal("expected stdio transport to be rejected by ResolveUpstreamConfig")
	}
	// authenticated upstream with a failing resolver fails closed.
	if _, err := ResolveUpstreamConfig(&edge.UpstreamServer{Transport: "http", Endpoint: "https://x/mcp", AuthSecretRef: "secret://missing"}, resolve); err == nil {
		t.Fatal("expected unresolved secret to fail closed")
	}
}

var errNoSecret = &secretLookupError{}

type secretLookupError struct{}

func (*secretLookupError) Error() string { return "no such secret" }

func TestEnvSecretResolver(t *testing.T) {
	t.Setenv("MONDAY_API_TOKEN", "env-tok")
	resolve := EnvSecretResolver(map[string]string{"secret://monday-token": "MONDAY_API_TOKEN"})
	got, err := resolve("secret://monday-token")
	if err != nil || got != "env-tok" {
		t.Fatalf("explicit mapping resolve = %q, %v", got, err)
	}
	// convention fallback: secret://foo-bar -> FOO_BAR
	t.Setenv("FOO_BAR", "conv")
	if got, err := resolve("secret://foo-bar"); err != nil || got != "conv" {
		t.Fatalf("convention resolve = %q, %v", got, err)
	}
	// inline (non-ref) value rejected.
	if _, err := resolve("plain-token"); err == nil {
		t.Fatal("expected non-secret:// ref to be rejected")
	}
	// empty env fails closed.
	if _, err := resolve("secret://absent-var"); err == nil {
		t.Fatal("expected empty env var to fail closed")
	}
}

// TestRemoteUpstream_MondayStreamableHTTPFraming locks the exact streamable-HTTP
// framing Monday's MCP server requires (the framing the bug ticket suspected was
// wrong — it is in fact correct): initialize (protocolVersion 2024-11-05, empty
// capabilities, clientInfo) -> capture Mcp-Session-Id -> notifications/initialized
// (with that session) -> tools/list + tools/call reuse the cached session. Every
// request sends Accept: text/event-stream + Content-Type: application/json and the
// resolved Authorization token (asserted only inside the fake; never printed).
func TestRemoteUpstream_MondayStreamableHTTPFraming(t *testing.T) {
	f := &fakeUpstreamServer{framing: "sse", tools: mondayLikeTools()}
	up, _ := newTestUpstream(t, f)

	if tools := up.ListTools(context.Background()); len(tools) != 2 {
		t.Fatalf("ListTools = %d tools, want 2", len(tools))
	}
	if _, err := up.Invoke(context.Background(), ToolCallParams{
		Name: "get_board_items_page", Arguments: json.RawMessage(`{"board_id":"5097518101"}`),
	}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	wantOrder := []string{MethodInitialize, mcpInitializedMethod, MethodToolsList, MethodToolsCall}
	if strings.Join(f.methods, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("method order = %v, want %v", f.methods, wantOrder)
	}

	var ip struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    map[string]any  `json:"capabilities"`
		ClientInfo      *Implementation `json:"clientInfo"`
	}
	if err := json.Unmarshal(f.initParams, &ip); err != nil {
		t.Fatalf("decode initialize params: %v", err)
	}
	var rawInit map[string]json.RawMessage
	if err := json.Unmarshal(f.initParams, &rawInit); err != nil {
		t.Fatalf("decode raw initialize params: %v", err)
	}
	if _, ok := rawInit["capabilities"]; !ok {
		t.Fatal("initialize must include capabilities:{}; Monday rejects the field being omitted")
	}
	if ip.ProtocolVersion != mondayStreamableHTTPProtocolVersion {
		t.Fatalf("initialize protocolVersion = %q, want %q", ip.ProtocolVersion, mondayStreamableHTTPProtocolVersion)
	}
	if len(ip.Capabilities) != 0 {
		t.Fatalf("initialize capabilities must be empty, got %v", ip.Capabilities)
	}
	if ip.ClientInfo == nil || strings.TrimSpace(ip.ClientInfo.Name) == "" {
		t.Fatal("initialize must send a clientInfo with a name")
	}

	for i, m := range f.methods {
		if !strings.Contains(f.accepts[i], "text/event-stream") {
			t.Fatalf("%s Accept=%q, want it to include text/event-stream", m, f.accepts[i])
		}
		if !strings.Contains(f.contentTypes[i], "application/json") {
			t.Fatalf("%s Content-Type=%q, want application/json", m, f.contentTypes[i])
		}
		if m == MethodInitialize {
			if f.sessions[i] != "" {
				t.Fatal("initialize must not carry an Mcp-Session-Id header")
			}
		} else if f.sessions[i] != "sess-xyz" {
			t.Fatalf("%s carried session %q, want the initialize session sess-xyz", m, f.sessions[i])
		}
	}

	if f.lastAuth != "test-token-123" { // the fake test token, never a real one
		t.Fatal("resolved Authorization token was not sent to the upstream")
	}
}

// TestRemoteUpstream_ReusesSingleSessionAcrossListAndCall guards the Monday-throttle
// regression: 2x ListTools + 1x Invoke must perform exactly ONE initialize and reuse
// the cached session — accidental per-call initialization is what trips Monday's
// per-token initialize rate limit (HTTP 400).
func TestRemoteUpstream_ReusesSingleSessionAcrossListAndCall(t *testing.T) {
	f := &fakeUpstreamServer{framing: "sse", tools: mondayLikeTools()}
	up, _ := newTestUpstream(t, f)

	_ = up.ListTools(context.Background())
	_ = up.ListTools(context.Background())
	if _, err := up.Invoke(context.Background(), ToolCallParams{Name: "all_monday_api", Arguments: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	inits := 0
	for _, m := range f.methods {
		if m == MethodInitialize {
			inits++
		}
	}
	if inits != 1 {
		t.Fatalf("initialize happened %d times across 2x ListTools + 1x Invoke, want exactly 1 (per-call init trips Monday throttle)", inits)
	}
}
