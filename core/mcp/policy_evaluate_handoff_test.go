package mcp

// task-06784e6d DoD 3: a REQUIRE_HUMAN call whose ApprovalHandoff returns no
// approval reference fails closed with ErrApprovalGateMisconfigured (-32097)
// instead of answering "approval pending: " with an empty ref. Nothing runs
// and the outcome is never dedupe-cached: an identical retry reaches the
// handoff again. Runs on both dedupe backends.

import (
	"errors"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestInvokeToolWithPolicy_EmptyHandoffRefFailsClosed(t *testing.T) {
	t.Parallel()
	refs := []struct{ name, ref string }{{"empty", ""}, {"blank", "   "}}
	for _, backend := range rsBackends {
		for _, rc := range refs {
			t.Run(backend.name+"/"+rc.name, func(t *testing.T) {
				t.Parallel()
				f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, backend.open(t))
				handoff := &capturingApprovalHandoff{ref: rc.ref}
				f.deps.ApprovalHandoff = handoff
				for call := 1; call <= 2; call++ {
					res, err := f.invoke(newAuthedToolCallCtx(), rsArgs)
					if res != nil || !errors.Is(err, ErrApprovalGateMisconfigured) {
						t.Errorf("call %d = %+v, %v; want nil and ErrApprovalGateMisconfigured", call, res, err)
					}
				}
				if handoff.calls != 2 || f.upstream.calls.Load() != 0 {
					t.Errorf("handoff calls = %d, upstream executions = %d; want 2 (never cached) and 0",
						handoff.calls, f.upstream.calls.Load())
				}
			})
		}
	}
}
