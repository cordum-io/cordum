package agentd

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

// arbitraryCodeCommands are build and test commands. They run
// repository-controlled code, so the classifier labels them
// command.class=arbitrary_code. The labels come from the real hook mapping
// through buildCommandRequest.
var arbitraryCodeCommands = []string{"npm test", "go test ./...", "make test", "pytest", "cargo build"}

const readOnlyGitCommand = "git status"

// Two evaluations of each build or test command reach the gateway twice; the
// read-only git control is served from the cache the second time.
func TestSafeAllowCacheSkipsBuildTestCommands(t *testing.T) {
	for _, command := range append([]string{readOnlyGitCommand}, arbitraryCodeCommands...) {
		t.Run(command, func(t *testing.T) {
			client := &stubEvaluateClient{resp: &EvaluateResponse{
				Decision:           string(edgecore.DecisionAllow),
				PermissionDecision: "allow",
				CacheEligible:      true,
				PolicySnapshot:     "snap-eval",
			}}
			evaluator := NewEvaluator(EvaluatorConfig{
				Client: client,
				State:  evaluatorTestState(edgecore.PolicyModeEnforce),
				Cache:  NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 8}, nil),
			})
			req := buildCommandRequest(t, command)
			for i := range 2 {
				decision, err := evaluator.EvaluateHook(context.Background(), req)
				if err != nil || decision.Decision != claude.DecisionAllow {
					t.Fatalf("call %d: decision = %+v, err = %v; want allow", i+1, decision, err)
				}
			}
			wantCalls := 2
			if command == readOnlyGitCommand {
				wantCalls = 1
			}
			if len(client.requests) != wantCalls {
				t.Fatalf("gateway evaluate calls = %d, want %d", len(client.requests), wantCalls)
			}
		})
	}
}

// In enforce mode with the gateway unreachable, each build or test command is
// denied and its process never starts; the read-only git control is allowed.
func TestFailModeDeniesBuildTestCommandsWhenGatewayUnavailable(t *testing.T) {
	unavailable := errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
	if got := ClassifyEvaluateError(unavailable); got != GatewayErrorUnavailable {
		t.Fatalf("error category = %q, want %q", got, GatewayErrorUnavailable)
	}
	for _, command := range append([]string{readOnlyGitCommand}, arbitraryCodeCommands...) {
		t.Run(command, func(t *testing.T) {
			client := &stubEvaluateClient{err: unavailable}
			evaluator := NewEvaluator(EvaluatorConfig{Client: client, State: evaluatorTestState(edgecore.PolicyModeEnforce)})
			decision, err := evaluator.EvaluateHook(context.Background(), buildCommandRequest(t, command))
			if err != nil || len(client.requests) != 1 {
				t.Fatalf("evaluate: err=%v calls=%d", err, len(client.requests))
			}
			_, statErr := os.Stat(buildCommandRunTool(t, decision))
			if command == readOnlyGitCommand {
				if decision.Decision != claude.DecisionAllow || statErr != nil {
					t.Fatalf("read-only git control: decision=%+v stat=%v; want allow and started", decision, statErr)
				}
				return
			}
			if decision.Decision != claude.DecisionDeny {
				t.Fatalf("decision = %+v, want deny", decision)
			}
			if !os.IsNotExist(statErr) {
				t.Fatalf("command started without gateway authority; stat=%v", statErr)
			}
		})
	}
}
