package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// firstWinsJSON decodes raw keeping the FIRST value of a repeated object key,
// as a fronted upstream's JSON parser may. encoding/json keeps the last one.
func firstWinsJSON(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func() (any, error)
	walk = func() (any, error) {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return tok, nil
		}
		if d == '[' {
			arr := []any{}
			for dec.More() {
				v, err := walk()
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
		obj := map[string]any{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := walk()
			if err != nil {
				return nil, err
			}
			if _, seen := obj[k.(string)]; !seen {
				obj[k.(string)] = v
			}
		}
		_, err = dec.Token()
		return obj, err
	}
	v, err := walk()
	if err != nil {
		t.Fatalf("first-wins decode of %s: %v", raw, err)
	}
	return v
}

// boundArgs is what the approval hash and the approver's canonical view bind
// for args: CanonicaliseArgs over the _approval_ref-stripped arguments.
func boundArgs(t *testing.T, args string) any {
	t.Helper()
	canonical, _, err := CanonicaliseArgs(stripApprovalRefArg(json.RawMessage(args)))
	if err != nil {
		t.Fatalf("canonicalise %s: %v", args, err)
	}
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode canonical %s: %v", canonical, err)
	}
	return v
}

// dupKeyFrontedServer is a policy-gated server fronting a remote upstream
// (ALLOW), optionally with the approval claim path wired to an approved claim.
func dupKeyFrontedServer(t *testing.T, withClaim bool) (*MCPServer, *fakeUpstreamServer, *fakeApprovalClaimStore) {
	t.Helper()
	f := &fakeUpstreamServer{framing: "sse", tools: mondayLikeTools()}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	up, err := NewRemoteUpstream(context.Background(), RemoteUpstreamConfig{Endpoint: srv.URL, AuthHeader: "tok", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("NewRemoteUpstream: %v", err)
	}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{decision: PolicyDecision{}}, &fakeEventEmitter{}, &fakeArtifactStore{})
	deps.Upstream = up
	server := NewServer(nil, up, nil, ServerConfig{Name: "cordum"}).WithPolicyGate("cordum.monday", deps)
	approved := time.Date(2026, 5, 16, 20, 0, 0, 0, time.UTC)
	store := &fakeApprovalClaimStore{consumed: true, approval: &edge.EdgeApproval{
		ApprovalRef: "edge_appr_xyz", TenantID: "tnt_a", Status: edge.ApprovalStatusApproved,
		Decision: edge.ApprovalDecisionApprove, ExpiresAt: &approved,
	}}
	if withClaim {
		server = server.WithApprovalHold(ApprovalHoldDeps{
			Store: store, ServerName: "cordum.monday",
			PolicySnapshot: func(context.Context) string { return "policy-v7" },
		})
	}
	return server, f, store
}

func toolsCallJSON(name, args string) json.RawMessage {
	return json.RawMessage(`{"name":"` + name + `","arguments":` + args + `}`)
}

// assertUpstreamRanBoundArgs fails when the upstream ran, read first-wins,
// with arguments other than the ones the approval hash bound.
func assertUpstreamRanBoundArgs(t *testing.T, f *fakeUpstreamServer, args string) {
	t.Helper()
	if f.toolCalls == 0 {
		return
	}
	if ran, bound := firstWinsJSON(t, f.lastArgs), boundArgs(t, args); !reflect.DeepEqual(ran, bound) {
		t.Fatalf("first-wins upstream ran %v, the approval hash and canonical view bound %v", ran, bound)
	}
}

func TestToolsCallDuplicateKey_FrontedApprovedCallRefusedBeforeHashing(t *testing.T) {
	server, f, store := dupKeyFrontedServer(t, true)
	args := `{"_approval_ref":"edge_appr_xyz","opts":{"board_id":"1","board_id":"2"}}`
	_, rpcErr := server.handleToolsCall(newAuthedToolCallCtx(), toolsCallJSON("get_board_items_page", args))
	assertUpstreamRanBoundArgs(t, f, args)
	if rpcErr == nil || rpcErr.Code != jsonRPCInvalidParamsCode {
		t.Fatalf("tools/call with a repeated nested key: rpcErr = %+v, want %d invalid params", rpcErr, jsonRPCInvalidParamsCode)
	}
	if f.toolCalls != 0 || store.calls != 0 {
		t.Fatalf("upstream calls = %d, approval claims = %d; want 0 and 0 (refused before the claim hashes the args)", f.toolCalls, store.calls)
	}
}

func TestToolsCallDuplicateKey_FrontedAllowedCallRefused(t *testing.T) {
	server, f, _ := dupKeyFrontedServer(t, false)
	args := `{"board_id":"1","board_id":"2"}`
	_, rpcErr := server.handleToolsCall(newAuthedToolCallCtx(), toolsCallJSON("get_board_items_page", args))
	assertUpstreamRanBoundArgs(t, f, args)
	if rpcErr == nil || rpcErr.Code != jsonRPCInvalidParamsCode {
		t.Fatalf("tools/call with a repeated key: rpcErr = %+v, want %d invalid params", rpcErr, jsonRPCInvalidParamsCode)
	}
	if f.toolCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0", f.toolCalls)
	}
}

func TestToolsCallDuplicateKey_BuiltinCallRefused(t *testing.T) {
	registry := NewToolRegistry()
	ran := 0
	if err := registry.Register(Tool{Name: "fs.write"}, func(context.Context, json.RawMessage) (*ToolCallResult, error) {
		ran++
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion})
	_, rpcErr := srv.handleToolsCall(newAuthedToolCallCtx(), toolsCallJSON("fs.write", `{"path":"/a","path":"/b"}`))
	if ran != 0 {
		t.Fatalf("built-in handler ran %d times with a repeated key; want the call refused first", ran)
	}
	if rpcErr == nil || rpcErr.Code != jsonRPCInvalidParamsCode {
		t.Fatalf("rpcErr = %+v, want %d invalid params", rpcErr, jsonRPCInvalidParamsCode)
	}
}
