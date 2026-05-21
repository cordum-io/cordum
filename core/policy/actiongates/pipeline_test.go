package actiongates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func approvalTimeAgo() *time.Time {
	t := time.Now().UTC().Add(-1 * time.Hour)
	return &t
}

// pipelineFixture returns the full production gate ordering wired against
// in-memory fakes. The mutation + provenance gates share the same approval
// store so behavior matches the real wiring.
type pipelineFixture struct {
	pipeline  *Pipeline
	approvals *fakeApprovalLookup
	resources *fakeResourceLookup
	identity  *fakeMCPIdentityResolver
	reach     *fakeReachability
	chain     *fakeChainVerifier
}

func newPipelineFixture(opts ...func(*pipelineFixture)) *pipelineFixture {
	f := &pipelineFixture{
		approvals: &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{}},
		resources: &fakeResourceLookup{},
		identity:  &fakeMCPIdentityResolver{by: map[string]*mcp.AgentIdentity{}},
		reach:     &fakeReachability{},
		chain:     &fakeChainVerifier{outcome: ChainVerifyOutcome{Status: ChainStatusOK}},
	}
	for _, o := range opts {
		o(f)
	}
	f.pipeline = NewPipeline(
		NewTenantGate(),
		NewFileGate(),
		NewURLGate(URLGateOptions{}),
		NewMCPGate(MCPGateOptions{Identities: f.identity, Reachability: f.reach}),
		NewMutationGate(MutationGateOptions{Approvals: f.approvals, Resources: f.resources}),
		NewProvenanceGate(ProvenanceGateOptions{Approvals: f.approvals, ChainVerifier: f.chain}),
	)
	return f
}

func pipelineAuthCtx() context.Context {
	return ctxWithAuth(&auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"})
}

func pipelineDeleteAction() *config.ActionDescriptor {
	return &config.ActionDescriptor{
		Kind:           config.ActionKindMutation,
		Verb:           config.ActionVerbDelete,
		TargetResource: &config.ActionTargetResource{Type: "user", ID: "user_42", OwnerTenant: "tnt_a"},
	}
}

type staticPipelineGate struct {
	id  string
	dec ActionGateDecision
}

func (g staticPipelineGate) ID() string {
	return g.id
}

func (g staticPipelineGate) Evaluate(context.Context, *config.PolicyInput) ActionGateDecision {
	return g.dec
}

// DoD-7 shape #1: allowed — valid backed approval, all gates clear, no fire.
func TestPipeline_AllowedShape(t *testing.T) {
	t.Parallel()
	act := pipelineDeleteAction()
	act.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_ok"}
	hash := CanonicalActionHash(act)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_ok",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	f := newPipelineFixture(func(f *pipelineFixture) {
		f.approvals.records["tnt_a:"+hash] = approval
	})
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	// Mutation gate emits ALLOW for a valid backend approval; fired must be
	// false because there is no blocking decision.
	if fired || dec.Fired() {
		t.Fatalf("expected pipeline to clear; got fired=%v dec=%v sub=%q", fired, dec.Decision, dec.SubReason)
	}
}

// DoD-7 shape #2: denied — cross-tenant DENY short-circuits the entire pipeline.
func TestPipeline_DeniedShape(t *testing.T) {
	t.Parallel()
	act := &config.ActionDescriptor{
		Kind:           config.ActionKindMutation,
		Verb:           config.ActionVerbDelete,
		TargetResource: &config.ActionTargetResource{Type: "user", ID: "user_42", OwnerTenant: "tnt_b"},
	}
	f := newPipelineFixture()
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if !fired || dec.Decision != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected DENY, got fired=%v dec=%v", fired, dec.Decision)
	}
	if dec.GateID != GateIDTenant || !strings.Contains(dec.SubReason, "cross_tenant") {
		t.Fatalf("expected tenant cross_tenant denial, got gate=%q sub=%q", dec.GateID, dec.SubReason)
	}
}

// DoD-7 shape #3: require-human — destructive verb with no claim, mutation
// gate raises REQUIRE_HUMAN (provenance gate would too but mutation fires first).
func TestPipeline_RequireHumanShape(t *testing.T) {
	t.Parallel()
	act := pipelineDeleteAction()
	f := newPipelineFixture()
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if !fired || dec.Decision != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		t.Fatalf("expected REQUIRE_HUMAN, got fired=%v dec=%v", fired, dec.Decision)
	}
}

// DoD-7 shape #4: tenant isolation — prefixed-ID encodes a different tenant.
func TestPipeline_TenantIsolationShape(t *testing.T) {
	t.Parallel()
	act := &config.ActionDescriptor{
		Kind:           config.ActionKindMutation,
		Verb:           config.ActionVerbWrite,
		TargetResource: &config.ActionTargetResource{Type: "doc", ID: "tnt_b_doc_99"},
	}
	f := newPipelineFixture()
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if !fired || dec.GateID != GateIDTenant {
		t.Fatalf("expected tenant gate denial, got fired=%v gate=%q", fired, dec.GateID)
	}
	if !strings.Contains(dec.SubReason, "prefixed_id_mismatch") {
		t.Fatalf("expected prefixed_id_mismatch sub_reason, got %q", dec.SubReason)
	}
}

// DoD-7 shape #5: stale/conflict — approval already consumed.
func TestPipeline_StaleConflictShape(t *testing.T) {
	t.Parallel()
	act := pipelineDeleteAction()
	act.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_old"}
	hash := CanonicalActionHash(act)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_old",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
		ConsumedAt:  approvalTimeAgo(),
	}
	f := newPipelineFixture(func(f *pipelineFixture) {
		f.approvals.records["tnt_a:"+hash] = approval
	})
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if !fired || dec.Code != CodeConflict {
		t.Fatalf("expected conflict, got fired=%v code=%q", fired, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "consumed") {
		t.Fatalf("expected consumed sub_reason, got %q", dec.SubReason)
	}
}

// DoD-7 shape #6: unavailable backend — approval store returns an error.
func TestPipeline_UnavailableBackendShape(t *testing.T) {
	t.Parallel()
	act := pipelineDeleteAction()
	act.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_x"}
	f := newPipelineFixture(func(f *pipelineFixture) {
		f.approvals.err = errors.New("redis down")
	})
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if !fired || dec.Code != CodeInternalError {
		t.Fatalf("expected internal_error fail-closed, got fired=%v code=%q", fired, dec.Code)
	}
}

// DoD-7 shape #7: audit evidence — provenance gate fires after mutation
// allows, when the chain verifier reports an evidence gap.
func TestPipeline_AuditEvidenceShape(t *testing.T) {
	t.Parallel()
	act := pipelineDeleteAction()
	act.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_gap"}
	hash := CanonicalActionHash(act)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_gap",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	f := newPipelineFixture(func(f *pipelineFixture) {
		f.approvals.records["tnt_a:"+hash] = approval
		f.chain.outcome = ChainVerifyOutcome{Status: ChainStatusOK, HasEvidenceGap: true}
	})
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if !fired || dec.GateID != GateIDProvenance {
		t.Fatalf("expected provenance gate fire, got fired=%v gate=%q", fired, dec.GateID)
	}
	if !strings.Contains(dec.SubReason, "audit_evidence_missing") {
		t.Fatalf("expected audit_evidence_missing, got %q", dec.SubReason)
	}
}

func TestPipeline_NilPipelineReturnsZero(t *testing.T) {
	t.Parallel()
	var p *Pipeline
	dec, fired := p.Run(pipelineAuthCtx(), &config.PolicyInput{Action: pipelineDeleteAction()})
	if fired || dec.Fired() {
		t.Fatalf("nil pipeline: expected zero/false, got fired=%v dec=%v", fired, dec.Decision)
	}
}

func TestPipeline_NilActionShortCircuits(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture()
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a"})
	if fired || dec.Fired() {
		t.Fatalf("nil action: expected zero/false, got fired=%v dec=%v", fired, dec.Decision)
	}
}

func TestPipeline_CanceledContextFailsClosed(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture()
	ctx, cancel := context.WithCancel(pipelineAuthCtx())
	cancel()
	dec, fired := f.pipeline.Run(ctx, &config.PolicyInput{Tenant: "tnt_a", Action: pipelineDeleteAction()})
	if !fired || dec.Code != CodeInternalError {
		t.Fatalf("canceled ctx: expected internal_error fail-closed, got fired=%v code=%q", fired, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "context_canceled") {
		t.Fatalf("expected context_canceled sub_reason, got %q", dec.SubReason)
	}
}

// TestPipeline_AllowedExtraSurvivesPastLaterAllow asserts the QA-flagged
// invariant: when an early gate emits ALLOW with breadcrumb Extras (mutation
// gate's `single_use=true`) and a later gate also passes, the merged Extra
// MUST survive on the returned decision even though fired=false. Before the
// merge fix, the later gate's Allow simply overwrote prior breadcrumbs by
// returning the zero decision.
func TestPipeline_AllowedExtraSurvivesPastLaterAllow(t *testing.T) {
	t.Parallel()
	act := pipelineDeleteAction()
	act.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_ok"}
	hash := CanonicalActionHash(act)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_ok",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	f := newPipelineFixture(func(f *pipelineFixture) {
		f.approvals.records["tnt_a:"+hash] = approval
	})
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if fired {
		t.Fatalf("expected no terminal decision, got fired=%v", fired)
	}
	if dec.Fired() {
		t.Fatalf("merged decision should report Fired()==false, got Decision=%v", dec.Decision)
	}
	if got := dec.Extra["single_use"]; got != "true" {
		t.Fatalf("merged Extra lost mutation gate's single_use breadcrumb; got %q want %q", got, "true")
	}
}

// TestPipeline_AllowedExtraEmptyOnPlainAllow verifies that when no gate
// emits non-empty Extra on the allowed path, the returned Extra map is
// nil — the merge accumulator is allocated lazily so plain ALLOWs do not
// leak an empty map into the audit surface.
func TestPipeline_AllowedExtraEmptyOnPlainAllow(t *testing.T) {
	t.Parallel()
	// Use a read action: no destructive verb, no approval lookup required;
	// only tenant gate runs (and clears) without setting Extras.
	act := &config.ActionDescriptor{
		Kind: config.ActionKindMutation,
		Verb: config.ActionVerbRead,
		TargetResource: &config.ActionTargetResource{
			Type:        "doc",
			ID:          "doc_42",
			OwnerTenant: "tnt_a",
		},
	}
	f := newPipelineFixture()
	dec, fired := f.pipeline.Run(pipelineAuthCtx(), &config.PolicyInput{Tenant: "tnt_a", Action: act})
	if fired || dec.Fired() {
		t.Fatalf("read action should pass cleanly, got fired=%v dec=%v", fired, dec.Decision)
	}
	if len(dec.Extra) != 0 {
		t.Fatalf("plain allow should not leak Extra entries, got %v", dec.Extra)
	}
}

func TestPipeline_AllowedWithConstraintsIsAllowOnlyAnomaly(t *testing.T) {
	t.Parallel()
	p := NewPipeline(
		staticPipelineGate{
			id: "test.constraints",
			dec: ActionGateDecision{
				Decision: pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
				Extra: map[string]string{
					"gate":       "test.constraints",
					"single_use": "true",
				},
				Constraints: map[string]any{"sandbox": "read_only"},
			},
		},
		staticPipelineGate{
			id: "test.later",
			dec: ActionGateDecision{
				Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
				Extra:    map[string]string{"later_gate": "allowed"},
			},
		},
	)

	dec, fired := p.Run(context.Background(), &config.PolicyInput{Action: pipelineDeleteAction()})
	if fired || dec.Fired() {
		t.Fatalf("allow-with-constraints must not become a terminal enforcement claim; fired=%v dec=%v", fired, dec.Decision)
	}
	if len(dec.Constraints) != 0 {
		t.Fatalf("action-gate constraints are not an enforced pipeline output; got %v", dec.Constraints)
	}
	if got := dec.Extra["single_use"]; got != "true" {
		t.Fatalf("safe Extra breadcrumb single_use = %q, want true", got)
	}
	if got := dec.Extra["later_gate"]; got != "allowed" {
		t.Fatalf("safe Extra breadcrumb later_gate = %q, want allowed", got)
	}
	// Policy-bundle constraints remain the typed enforcement path; this action
	// gate regression only guarantees generic action-gate constraints are not
	// silently advertised as enforced by the production pipeline.
}

func TestPipeline_GatesReturnsOrderedCopy(t *testing.T) {
	t.Parallel()
	f := newPipelineFixture()
	gates := f.pipeline.Gates()
	wantOrder := []string{GateIDTenant, GateIDFile, GateIDURL, GateIDMCP, GateIDMutation, GateIDProvenance}
	if len(gates) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(gates), len(wantOrder))
	}
	for i, g := range gates {
		if g.ID() != wantOrder[i] {
			t.Fatalf("Gates()[%d] = %q, want %q", i, g.ID(), wantOrder[i])
		}
	}
	// Mutating the returned slice must not affect the pipeline.
	gates[0] = nil
	if f.pipeline.Gates()[0].ID() != GateIDTenant {
		t.Fatal("Gates() returned a shared slice; internal state was mutated")
	}
}
