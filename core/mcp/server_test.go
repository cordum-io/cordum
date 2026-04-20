package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// testAdminIdentity returns an identity that sees every tool. Test
// messages targeting tools/list attach this so pre-filter tests
// continue to exercise the full catalogue.
func testAdminIdentity() *AgentIdentity {
	return &AgentIdentity{
		ID:                  "test-admin",
		RiskTier:            "critical",
		AllowedTools:        []string{"*"},
		DataClassifications: []string{"pii", "phi", "secrets"},
	}
}

type channelTransport struct {
	in   chan *JSONRPCMessage
	out  chan *JSONRPCMessage
	done chan struct{}
}

func newChannelTransport() *channelTransport {
	return &channelTransport{
		in:   make(chan *JSONRPCMessage, 16),
		out:  make(chan *JSONRPCMessage, 16),
		done: make(chan struct{}),
	}
}

func (t *channelTransport) ReadMessage() (*JSONRPCMessage, error) {
	select {
	case <-t.done:
		return nil, ErrTransportClosed
	case msg, ok := <-t.in:
		if !ok {
			return nil, ErrTransportClosed
		}
		return msg, nil
	}
}

func (t *channelTransport) WriteMessage(msg *JSONRPCMessage) error {
	select {
	case <-t.done:
		return ErrTransportClosed
	case t.out <- msg:
		return nil
	}
}

func (t *channelTransport) Close() error {
	select {
	case <-t.done:
		return nil
	default:
		close(t.done)
		close(t.in)
		return nil
	}
}

type parseErrorTransport struct {
	writes chan *JSONRPCMessage
	reads  int
}

func (t *parseErrorTransport) ReadMessage() (*JSONRPCMessage, error) {
	if t.reads == 0 {
		t.reads++
		return nil, fmt.Errorf("%w: bad json", ErrInvalidMessage)
	}
	return nil, ErrTransportClosed
}

func (t *parseErrorTransport) WriteMessage(msg *JSONRPCMessage) error {
	t.writes <- msg
	return nil
}

func (t *parseErrorTransport) Close() error { return nil }

func TestInitializeHandshake(t *testing.T) {
	t.Parallel()
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{
		Name:            "cordum",
		Version:         "test",
		ProtocolVersion: DefaultProtocolVersion,
		RequestTimeout:  2 * time.Second,
	})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  MethodInitialize,
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var initRes InitializeResult
	decodeResult(t, resp.Result, &initRes)
	if initRes.ProtocolVersion != DefaultProtocolVersion {
		t.Fatalf("unexpected protocol version: %q", initRes.ProtocolVersion)
	}
	if initRes.ServerInfo.Name != "cordum" {
		t.Fatalf("unexpected server name: %q", initRes.ServerInfo.Name)
	}
	closeServer(t, transport, errCh)
}

func TestToolsList(t *testing.T) {
	t.Parallel()
	tools := NewToolRegistry()
	if err := tools.Register(Tool{Name: "jobs.submit", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	transport := newChannelTransport()
	srv := NewServer(transport, tools, NewResourceRegistry(), ServerConfig{})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC:  JSONRPCVersion,
		ID:       json.RawMessage(`"tools-list"`),
		Method:   MethodToolsList,
		identity: testAdminIdentity(),
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var list ToolListResult
	decodeResult(t, resp.Result, &list)
	if len(list.Tools) != 1 || list.Tools[0].Name != "jobs.submit" {
		t.Fatalf("unexpected tool list: %+v", list.Tools)
	}
	closeServer(t, transport, errCh)
}

func TestToolsCall(t *testing.T) {
	t.Parallel()
	tools := NewToolRegistry()
	if err := tools.Register(
		Tool{
			Name:        "jobs.submit",
			InputSchema: map[string]any{"type": "object", "required": []any{"topic"}},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			var payload map[string]any
			if err := json.Unmarshal(params, &payload); err != nil {
				return nil, err
			}
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "submitted"}},
				StructuredContent: map[string]any{
					"topic": payload["topic"],
				},
			}, nil
		},
	); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	transport := newChannelTransport()
	srv := NewServer(transport, tools, NewResourceRegistry(), ServerConfig{})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`2`),
		Method:  MethodToolsCall,
		Params:  json.RawMessage(`{"name":"jobs.submit","arguments":{"topic":"job.echo"}}`),
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var callRes ToolCallResult
	decodeResult(t, resp.Result, &callRes)
	if len(callRes.Content) != 1 || callRes.Content[0].Text != "submitted" {
		t.Fatalf("unexpected tool result: %+v", callRes)
	}
	closeServer(t, transport, errCh)
}

func TestResourcesListAndRead(t *testing.T) {
	t.Parallel()
	resources := NewResourceRegistry()
	if err := resources.Register(Resource{
		URI:      "cordum://status",
		Name:     "status",
		MIMEType: "application/json",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri, MIMEType: "application/json", Text: `{"ok":true}`}, nil
	}); err != nil {
		t.Fatalf("register resource: %v", err)
	}

	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), resources, ServerConfig{})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`3`),
		Method:  MethodResourcesList,
	}
	listResp := awaitResponse(t, transport.out)
	if listResp.Error != nil {
		t.Fatalf("unexpected list error: %+v", listResp.Error)
	}
	var list ResourceListResult
	decodeResult(t, listResp.Result, &list)
	if len(list.Resources) != 1 || list.Resources[0].Name != "status" {
		t.Fatalf("unexpected resources list: %+v", list.Resources)
	}

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`4`),
		Method:  MethodResourcesRead,
		Params:  json.RawMessage(`{"uri":"cordum://status"}`),
	}
	readResp := awaitResponse(t, transport.out)
	if readResp.Error != nil {
		t.Fatalf("unexpected read error: %+v", readResp.Error)
	}
	var readRes ResourceReadResult
	decodeResult(t, readResp.Result, &readRes)
	if len(readRes.Contents) != 1 || readRes.Contents[0].URI != "cordum://status" {
		t.Fatalf("unexpected resource read result: %+v", readRes.Contents)
	}
	closeServer(t, transport, errCh)
}

func TestUnknownMethod(t *testing.T) {
	t.Parallel()
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`5`),
		Method:  "unknown/method",
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected method-not-found error, got %+v", resp.Error)
	}
	closeServer(t, transport, errCh)
}

func TestInvalidJSONReturnsParseError(t *testing.T) {
	t.Parallel()
	transport := &parseErrorTransport{
		writes: make(chan *JSONRPCMessage, 1),
	}
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{})
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve returned error: %v", err)
	}
	select {
	case resp := <-transport.writes:
		if resp == nil || resp.Error == nil {
			t.Fatalf("expected parse error response, got %+v", resp)
		}
		if resp.Error.Code != -32700 {
			t.Fatalf("expected parse error code -32700, got %d", resp.Error.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for parse error response")
	}
}

func TestReloadConfigAppliesToRegistries(t *testing.T) {
	t.Parallel()
	tools := NewToolRegistry()
	if err := tools.Register(Tool{Name: "demo.tool"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	resources := NewResourceRegistry()
	if err := resources.Register(Resource{URI: "cordum://demo", Name: "demo.resource"}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri}, nil
	}); err != nil {
		t.Fatalf("register resource: %v", err)
	}

	srv := NewServer(newChannelTransport(), tools, resources, ServerConfig{})
	if len(tools.List()) != 1 || len(resources.List()) != 1 {
		t.Fatalf("expected registries enabled before reload")
	}

	srv.ReloadConfig(map[string]any{
		"mcp": map[string]any{
			"tools": map[string]any{
				"demo.tool": map[string]any{"enabled": false},
			},
			"resources": map[string]any{
				"demo.resource": map[string]any{"enabled": false},
			},
		},
	})

	if got := len(tools.List()); got != 0 {
		t.Fatalf("expected tool disabled after reload, got %d", got)
	}
	if got := len(resources.List()); got != 0 {
		t.Fatalf("expected resource disabled after reload, got %d", got)
	}
}

// TestToolsCallScopeFilter_DenialSubReasons asserts the server maps
// each filter rejection to JSON-RPC code -32098 with the specific
// sub_reason preserved in error.data.
func TestToolsCallScopeFilter_DenialSubReasons(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("handler must not run when filter denies")
		return nil, nil
	}

	type denialCase struct {
		name       string
		identity   *AgentIdentity
		tool       Tool
		wantReason DenyReason
	}
	cases := []denialCase{
		{
			name: "tool_not_in_allowed_list",
			identity: &AgentIdentity{
				ID:           "under",
				RiskTier:     "critical",
				AllowedTools: []string{"fs.*"},
			},
			tool:       Tool{Name: "jobs.delete", RiskTier: "low"},
			wantReason: DenyReasonNotInAllowedList,
		},
		{
			name: "risk_tier_too_low",
			identity: &AgentIdentity{
				ID:           "low-tier",
				RiskTier:     "low",
				AllowedTools: []string{"*"},
			},
			tool:       Tool{Name: "jobs.delete", RiskTier: "critical"},
			wantReason: DenyReasonRiskTierTooLow,
		},
		{
			name: "missing_data_classification",
			identity: &AgentIdentity{
				ID:                  "no-pii",
				RiskTier:            "critical",
				AllowedTools:        []string{"*"},
				DataClassifications: nil,
			},
			tool:       Tool{Name: "pii.read", RiskTier: "low", DataClassifications: []string{"pii"}},
			wantReason: DenyReasonMissingDataClassification,
		},
		{
			name:       "no_identity",
			identity:   nil,
			tool:       Tool{Name: "fs.read", RiskTier: "low"},
			wantReason: DenyReasonNoIdentity,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tools := NewToolRegistry()
			tools.SetScopeEnforcement(true)
			recorded := make(chan DenyEvent, 1)
			tools.SetDenyAuditor(denyAuditorFunc(func(_ context.Context, ev DenyEvent) {
				recorded <- ev
			}))
			if err := tools.Register(tc.tool, handler); err != nil {
				t.Fatalf("register: %v", err)
			}

			transport := newChannelTransport()
			srv := NewServer(transport, tools, NewResourceRegistry(), ServerConfig{})
			errCh := startServer(t, srv, transport)

			params := json.RawMessage(`{"name":"` + tc.tool.Name + `","arguments":{}}`)
			transport.in <- &JSONRPCMessage{
				JSONRPC:  JSONRPCVersion,
				ID:       json.RawMessage(`10`),
				Method:   MethodToolsCall,
				Params:   params,
				identity: tc.identity,
			}
			resp := awaitResponse(t, transport.out)
			if resp.Error == nil {
				t.Fatalf("expected rpc error, got result %+v", resp.Result)
			}
			if resp.Error.Code != jsonRPCNotAuthorizedCode {
				t.Errorf("error.code = %d, want %d", resp.Error.Code, jsonRPCNotAuthorizedCode)
			}
			// error.data may be *NotAuthorized in-process or a map after
			// JSON round-trip. Accept both.
			var reason DenyReason
			switch d := resp.Error.Data.(type) {
			case *NotAuthorized:
				reason = d.SubReason
			case map[string]any:
				reason = DenyReason(stringOf(d["sub_reason"]))
			}
			if reason != tc.wantReason {
				t.Errorf("sub_reason = %q, want %q", reason, tc.wantReason)
			}

			// Audit hook must fire with matching sub_reason.
			select {
			case ev := <-recorded:
				if ev.SubReason != tc.wantReason {
					t.Errorf("audit sub_reason = %q, want %q", ev.SubReason, tc.wantReason)
				}
				if ev.ToolName != tc.tool.Name {
					t.Errorf("audit tool_name = %q, want %q", ev.ToolName, tc.tool.Name)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for DenyAuditor")
			}

			closeServer(t, transport, errCh)
		})
	}
}

type denyAuditorFunc func(ctx context.Context, ev DenyEvent)

func (f denyAuditorFunc) ToolDenied(ctx context.Context, ev DenyEvent) { f(ctx, ev) }

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestToolsCallApprovalRequiredMapsTo32099 is the end-to-end server
// check for step 4: a tool with RequiresApproval=true and a gate that
// returns ApprovalRequired must surface on the transport as a JSON-RPC
// error with code=-32099 and data.approval_id populated.
func TestToolsCallApprovalRequiredMapsTo32099(t *testing.T) {
	t.Parallel()
	tools := NewToolRegistry()
	if err := tools.Register(Tool{Name: "dangerous.delete", RequiresApproval: true},
		func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
			t.Fatal("handler must not execute when gate blocks")
			return nil, nil
		}); err != nil {
		t.Fatalf("register: %v", err)
	}
	tools.SetApprovalGate(&fakeGate{response: &ApprovalRequired{ApprovalID: "app-42", Reason: "dangerous rule matched"}})

	transport := newChannelTransport()
	srv := NewServer(transport, tools, NewResourceRegistry(), ServerConfig{})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`7`),
		Method:  MethodToolsCall,
		Params:  json.RawMessage(`{"name":"dangerous.delete","arguments":{}}`),
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error == nil {
		t.Fatalf("expected JSON-RPC error, got result %+v", resp.Result)
	}
	if resp.Error.Code != jsonRPCApprovalRequiredCode {
		t.Errorf("error.code = %d, want %d", resp.Error.Code, jsonRPCApprovalRequiredCode)
	}
	// error.data should carry the ApprovalRequired struct — either as
	// the struct itself (in-process) or as a decoded map after JSON
	// round-trip. Accept both shapes.
	var approvalID, toolName string
	switch d := resp.Error.Data.(type) {
	case *ApprovalRequired:
		approvalID = d.ApprovalID
		toolName = d.Tool
	case map[string]any:
		if v, ok := d["approval_id"].(string); ok {
			approvalID = v
		}
		if v, ok := d["tool"].(string); ok {
			toolName = v
		}
	default:
		t.Fatalf("error.data has unexpected type %T: %#v", resp.Error.Data, resp.Error.Data)
	}
	if approvalID != "app-42" {
		t.Errorf("approval_id = %q, want app-42", approvalID)
	}
	if toolName != "dangerous.delete" {
		t.Errorf("tool = %q, want dangerous.delete", toolName)
	}
	closeServer(t, transport, errCh)
}

func startServer(t *testing.T, srv *MCPServer, transport *channelTransport) chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()
	return errCh
}

func closeServer(t *testing.T, transport *channelTransport, errCh chan error) {
	t.Helper()
	if err := transport.Close(); err != nil {
		t.Fatalf("close transport: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server shutdown")
	}
}

func awaitResponse(t *testing.T, out <-chan *JSONRPCMessage) *JSONRPCMessage {
	t.Helper()
	select {
	case msg := <-out:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response")
		return nil
	}
}

func decodeResult(t *testing.T, src any, dst any) {
	t.Helper()
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}
