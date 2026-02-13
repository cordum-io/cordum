package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

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
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`"tools-list"`),
		Method:  MethodToolsList,
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
