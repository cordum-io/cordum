package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPromptsList_DispatchedThroughServer(t *testing.T) {
	t.Parallel()
	prompts := NewPromptRegistry()
	if err := RegisterAllPrompts(prompts); err != nil {
		t.Fatalf("register: %v", err)
	}
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{
		Name: "cordum", Version: "test", ProtocolVersion: DefaultProtocolVersion, RequestTimeout: 2 * time.Second,
	}).WithPrompts(prompts)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  MethodPromptsList,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result PromptListResult
	decodeResult(t, resp.Result, &result)
	if len(result.Prompts) != 4 {
		t.Fatalf("expected 4 prompts, got %d", len(result.Prompts))
	}
	closeServer(t, transport, errCh)
}

func TestPromptsList_EmptyWhenServiceMissing(t *testing.T) {
	t.Parallel()
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  MethodPromptsList,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result PromptListResult
	decodeResult(t, resp.Result, &result)
	if len(result.Prompts) != 0 {
		t.Fatalf("expected empty list, got %+v", result.Prompts)
	}
	closeServer(t, transport, errCh)
}

func TestPromptsGet_RendersTemplate(t *testing.T) {
	t.Parallel()
	prompts := NewPromptRegistry()
	_ = RegisterAllPrompts(prompts)
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{}).WithPrompts(prompts)
	errCh := startServer(t, srv, transport)

	params, _ := json.Marshal(PromptGetParams{
		Name:      draftSafetyRulePromptName,
		Arguments: map[string]string{"scenario": "block PII writes", "risk_level": "high"},
	})
	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  MethodPromptsGet,
		Params:  params,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result PromptGetResult
	decodeResult(t, resp.Result, &result)
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if !strings.Contains(result.Messages[0].Content.Text, "Cordum safety-policy author") {
		t.Fatal("system message lost between dispatch and serialisation")
	}
	closeServer(t, transport, errCh)
}

func TestPromptsGet_ReturnsErrorOnUnknown(t *testing.T) {
	t.Parallel()
	prompts := NewPromptRegistry()
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{}).WithPrompts(prompts)
	errCh := startServer(t, srv, transport)

	params, _ := json.Marshal(PromptGetParams{Name: "missing"})
	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`),
		Method: MethodPromptsGet, Params: params,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error == nil {
		t.Fatal("expected error for unknown prompt")
	}
	if resp.Error.Code != jsonRPCMethodNotFoundCode {
		t.Fatalf("expected method-not-found, got %d", resp.Error.Code)
	}
	closeServer(t, transport, errCh)
}

func TestInitialize_AdvertisesPromptsCapabilityWhenServiceWired(t *testing.T) {
	t.Parallel()
	prompts := NewPromptRegistry()
	_ = RegisterAllPrompts(prompts)
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{
		Name: "cordum", Version: "test", ProtocolVersion: DefaultProtocolVersion,
	}).WithPrompts(prompts)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`),
		Method: MethodInitialize,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("init error: %+v", resp.Error)
	}
	var init InitializeResult
	decodeResult(t, resp.Result, &init)
	if init.Capabilities.Prompts == nil {
		t.Fatal("expected prompts capability advertised")
	}
	if !init.Capabilities.Prompts.ListChanged {
		t.Fatal("expected prompts.listChanged=true")
	}
	closeServer(t, transport, errCh)
}

func TestInitialize_NoPromptsCapabilityWhenServiceMissing(t *testing.T) {
	t.Parallel()
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{
		Name: "cordum", Version: "test", ProtocolVersion: DefaultProtocolVersion,
	})
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`),
		Method: MethodInitialize,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("init error: %+v", resp.Error)
	}
	var init InitializeResult
	decodeResult(t, resp.Result, &init)
	if init.Capabilities.Prompts != nil {
		t.Fatal("expected no prompts capability when service missing")
	}
	closeServer(t, transport, errCh)
}

func TestPromptsGet_ContextFlowsToFetcher(t *testing.T) {
	t.Parallel()
	prompts := NewPromptRegistry()
	_ = RegisterAllPrompts(prompts)
	called := false
	fetcher := func(_ context.Context, _ string) (DenialContext, error) {
		called = true
		return DenialContext{Decision: "deny", RuleID: "r-1", Reason: "test"}, nil
	}
	transport := newChannelTransport()
	srv := NewServer(transport, NewToolRegistry(), NewResourceRegistry(), ServerConfig{}).WithPrompts(prompts)
	errCh := startServer(t, srv, transport)

	params, _ := json.Marshal(PromptGetParams{Name: explainDenialPromptName, Arguments: map[string]string{"job_id": "j-7"}})
	msg := &JSONRPCMessage{
		JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`),
		Method: MethodPromptsGet, Params: params,
		requestCtx: WithDenialContextFetcher(context.Background(), fetcher),
	}
	transport.in <- msg
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !called {
		t.Fatal("fetcher not invoked — request context did not flow into renderer")
	}
	closeServer(t, transport, errCh)
}
