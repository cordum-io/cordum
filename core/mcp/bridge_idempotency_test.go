package mcp

import (
	"context"
	"net/http"
	"testing"
)

// Every mutating bridge method MUST forward IdempotencyKey as an
// Idempotency-Key header so the downstream gateway can dedupe retries
// after a human approval. Gateway-side dedupe already exists for the
// workflow-run path (handlers_workflows.go TrySetRunIdempotencyKey);
// other endpoints can add it incrementally without breaking this
// contract. The contract for THIS task is: 'the MCP client never
// holds the duplicate-prevention state; the gateway is the source of
// truth.' All we verify here is that the key reaches the gateway.

func TestMutatingBridge_ForwardsIdempotencyHeader_AllMethods(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		path   string
		method string
		invoke func(b *HTTPServiceBridge) error
	}{
		{
			name:   "CreateWorkflow",
			path:   "/api/v1/workflows",
			method: http.MethodPost,
			invoke: func(b *HTTPServiceBridge) error {
				_, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{
					Steps:          map[string]any{"log": map[string]any{"type": "log"}},
					IdempotencyKey: "idem-cw",
				})
				return err
			},
		},
		{
			name:   "InstallPack",
			path:   "/api/v1/packs/install",
			method: http.MethodPost,
			invoke: func(b *HTTPServiceBridge) error {
				_, err := b.InstallPack(context.Background(), InstallPackInput{
					PackID:         "cordum/slack",
					IdempotencyKey: "idem-ip",
				})
				return err
			},
		},
		{
			name:   "UninstallPack",
			path:   "/api/v1/packs/cordum%2Fslack/uninstall",
			method: http.MethodPost,
			invoke: func(b *HTTPServiceBridge) error {
				return b.UninstallPack(context.Background(), UninstallPackInput{
					PackID:         "cordum/slack",
					IdempotencyKey: "idem-up",
				})
			},
		},
		{
			name:   "RegisterAgent",
			path:   "/api/v1/agents",
			method: http.MethodPost,
			invoke: func(b *HTTPServiceBridge) error {
				_, err := b.RegisterAgent(context.Background(), RegisterAgentInput{
					Name:           "agt-x",
					Owner:          "acme",
					RiskTier:       "low",
					IdempotencyKey: "idem-ra",
				})
				return err
			},
		},
		{
			name:   "UpdatePolicyBundle",
			path:   "/api/v1/policy/bundles/secops",
			method: http.MethodPut,
			invoke: func(b *HTTPServiceBridge) error {
				_, err := b.UpdatePolicyBundle(context.Background(), UpdatePolicyBundleInput{
					BundleID:       "secops",
					Content:        "rules: []",
					IdempotencyKey: "idem-pb",
				})
				return err
			},
		},
		{
			name:   "RevokeWorkerSession",
			path:   "/api/v1/workers/w1/revoke-session",
			method: http.MethodPost,
			invoke: func(b *HTTPServiceBridge) error {
				return b.RevokeWorkerSession(context.Background(), RevokeWorkerSessionInput{
					WorkerID:       "w1",
					IdempotencyKey: "idem-rw",
				})
			},
		},
		{
			name:   "SetAgentScope",
			path:   "/api/v1/agents/agt-x",
			method: http.MethodPut,
			invoke: func(b *HTTPServiceBridge) error {
				_, err := b.SetAgentScope(context.Background(), SetAgentScopeInput{
					AgentID:        "agt-x",
					AllowedTools:   []string{"x"},
					IdempotencyKey: "idem-sa",
				})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
				"id":          "x",
				"workflow_id": "x",
				"pack_id":     "cordum/slack",
				"installed":   true,
				"bundle_id":   "secops",
			})
			b := mutatingBridge(t, srv)
			if err := tc.invoke(b); err != nil {
				t.Fatalf("invoke %s: %v", tc.name, err)
			}
			if captured.method != tc.method {
				t.Errorf("%s: method = %s, want %s", tc.name, captured.method, tc.method)
			}
			if got := captured.header.Get("Idempotency-Key"); got == "" {
				t.Errorf("%s: Idempotency-Key header missing", tc.name)
			}
		})
	}
}

// When no idempotency_key is provided, the header must NOT be set —
// empty strings would reserve a cache slot the gateway can never
// match against later.
func TestMutatingBridge_OmitsIdempotencyHeader_WhenArgUnset(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{"id": "w"})
	b := mutatingBridge(t, srv)
	if _, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{
		Steps: map[string]any{"log": map[string]any{"type": "log"}},
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := captured.header.Get("Idempotency-Key"); got != "" {
		t.Errorf("Idempotency-Key must be absent when arg unset, got %q", got)
	}
}
