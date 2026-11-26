package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/edge/claude"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// These tests change authority while an Edge approval wait is in progress and
// prove the tool does not start and the approval stays unconsumed. The repro's
// waitAuthority* helpers supply the agentd evaluator, the hook and the tool.

func newWaitAuthoritySafety() *edgeEvaluateStubSafetyClient {
	return &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, Reason: "approval required", ApprovalRequired: true,
	}}
}

// waitAuthorityApproveAfter waits for the session's pending approval, runs
// during, then approves it.
func waitAuthorityApproveAfter(ctx context.Context, s *server, sessionID string, during func() error) error {
	for {
		page, err := s.edgeStore.ListApprovals(ctx, edgecore.ListApprovalsQuery{
			TenantID: edgeRouteTenant, SessionID: sessionID, Status: edgecore.ApprovalStatusPending, Limit: 10,
		})
		if err != nil {
			return err
		}
		if len(page.Items) == 1 {
			if err := during(); err != nil {
				return err
			}
			_, err := s.edgeStore.ApproveApproval(ctx, edgecore.ApprovalResolution{
				TenantID: edgeRouteTenant, ApprovalRef: page.Items[0].ApprovalRef,
				ResolverID: "reviewer", ResolvedBy: "reviewer", ResolvedAt: time.Now().UTC(),
			})
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pending approval not observed: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitAuthorityHookAfter runs one hook call through agentd's inline approval
// wait, applies during while the approval is pending, and returns the tool marker.
func waitAuthorityHookAfter(t *testing.T, s *server, handler http.Handler, session edgeSessionCreateResponseJSON, during func() error) string {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resolved := make(chan error, 1)
	go func() { resolved <- waitAuthorityApproveAfter(ctx, s, session.SessionID, during) }()
	out := waitAuthorityHook(t, waitAuthorityEvaluator(t, httpServer.URL, session), session, "echo reviewed-action")
	if err := <-resolved; err != nil {
		t.Fatalf("resolution setup failed: %v", err)
	}
	return waitAuthorityRunTool(t, out)
}

// assertWaitAuthorityOutcome checks the tool marker and the approval's consume
// state agree with whether the action was meant to run.
func assertWaitAuthorityOutcome(t *testing.T, store edgecore.Store, sessionID, marker string, wantRun bool) {
	t.Helper()
	_, statErr := os.Stat(marker)
	if wantRun && statErr != nil {
		t.Fatalf("tool did not start: %v", statErr)
	}
	if !wantRun && !os.IsNotExist(statErr) {
		t.Fatalf("tool must not start; stat=%v", statErr)
	}
	page, err := store.ListApprovals(context.Background(), edgecore.ListApprovalsQuery{
		TenantID: edgeRouteTenant, SessionID: sessionID, Status: edgecore.ApprovalStatusApproved, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("approved approvals = %d, err=%v; want 1", len(page.Items), err)
	}
	if consumed := page.Items[0].ConsumedAt != nil; consumed != wantRun {
		t.Fatalf("approval consumed = %v, want %v", consumed, wantRun)
	}
}

func TestEdgeEvaluateRetryWaitPolicyDenyWins(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deny     bool
		wantRun  bool
		decision edgecore.EdgeDecision
	}{
		{name: "unchanged", wantRun: true, decision: edgecore.DecisionAllow},
		{name: "policy_deny", deny: true, decision: edgecore.DecisionDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			safety := newWaitAuthoritySafety()
			s, handler := newEdgeEvaluateTestServer(t, safety)
			session := createEdgeRouteSession(t, handler)
			safety.response.PolicySnapshot = session.PolicySnapshot
			cmd := map[string]any{"command": "echo reviewed-action"}
			var pending edgeEvaluateResponseJSON
			decodeEdgeRouteJSON(t, edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
				edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd)), &pending)
			if pending.Decision != string(edgecore.DecisionRequireApproval) || pending.ApprovalRef == "" {
				t.Fatalf("first evaluate = %+v, want a pending approval", pending)
			}
			// The retry evaluates policy before its wait; the deny applies only
			// after that evaluation, while the retry waits.
			denyDuringWait := func() error {
				for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					safety.mu.Lock()
					seen := len(safety.requests)
					if seen >= 2 && tc.deny {
						safety.response = &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY,
							Reason: "action is no longer permitted", PolicySnapshot: session.PolicySnapshot}
					}
					safety.mu.Unlock()
					if seen >= 2 {
						return nil
					}
				}
				return errors.New("retry policy evaluation not observed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resolved := make(chan error, 1)
			go func() { resolved <- waitAuthorityApproveAfter(ctx, s, session.SessionID, denyDuringWait) }()
			rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBodyWithInlineWait(
				session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", cmd, pending.ApprovalRef, 2000))
			if err := <-resolved; err != nil {
				t.Fatalf("resolution setup failed: %v", err)
			}
			var resp agentd.EvaluateResponse
			decodeEdgeRouteJSON(t, rr, &resp)
			if resp.Decision != string(tc.decision) {
				t.Fatalf("decision = %q, want %q; body=%s", resp.Decision, tc.decision, rr.Body.String())
			}
			decision := agentd.AgentdDecisionFromEvaluateResponse(context.Background(), resp, agentd.ApprovalDecisionConfig{}, nil, nil)
			marker := waitAuthorityRunTool(t, claude.ClaudeHookOutputForDecision("PreToolUse", decision))
			assertWaitAuthorityOutcome(t, s.edgeStore, session.SessionID, marker, tc.wantRun)
		})
	}
}

// waitAuthoritySessionEndingStore ends the session at the moment the approval
// is claimed, so the claim itself is where the ended session must be refused.
type waitAuthoritySessionEndingStore struct {
	edgecore.Store
	claimed atomic.Bool
}

func (s *waitAuthoritySessionEndingStore) ClaimApproval(ctx context.Context, req edgecore.ApprovalClaimRequest) (*edgecore.EdgeApproval, bool, error) {
	s.claimed.Store(true)
	if _, err := s.EndSession(ctx, req.TenantID, req.SessionID, time.Now().UTC(), edgecore.SessionStatusEnded); err != nil {
		return nil, false, err
	}
	return s.Store.ClaimApproval(ctx, req)
}

func TestEdgeEvaluateWaitSessionEndedConsumeFails(t *testing.T) {
	safety := newWaitAuthoritySafety()
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot
	store := &waitAuthoritySessionEndingStore{Store: s.edgeStore}
	s.edgeStore = store
	marker := waitAuthorityHookAfter(t, s, handler, session, func() error { return nil })
	if !store.claimed.Load() {
		t.Fatal("the consume never reached ClaimApproval")
	}
	assertWaitAuthorityOutcome(t, store.Store, session.SessionID, marker, false)
}

func waitAuthorityKeysFile(principals map[string]string) string {
	entries := "["
	for key, principal := range principals {
		if entries != "[" {
			entries += ","
		}
		entries += `{"key":"` + key + `","tenant":"` + edgeRouteTenant + `","role":"admin","principal_id":"` + principal + `"}`
	}
	return entries + "]"
}

func TestEdgeEvaluateWaitRevokedPrincipalConsumeFails(t *testing.T) {
	reviewerOnly := map[string]string{edgeRouteReviewerAPIKey: "principal-reviewer"}
	for _, tc := range []struct {
		name    string
		revoke  bool
		wantRun bool
	}{
		{name: "unchanged", wantRun: true},
		{name: "key_removed", revoke: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keysPath := filepath.Join(t.TempDir(), "api-keys.json")
			all := map[string]string{edgeRouteTestAPIKey: "principal-edge-a", edgeRouteReviewerAPIKey: "principal-reviewer"}
			if err := os.WriteFile(keysPath, []byte(waitAuthorityKeysFile(all)), 0o600); err != nil {
				t.Fatal(err)
			}
			safety := newWaitAuthoritySafety()
			s, _, _ := newTestGateway(t)
			s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
			s.safetyClient = safety
			s.auth = newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS_PATH": keysPath})
			mux := http.NewServeMux()
			if err := s.registerRoutes(mux); err != nil {
				t.Fatalf("register routes: %v", err)
			}
			handler := apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
			session := createEdgeRouteSession(t, handler)
			safety.response.PolicySnapshot = session.PolicySnapshot
			marker := waitAuthorityHookAfter(t, s, handler, session, func() error {
				if !tc.revoke {
					return nil
				}
				if err := os.WriteFile(keysPath, []byte(waitAuthorityKeysFile(reviewerOnly)), 0o600); err != nil {
					return err
				}
				// The provider reloads the file only when its mtime moves forward.
				later := time.Now().Add(time.Minute)
				return os.Chtimes(keysPath, later, later)
			})
			assertWaitAuthorityOutcome(t, s.edgeStore, session.SessionID, marker, tc.wantRun)
		})
	}
}
