package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestJSONRPC_PolicySnapshotChangedWiresTo32096(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"registry_gate", "action_gate_handoff", "outranks_store_wrap"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			driftErr := fmt.Errorf("%w: policy snapshot policy-v7 changed to policy-v8 before the Edge mint", ErrPolicySnapshotChanged)
			srv := NewServer(newChannelTransport(), NewToolRegistry(), NewResourceRegistry(), ServerConfig{})
			var rpcErr *JSONRPCError
			switch path {
			case "registry_gate":
				rpcErr = snapshotChangedRegistryCall(t, driftErr)
			case "action_gate_handoff":
				rpcErr = srv.mapHandlerError(fmt.Errorf("approval handoff: %w", driftErr))
			case "outranks_store_wrap":
				rpcErr = srv.mapHandlerError(fmt.Errorf("%w: edge approval mint failed: %w", ErrApprovalStoreUnavailable, driftErr))
			}
			if rpcErr == nil {
				t.Fatal("expected policy_snapshot_changed JSON-RPC error")
			}
			if rpcErr.Code != jsonRPCApprovalLifecycleErrorCode || rpcErr.Message != "approval lifecycle error" {
				t.Errorf("error = %d %q; want -32096 approval lifecycle error", rpcErr.Code, rpcErr.Message)
			}
			want := map[string]any{"kind": "policy_snapshot_changed", "reason": policySnapshotChangedReason}
			if !reflect.DeepEqual(rpcErr.Data, want) {
				t.Errorf("error.data = %#v; want %#v", rpcErr.Data, want)
			}
			raw, err := json.Marshal(rpcErr)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			for _, snapshot := range []string{"policy-v7", "policy-v8"} {
				if strings.Contains(string(raw), snapshot) {
					t.Errorf("JSON-RPC error leaks %q: %s", snapshot, raw)
				}
			}
		})
	}
}

func snapshotChangedRegistryCall(t *testing.T, driftErr error) *JSONRPCError {
	t.Helper()
	registry := NewToolRegistry()
	calls := 0
	if err := registry.Register(Tool{Name: "fs.write", RequiresApproval: true}, func(context.Context, json.RawMessage) (*ToolCallResult, error) {
		calls++
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	registry.SetApprovalGate(approvalGateFunc(func(context.Context, Tool, json.RawMessage) (*ApprovalRequired, error) {
		return nil, driftErr
	}))
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{})
	_, rpcErr := srv.handleToolsCall(context.Background(), json.RawMessage(`{"name":"fs.write","arguments":{"path":"/etc/hostname"}}`))
	if calls != 0 {
		t.Fatalf("handler executions = %d; want 0", calls)
	}
	return rpcErr
}
