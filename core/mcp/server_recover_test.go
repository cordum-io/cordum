package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
)

// recoverTestTools is a ToolService whose Call panics; ListTools is empty.
type recoverTestTools struct{ calls atomic.Int32 }

func (r *recoverTestTools) ListTools(context.Context) []Tool { return nil }

func (r *recoverTestTools) Call(context.Context, string, json.RawMessage) (*ToolCallResult, error) {
	r.calls.Add(1)
	panic("recover-test tool handler failure")
}

// recoverTestResources is a ResourceService whose Read panics.
type recoverTestResources struct{ calls atomic.Int32 }

func (r *recoverTestResources) List() []Resource                  { return nil }
func (r *recoverTestResources) ListTemplates() []ResourceTemplate { return nil }
func (r *recoverTestResources) Read(context.Context, string) (*ResourceContents, error) {
	r.calls.Add(1)
	panic("recover-test resource handler failure")
}

// TestServe_HandlerPanicIsAnsweredWithInternalError pins that a request
// whose tool or resource handler panics is answered with a fixed -32603,
// that each one is logged once with its stack, and that the next message
// is answered. Not parallel: it swaps the default slog logger.
func TestServe_HandlerPanicIsAnsweredWithInternalError(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tools := &recoverTestTools{}
	resources := &recoverTestResources{}
	transport := newChannelTransport()
	srv := NewServer(transport, tools, resources, ServerConfig{})
	errCh := startServer(t, srv, transport)

	ping := func(id string) {
		t.Helper()
		transport.in <- &JSONRPCMessage{JSONRPC: JSONRPCVersion, ID: json.RawMessage(id), Method: MethodPing}
		resp := awaitResponse(t, transport.out)
		if resp.Error != nil || string(resp.ID) != id {
			t.Fatalf("ping %s: id=%s error=%+v, want a result", id, resp.ID, resp.Error)
		}
	}
	requireInternal := func(resp *JSONRPCMessage, id string) {
		t.Helper()
		if string(resp.ID) != id || resp.Error == nil || resp.Result != nil {
			t.Fatalf("id=%s result=%v error=%+v, want error for id %s", resp.ID, resp.Result, resp.Error, id)
		}
		if resp.Error.Code != jsonRPCInternalErrorCode || resp.Error.Message != "internal error" || resp.Error.Data != nil {
			t.Fatalf("error=%+v, want -32603 \"internal error\" with no data", resp.Error)
		}
	}

	transport.in <- &JSONRPCMessage{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`), Method: MethodToolsCall,
		Params: json.RawMessage(`{"name":"any.tool","arguments":{}}`), identity: testAdminIdentity()}
	requireInternal(awaitResponse(t, transport.out), "1")
	ping("2")

	transport.in <- &JSONRPCMessage{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`3`), Method: MethodResourcesRead,
		Params: json.RawMessage(`{"uri":"cordum://jobs/j1"}`)}
	requireInternal(awaitResponse(t, transport.out), "3")
	ping("4")

	// A notification gets no response, even when its handler panics.
	transport.in <- &JSONRPCMessage{JSONRPC: JSONRPCVersion, Method: MethodResourcesRead,
		Params: json.RawMessage(`{"uri":"cordum://jobs/j2"}`)}
	ping("5")

	if got := tools.calls.Load(); got != 1 {
		t.Fatalf("tool handler calls = %d, want 1", got)
	}
	if got := resources.calls.Load(); got != 2 {
		t.Fatalf("resource handler calls = %d, want 2", got)
	}
	closeServer(t, transport, errCh)

	records := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(records) != 3 {
		t.Fatalf("error records = %d, want 3 (one per recovered handler); log=%s", len(records), logBuf.String())
	}
	for i, line := range records {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record %d: %v line=%s", i, err, line)
		}
		stack, _ := rec["stack"].(string)
		if rec["level"] != "ERROR" || rec["method"] == nil || !strings.Contains(stack, "goroutine ") {
			t.Fatalf("record %d = %v, want level ERROR with method and stack", i, rec)
		}
	}
}
