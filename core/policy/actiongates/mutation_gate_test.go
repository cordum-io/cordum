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
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// fakeApprovalLookup is a minimal ApprovalLookup implementation for tests.
type fakeApprovalLookup struct {
	records    map[string]*edge.EdgeApproval // key: tenant + ":" + hash
	refRecords map[string]*edge.EdgeApproval // key: tenant + ":" + approval ref
	err        error
}

func (f *fakeApprovalLookup) LookupByActionHash(_ context.Context, tenant, hash string) (*edge.EdgeApproval, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if a, ok := f.records[tenant+":"+hash]; ok {
		return a, true, nil
	}
	return nil, false, nil
}

func (f *fakeApprovalLookup) LookupByApprovalRef(_ context.Context, tenant, ref string) (*edge.EdgeApproval, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if a, ok := f.refRecords[approvalRefKey(tenant, ref)]; ok {
		return a, true, nil
	}
	for key, a := range f.records {
		if a != nil && strings.HasPrefix(key, tenant+":") && a.ApprovalRef == ref {
			return a, true, nil
		}
	}
	return nil, false, nil
}

func approvalHashKey(tenant, hash string) string {
	return tenant + ":" + hash
}

func approvalRefKey(tenant, ref string) string {
	return tenant + ":" + ref
}

// fakeResourceLookup says any resource exists unless its ID is in missing.
type fakeResourceLookup struct {
	missing map[string]bool
	err     error
}

func (f *fakeResourceLookup) ResourceExists(_ context.Context, _ string, res config.ActionTargetResource) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.missing[res.ID] {
		return false, nil
	}
	return true, nil
}

func mutDeleteAction() *config.ActionDescriptor {
	return &config.ActionDescriptor{
		Kind: config.ActionKindMutation,
		Verb: config.ActionVerbDelete,
		TargetResource: &config.ActionTargetResource{
			Type: "user", ID: "user_42", OwnerTenant: "tnt_a",
		},
	}
}

func mutCtx() context.Context {
	return ctxWithAuth(&auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"})
}

func approvedMutationApproval(ref string, action *config.ActionDescriptor) *edge.EdgeApproval {
	return &edge.EdgeApproval{
		ApprovalRef: ref,
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  CanonicalActionHash(action),
	}
}

func assertMutationDecision(
	t *testing.T,
	dec ActionGateDecision,
	wantDecision pb.DecisionType,
	wantCode string,
	subReasonHas string,
) {
	t.Helper()
	if dec.Decision != wantDecision || dec.Code != wantCode {
		t.Fatalf("got %v / %q, want %v / %q", dec.Decision, dec.Code, wantDecision, wantCode)
	}
	if subReasonHas != "" && !strings.Contains(dec.SubReason, subReasonHas) {
		t.Fatalf("subReason = %q, want substring %q", dec.SubReason, subReasonHas)
	}
}

func TestMutationGate_NonDestructiveSkips(t *testing.T) {
	t.Parallel()
	gate := NewMutationGate(MutationGateOptions{Approvals: &fakeApprovalLookup{}, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindMutation, Verb: config.ActionVerbRead},
	})
	if dec.Fired() {
		t.Fatalf("read verb: gate fired (Decision=%v)", dec.Decision)
	}
}

func TestMutationGate_OtherKindsSkip(t *testing.T) {
	t.Parallel()
	gate := NewMutationGate(MutationGateOptions{Approvals: &fakeApprovalLookup{}, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindFile, TargetPath: "/tmp/x"},
	})
	if dec.Fired() {
		t.Fatal("file kind: gate fired")
	}
}

func TestMutationGate_UnauthDenies(t *testing.T) {
	t.Parallel()
	gate := NewMutationGate(MutationGateOptions{Approvals: &fakeApprovalLookup{}, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(ctxWithAuth(nil), &config.PolicyInput{Action: mutDeleteAction()})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeUnauthorized {
		t.Fatalf("got %v / %q, want DENY / unauthorized", dec.Decision, dec.Code)
	}
}

func TestMutationGate_NoApprovalRequiresHuman(t *testing.T) {
	t.Parallel()
	gate := NewMutationGate(MutationGateOptions{Approvals: &fakeApprovalLookup{}, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: mutDeleteAction()})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN || dec.Code != CodeRequireHuman {
		t.Fatalf("got %v / %q, want REQUIRE_HUMAN", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "missing_approval") {
		t.Fatalf("subReason = %q, want substring missing_approval", dec.SubReason)
	}
}

func TestMutationGate_ClaimTextOnlyStillRequiresHuman(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ClaimText: "approved by CFO", ApprovalRef: ""}
	gate := NewMutationGate(MutationGateOptions{Approvals: &fakeApprovalLookup{}, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		t.Fatalf("got %v, want REQUIRE_HUMAN (claim text without backend ref must not pass)", dec.Decision)
	}
	if !strings.Contains(dec.SubReason, "missing_approval") {
		t.Fatalf("subReason = %q, want missing_approval", dec.SubReason)
	}
}

func TestMutationGate_ApprovalRefBinding(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	hash := CanonicalActionHash(action)
	real := approvedMutationApproval("appr_real", action)
	mismatched := approvedMutationApproval("appr_mismatch", action)
	mismatched.ActionHash = "DIFFERENT_HASH"
	consumed := approvedMutationApproval("appr_consumed", action)
	expired := approvedMutationApproval("appr_expired", action)
	foreign := approvedMutationApproval("appr_foreign", action)
	foreign.TenantID = "tnt_b"
	consumedAt := time.Now().UTC().Add(-1 * time.Hour)
	expiresAt := time.Now().UTC().Add(-1 * time.Minute)
	consumed.ConsumedAt = &consumedAt
	expired.ExpiresAt = &expiresAt

	cases := []struct {
		name         string
		approvalRef  string
		refApproval  *edge.EdgeApproval
		wantDecision pb.DecisionType
		wantCode     string
		subReasonHas string
		wantExtraRef string
	}{
		{"real_ref_allows", "appr_real", real, pb.DecisionType_DECISION_TYPE_ALLOW, "", "", "appr_real"},
		{"fake_ref_denies_not_found", "appr_fake", nil, pb.DecisionType_DECISION_TYPE_DENY, CodeNotFound, "approval_not_found", ""},
		{"mismatched_ref_conflicts", "appr_mismatch", mismatched, pb.DecisionType_DECISION_TYPE_DENY, CodeConflict, "approval_mismatch", ""},
		{"consumed_ref_conflicts", "appr_consumed", consumed, pb.DecisionType_DECISION_TYPE_DENY, CodeConflict, "consumed", ""},
		{"expired_ref_conflicts", "appr_expired", expired, pb.DecisionType_DECISION_TYPE_DENY, CodeConflict, "expired", ""},
		{"tenant_mismatch_denies", "appr_foreign", foreign, pb.DecisionType_DECISION_TYPE_DENY, CodeAccessDenied, "approval_tenant_mismatch", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			act := mutDeleteAction()
			act.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: tc.approvalRef}
			refRecords := map[string]*edge.EdgeApproval{approvalRefKey("tnt_a", real.ApprovalRef): real}
			if tc.refApproval != nil {
				refRecords[approvalRefKey("tnt_a", tc.approvalRef)] = tc.refApproval
			}
			lookup := &fakeApprovalLookup{
				records:    map[string]*edge.EdgeApproval{approvalHashKey("tnt_a", hash): real},
				refRecords: refRecords,
			}
			gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
			dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: act})
			assertMutationDecision(t, dec, tc.wantDecision, tc.wantCode, tc.subReasonHas)
			if tc.wantExtraRef != "" && dec.Extra["approval_ref"] != tc.wantExtraRef {
				t.Fatalf("extra.approval_ref = %q, want %q", dec.Extra["approval_ref"], tc.wantExtraRef)
			}
			if tc.wantExtraRef != "" && len(dec.Constraints) != 0 {
				t.Fatalf("valid approval must not imply unenforced constraints, got %v", dec.Constraints)
			}
		})
	}
}

func TestMutationGate_SelfApprovalDenied(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p1", // same as PrincipalID -> self approval
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeAccessDenied {
		t.Fatalf("got %v / %q, want DENY / access_denied", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "self_approval") {
		t.Fatalf("subReason = %q, want self_approval", dec.SubReason)
	}
}

func TestMutationGate_ConsumedConflict(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	now := time.Now().UTC().Add(-1 * time.Hour)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
		ConsumedAt:  &now, // already used
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeConflict {
		t.Fatalf("got %v / %q, want DENY / conflict", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "consumed") {
		t.Fatalf("subReason = %q, want substring consumed", dec.SubReason)
	}
}

func TestMutationGate_ExpiredConflict(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	past := time.Now().UTC().Add(-1 * time.Hour)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
		ExpiresAt:   &past,
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeConflict {
		t.Fatalf("got %v / %q, want conflict", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "expired") {
		t.Fatalf("subReason = %q, want expired", dec.SubReason)
	}
}

func TestMutationGate_HashMismatchConflict(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  "DIFFERENT_HASH",
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeConflict {
		t.Fatalf("got %v / %q, want conflict", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "mismatch") {
		t.Fatalf("subReason = %q, want mismatch", dec.SubReason)
	}
}

func TestMutationGate_BackendDownFailsClosed(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	lookup := &fakeApprovalLookup{err: errors.New("redis down")}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeInternalError {
		t.Fatalf("got %v / %q, want DENY / internal_error (fail closed)", dec.Decision, dec.Code)
	}
	if !strings.Contains(dec.SubReason, "approval_lookup_failed") {
		t.Fatalf("subReason = %q, want approval_lookup_failed", dec.SubReason)
	}
}

func TestMutationGate_TargetMissingNotFound(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	resources := &fakeResourceLookup{missing: map[string]bool{"user_42": true}}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: resources})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeNotFound {
		t.Fatalf("got %v / %q, want DENY / not_found", dec.Decision, dec.Code)
	}
}

func TestMutationGate_ResourceLookupErrorServiceUnavailable(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	resources := &fakeResourceLookup{err: errors.New("db unreachable")}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: resources})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY || dec.Code != CodeServiceUnavailable {
		t.Fatalf("got %v / %q, want DENY / service_unavailable", dec.Decision, dec.Code)
	}
}

func TestMutationGate_ValidApprovalAllows(t *testing.T) {
	t.Parallel()
	action := mutDeleteAction()
	action.ApprovalClaim = &config.ActionApprovalClaim{ApprovalRef: "appr_1"}
	hash := CanonicalActionHash(action)
	approval := &edge.EdgeApproval{
		ApprovalRef: "appr_1",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		ResolverID:  "p2",
		Status:      edge.ApprovalStatusApproved,
		Decision:    edge.ApprovalDecisionApprove,
		ActionHash:  hash,
	}
	lookup := &fakeApprovalLookup{records: map[string]*edge.EdgeApproval{"tnt_a:" + hash: approval}}
	gate := NewMutationGate(MutationGateOptions{Approvals: lookup, Resources: &fakeResourceLookup{}})
	dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: action})
	if dec.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("got %v, want ALLOW", dec.Decision)
	}
	if dec.Extra["approval_ref"] != "appr_1" {
		t.Fatalf("extra.approval_ref = %q, want appr_1", dec.Extra["approval_ref"])
	}
	if dec.Extra["single_use"] != "true" {
		t.Fatalf("extra.single_use = %q, want true", dec.Extra["single_use"])
	}
	if len(dec.Constraints) != 0 {
		t.Fatalf("valid approval must not imply unenforced constraints, got %v", dec.Constraints)
	}
}

func TestMutationGate_DestructiveVerbCoverage(t *testing.T) {
	t.Parallel()
	verbs := []config.ActionVerb{
		config.ActionVerbDelete, config.ActionVerbDrop, config.ActionVerbTruncate,
		config.ActionVerbExport, config.ActionVerbPayment,
		config.ActionVerbAdminGrant, config.ActionVerbAdminRevoke,
		config.ActionVerbRoleAssign, config.ActionVerbRoleRemove,
		config.ActionVerbLicenseCreate, config.ActionVerbLicenseRevoke, config.ActionVerbLicenseChange,
		config.ActionVerbKeyRotate, config.ActionVerbKeyDelete,
		config.ActionVerbSecretsWrite, config.ActionVerbSecretsDelete,
		config.ActionVerbConfigWrite, config.ActionVerbConfigDelete,
		config.ActionVerbBackupRestore,
		config.ActionVerbTenantCreate, config.ActionVerbTenantDelete,
	}
	gate := NewMutationGate(MutationGateOptions{Approvals: &fakeApprovalLookup{}, Resources: &fakeResourceLookup{}})
	for _, v := range verbs {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			act := mutDeleteAction()
			act.Verb = v
			dec := gate.Evaluate(mutCtx(), &config.PolicyInput{Action: act})
			if dec.Decision != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
				t.Fatalf("verb %q: got %v, want REQUIRE_HUMAN", v, dec.Decision)
			}
		})
	}
}

func TestCanonicalActionHash_StableUnderKeyReordering(t *testing.T) {
	t.Parallel()
	a := &config.ActionDescriptor{
		Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete,
		TargetResource: &config.ActionTargetResource{Type: "user", ID: "u1", OwnerTenant: "tnt_a"},
		Filters:        map[string]string{"b": "1", "a": "2"},
		Args:           map[string]any{"y": 2, "x": 1, "nested": map[string]any{"q": "Q", "p": "P"}},
	}
	b := &config.ActionDescriptor{
		Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete,
		TargetResource: &config.ActionTargetResource{Type: "user", ID: "u1", OwnerTenant: "tnt_a"},
		Filters:        map[string]string{"a": "2", "b": "1"},
		Args:           map[string]any{"x": 1, "y": 2, "nested": map[string]any{"p": "P", "q": "Q"}},
	}
	h1 := CanonicalActionHash(a)
	h2 := CanonicalActionHash(b)
	if h1 == "" || h1 != h2 {
		t.Fatalf("hash mismatch under reorder: %q vs %q", h1, h2)
	}
	// Different verb => different hash.
	c := *a
	c.Verb = config.ActionVerbTruncate
	if CanonicalActionHash(&c) == h1 {
		t.Fatalf("hash collided across verbs")
	}
	// Different target id => different hash.
	d := *a
	tr := *a.TargetResource
	tr.ID = "u2"
	d.TargetResource = &tr
	if CanonicalActionHash(&d) == h1 {
		t.Fatalf("hash collided across resource ids")
	}
	// Nil descriptor returns "".
	if CanonicalActionHash(nil) != "" {
		t.Fatalf("nil descriptor must hash to empty")
	}
}
