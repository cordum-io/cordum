package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFailureBehaviorApprovalStoreOutageLeavesUngatedToolsUnaffected(t *testing.T) {
	registry := NewToolRegistry()
	ungated, ungatedCalls := approvalGateCountingHandler()
	gated, gatedCalls := approvalGateCountingHandler()
	require.NoError(t, registry.Register(Tool{Name: "failure.read"}, ungated))
	require.NoError(t, registry.Register(Tool{Name: "failure.write", RequiresApproval: true}, gated))
	gateCalls := 0
	outage := errors.New("approval store unavailable")
	registry.SetApprovalGate(approvalGateFunc(func(context.Context, Tool, json.RawMessage) (*ApprovalRequired, error) {
		gateCalls++
		return nil, outage
	}))

	ctx := context.Background()
	result, err := registry.Call(ctx, "failure.read", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *ungatedCalls)
	require.Zero(t, gateCalls)

	result, err = registry.Call(ctx, "failure.write", json.RawMessage(`{}`))
	require.ErrorIs(t, err, outage)
	require.Nil(t, result)
	require.Equal(t, 1, gateCalls)
	require.Zero(t, *gatedCalls)

	// Recovery proves that the gated handler's counter records a real call.
	registry.SetApprovalGate(approvalGateFunc(func(context.Context, Tool, json.RawMessage) (*ApprovalRequired, error) {
		return nil, nil
	}))
	result, err = registry.Call(ctx, "failure.write", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, *gatedCalls)
}
