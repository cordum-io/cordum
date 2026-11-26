package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

var inputCommitmentFormat = regexp.MustCompile(`^hmac-sha256:[0-9a-f]{64}$`)

func TestInputCommitmentIsStableWithinSession(t *testing.T) {
	t.Parallel()
	_, sent := commitmentEvaluate(t,
		commitmentHookRequest(t, "echo Bearer synthetic-first-value"),
		commitmentHookRequest(t, "echo Bearer synthetic-first-value"))
	if !inputCommitmentFormat.MatchString(sent[0].InputCommitment) {
		t.Fatalf("input_commitment = %q, want hmac-sha256:<64 hex>", sent[0].InputCommitment)
	}
	if sent[1].InputCommitment != sent[0].InputCommitment {
		t.Fatalf("same input gave two commitments: %q, %q", sent[0].InputCommitment, sent[1].InputCommitment)
	}
}

func TestInputCommitmentSeparatesInputsThatRedactAlike(t *testing.T) {
	t.Parallel()
	_, sent := commitmentEvaluate(t,
		commitmentHookRequest(t, "echo Bearer synthetic-first-value"),
		commitmentHookRequest(t, "echo Bearer synthetic-other-value"))
	if !reflect.DeepEqual(sent[0].InputRedacted, sent[1].InputRedacted) || sent[0].InputHash != sent[1].InputHash {
		t.Fatalf("inputs must redact alike: %#v / %#v", sent[0].InputRedacted, sent[1].InputRedacted)
	}
	if sent[0].InputCommitment == "" || sent[0].InputCommitment == sent[1].InputCommitment {
		t.Fatalf("commitments must differ: %q, %q", sent[0].InputCommitment, sent[1].InputCommitment)
	}
}

func TestInputCommitmentCannotBeTestedAgainstGuesses(t *testing.T) {
	t.Parallel()
	req := commitmentHookRequest(t, "echo Bearer synthetic-pin-7")
	evaluator, sent := commitmentEvaluate(t, req)
	commitment := sent[0].InputCommitment
	if !inputCommitmentFormat.MatchString(commitment) || len(evaluator.commitmentKey) != 32 {
		t.Fatalf("commitment %q from a %d-byte key, want hmac-sha256 under 32 bytes", commitment, len(evaluator.commitmentKey))
	}
	if _, other := commitmentEvaluate(t, req); other[0].InputCommitment == commitment {
		t.Fatalf("a second session reproduced commitment %q", commitment)
	}
	// The real input is one of these ten candidates, so a bare hash would match.
	for pin := 0; pin < 10; pin++ {
		original, err := claude.OriginalActionInput(commitmentHookPayload(fmt.Sprintf("echo Bearer synthetic-pin-%d", pin)))
		if err != nil {
			t.Fatalf("OriginalActionInput: %v", err)
		}
		sum := sha256.Sum256(original)
		if strings.Contains(commitment, hex.EncodeToString(sum[:])) {
			t.Fatalf("commitment is a plain SHA-256 of candidate %d", pin)
		}
	}
	body, err := json.Marshal(sent[0])
	if err != nil {
		t.Fatalf("marshal evaluate request: %v", err)
	}
	original, err := claude.OriginalActionInput(req.RawPayload)
	if err != nil {
		t.Fatalf("OriginalActionInput: %v", err)
	}
	key := evaluator.commitmentKey
	if bytes.Contains(body, key) || bytes.Contains(body, original) {
		t.Fatalf("evaluate request holds the key or the original input: %s", body)
	}
	for _, leaked := range []string{"synthetic-pin-7", hex.EncodeToString(key), base64.StdEncoding.EncodeToString(key), base64.RawURLEncoding.EncodeToString(key)} {
		if strings.Contains(string(body), leaked) {
			t.Fatalf("evaluate request leaked %q: %s", leaked, body)
		}
	}
}

func TestInputCommitmentAbsentWithoutUsablePayload(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string][]byte{
		"empty":         nil,
		"unknown event": []byte(`{"hook_event_name":"FutureHook","tool_name":"Bash","tool_input":{"command":"echo ok"}}`),
	} {
		req := commitmentHookRequest(t, "echo ok")
		req.RawPayload = raw
		if _, sent := commitmentEvaluate(t, req); sent[0].InputCommitment != "" {
			t.Fatalf("%s payload: input_commitment = %q, want none", name, sent[0].InputCommitment)
		}
	}
}

func TestInputCommitmentKeepsConcurrentEvaluatesApart(t *testing.T) {
	t.Parallel()
	client := newBlockingEvaluateClient(coalescedAllowResponse(), nil)
	evaluator := NewEvaluator(EvaluatorConfig{Client: client, State: evaluatorTestState(edgecore.PolicyModeEnforce), HookTimeout: 5 * time.Second})
	reqs := []claude.AgentdRequest{
		commitmentHookRequest(t, "echo Bearer synthetic-first-value"),
		commitmentHookRequest(t, "echo Bearer synthetic-other-value"),
	}
	var registered, done sync.WaitGroup
	registered.Add(len(reqs))
	evaluator.coalesceWaitHook = registered.Done
	for _, req := range reqs {
		done.Add(1)
		go func() {
			defer done.Done()
			_, _ = evaluator.EvaluateHook(context.Background(), req)
		}()
	}
	waitForCoalesceWaiters(t, &registered)
	close(client.release)
	done.Wait()
	if calls := client.callCount(); calls != len(reqs) {
		t.Fatalf("gateway evaluate calls = %d, want one per original input (%d)", calls, len(reqs))
	}
}

// commitmentHookPayload is the stdin Claude Code gives the hook for a Bash call.
func commitmentHookPayload(command string) []byte {
	encoded, _ := json.Marshal(command)
	return []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu-commitment","tool_input":{"command":` + string(encoded) + `}}`)
}

// commitmentHookRequest runs the real hook over a Bash call and returns the
// request it sends to agentd.
func commitmentHookRequest(t *testing.T, command string) claude.AgentdRequest {
	t.Helper()
	var got claude.AgentdRequest
	rc := claude.Run(context.Background(), claude.RunOptions{
		Stdin: bytes.NewReader(commitmentHookPayload(command)),
		Agentd: stubAgentdClientFunc(func(_ context.Context, req claude.AgentdRequest) (claude.AgentdDecision, error) {
			got = req
			return claude.AgentdDecision{Decision: claude.DecisionAllow}, nil
		}),
		Env: map[string]string{"CORDUM_EDGE_SESSION_ID": "edge_sess_eval", "CORDUM_EDGE_EXECUTION_ID": "edge_exec_eval",
			"CORDUM_TENANT_ID": "tenant-eval", "CORDUM_PRINCIPAL_ID": "principal-eval"},
	})
	if rc != 0 || len(got.RawPayload) == 0 || got.ActionHash == "" {
		t.Fatalf("hook run rc=%d action_hash=%q raw=%d bytes", rc, got.ActionHash, len(got.RawPayload))
	}
	return got
}

// commitmentEvaluate sends the requests through one new evaluator, which is
// one Edge session, and returns what reached the gateway client.
func commitmentEvaluate(t *testing.T, reqs ...claude.AgentdRequest) (*Evaluator, []EvaluateRequest) {
	t.Helper()
	client := &stubEvaluateClient{resp: &EvaluateResponse{Decision: string(edgecore.DecisionDeny), PermissionDecision: "deny", PolicySnapshot: "snap-eval"}}
	evaluator := NewEvaluator(EvaluatorConfig{Client: client, State: evaluatorTestState(edgecore.PolicyModeEnforce), HookTimeout: 5 * time.Second})
	for _, req := range reqs {
		if _, err := evaluator.EvaluateHook(context.Background(), req); err != nil {
			t.Fatalf("EvaluateHook: %v", err)
		}
	}
	if len(client.requests) != len(reqs) {
		t.Fatalf("gateway evaluate calls = %d, want %d", len(client.requests), len(reqs))
	}
	return evaluator, client.requests
}
