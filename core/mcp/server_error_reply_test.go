package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

func TestMCPServer_ArtifactPutFailureRepliesBareInternalError(t *testing.T) {
	t.Parallel()
	tools := &fakeToolService{}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, &fakeEventEmitter{}, &fakeArtifactStore{
		err: errors.New("artifact bucket for execution exec_99 is full"),
	})
	srv := NewServer(nil, tools, nil, ServerConfig{}).WithPolicyGate("local-fs", deps)
	// Low-entropy text survives redaction and exceeds the inline artifact limit.
	args := json.RawMessage(`{"text":"` + strings.Repeat("a", edge.MaxInputRedactedBytes+1) + `"}`)
	if len(args) <= edge.MaxInputRedactedBytes || len(args) >= MaxToolCallArgsBytes {
		t.Fatalf("unexpected argument size: %d", len(args))
	}
	params, err := json.Marshal(ToolCallParams{Name: "fs.read_file", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := srv.handleToolsCall(newAuthedToolCallCtx(), params)
	reply := &JSONRPCMessage{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`), Error: rpcErr}
	raw := requireBareInternalReply(t, reply)
	if result != nil || tools.called != 0 {
		t.Fatalf("result=%+v tool calls=%d; reply=%s", result, tools.called, raw)
	}
	if strings.Contains(raw, "artifact bucket") || strings.Contains(raw, "exec_99") {
		t.Fatalf("unexpected internal detail in reply: %s", raw)
	}
}

func requireBareInternalReply(t *testing.T, reply *JSONRPCMessage) string {
	t.Helper()
	raw, err := json.Marshal(reply)
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	if reply == nil || reply.Error == nil || reply.Error.Code != -32603 ||
		reply.Error.Message != "internal error" || reply.Error.Data != nil || reply.Result != nil {
		t.Fatalf("want bare -32603 internal error, got %s", raw)
	}
	return string(raw)
}

func TestServe_QueuedIdenticalCallNeverGetsAPanicOutcome(t *testing.T) {
	t.Parallel()
	tools := &queuedPanicReplyTool{started: make(chan struct{}), release: make(chan struct{})}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, &fakeEventEmitter{}, &fakeArtifactStore{})
	deps.DedupeState = NewInProcessDedupeStore()
	transport := newChannelTransport()
	srv := NewServer(transport, tools, nil, ServerConfig{RequestTimeout: 10 * time.Second}).WithPolicyGate("local-fs", deps)
	errCh := startServer(t, srv, transport)
	var release sync.Once
	unblock := func() { release.Do(func() { close(tools.release) }) }
	t.Cleanup(func() {
		unblock()
		closeServer(t, transport, errCh)
	})
	sendQueuedReplyCall(t, transport, "1")
	select {
	case <-tools.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first tool call did not start")
	}
	// Both requests carry the same metadata and arguments, hence the same dedupe key.
	sendQueuedReplyCall(t, transport, "2")
	unblock()
	first := awaitResponse(t, transport.out)
	firstJSON := requireBareInternalReply(t, first)
	second := awaitResponse(t, transport.out)
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second reply: %v", err)
	}
	if second == nil || second.Error != nil || second.Result == nil || string(first.ID) != "1" || string(second.ID) != "2" {
		t.Fatalf("want error then result for IDs 1 and 2; replies=%s, %s", firstJSON, secondJSON)
	}
	var result ToolCallResult
	decodeResult(t, second.Result, &result)
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != "ok" || tools.called.Load() != 2 {
		t.Fatalf("want two tool calls and text result; calls=%d replies=%s, %s", tools.called.Load(), firstJSON, secondJSON)
	}
	for _, raw := range []string{firstJSON, string(secondJSON)} {
		if strings.Contains(raw, "exploded") || strings.Contains(raw, "panic") || strings.Contains(raw, "exec_99") {
			t.Fatalf("unexpected internal detail in reply: %s", raw)
		}
	}
}

type queuedPanicReplyTool struct {
	called  atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (f *queuedPanicReplyTool) ListTools(context.Context) []Tool { return nil }

func (f *queuedPanicReplyTool) Call(ctx context.Context, _ string, _ json.RawMessage) (*ToolCallResult, error) {
	if f.called.Add(1) == 1 {
		close(f.started)
		select {
		case <-f.release:
			panic("edge execution exec_99 exploded")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
}

func sendQueuedReplyCall(t *testing.T, transport *channelTransport, id string) {
	t.Helper()
	msg := &JSONRPCMessage{
		JSONRPC:    JSONRPCVersion,
		ID:         json.RawMessage(id),
		Method:     MethodToolsCall,
		Params:     json.RawMessage(`{"name":"fs.read_file","arguments":{"path":"/etc/hostname"}}`),
		requestCtx: newAuthedToolCallCtx(),
	}
	select {
	case transport.in <- msg:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out queueing call %s", id)
	}
}
