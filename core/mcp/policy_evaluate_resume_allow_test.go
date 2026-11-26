package mcp

// ALLOW-resume tests (task-74f4c01e, PR #541 review thread on
// policy_evaluate.go). A call whose claim consumed an Edge approval can be
// evaluated to ALLOW instead of REQUIRE_HUMAN, for example once the session
// taint lookup has recovered since the hold. It still executes once, and its
// mcp.tool.post or mcp.tool.failed event must name the consumed approval, as
// a REQUIRE_HUMAN resume's event does, so the Edge approval joins the
// execution it was spent on. Fakes are prefixed ra; the rs fixture of
// policy_evaluate_resume_test.go is reused unchanged.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var errRAUpstream = errors.New("ra upstream failed")

// raFailingUpstream counts executions and fails every one.
type raFailingUpstream struct{ calls atomic.Int64 }

func (u *raFailingUpstream) Invoke(context.Context, ToolCallParams) (*ToolCallResult, error) {
	u.calls.Add(1)
	return nil, errRAUpstream
}

func TestResume_AllowCarriesConsumedApprovalRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		resume  bool
		fail    bool
		kind    edge.EventKind
		other   edge.EventKind
		wantRef string
	}{
		{"resume_post", true, false, edge.EventKindMCPToolPost, edge.EventKindMCPToolFailed, "edge_appr_rs_1"},
		{"resume_failed", true, true, edge.EventKindMCPToolFailed, edge.EventKindMCPToolPost, "edge_appr_rs_1"},
		{"refless_post", false, false, edge.EventKindMCPToolPost, edge.EventKindMCPToolFailed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRSFixture(pb.DecisionType_DECISION_TYPE_ALLOW, NewInProcessDedupeStore())
			failing := &raFailingUpstream{}
			if tc.fail {
				f.deps.Upstream = failing
			}
			marker := rsMarker(rsTool, rsArgs)
			ctx := newAuthedToolCallCtx()
			if tc.resume {
				ctx = rsResume(marker)
			}
			res, err := f.invoke(ctx, rsArgs)
			executions := f.upstream.calls.Load() + failing.calls.Load()
			if executions != 1 {
				t.Errorf("upstream executions = %d, want 1", executions)
			}
			if tc.fail {
				if !errors.Is(err, errRAUpstream) || res != nil {
					t.Errorf("call = %+v, %v; want the upstream error", res, err)
				}
			} else if err != nil || res != f.upstream.result {
				t.Errorf("call = %+v, %v; want the upstream result %+v", res, err, f.upstream.result)
			}
			var refs []string
			for _, evt := range f.emitter.ofKind(tc.kind) {
				refs = append(refs, evt.ApprovalRef)
			}
			if len(refs) != 1 || refs[0] != tc.wantRef {
				t.Errorf("%s event ApprovalRefs = %q; want exactly [%q]", tc.kind, refs, tc.wantRef)
			}
			if n := len(f.emitter.ofKind(tc.other)); n != 0 {
				t.Errorf("%s events = %d, want 0", tc.other, n)
			}
			if mints, authz := f.handoff.mints.Load(), len(f.handoff.authorized()); mints != 0 || authz != 0 {
				t.Errorf("mints = %d, authorizations = %d; want 0 and 0 on ALLOW", mints, authz)
			}
			// The policy evaluator never spends the marker: on ALLOW only a
			// registry gate may Use it, once.
			if !marker.Use(rsTool, marker.ActionHash, marker.InputHash) {
				t.Error("marker.Use = false after the call; the ALLOW path must not consume it")
			}
		})
	}
}
