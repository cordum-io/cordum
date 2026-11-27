package safetykernel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// License-state cells for the kernel's policy evaluation and decision record
// (docs/contract/license-behavior.md). Each fixture installs its policy while
// the license is Enterprise, forces a state on the one resolver the server and
// the policy loader share, reloads through reloadPolicy (the watcher's path),
// and evaluates through Check.

type kernelLicenseState struct {
	name   string
	plan   licensing.Plan
	status string
}

// kernelLicenseStates mirrors the contract's States table.
var kernelLicenseStates = []kernelLicenseState{
	{"valid", licensing.PlanEnterprise, "active"},
	{"warning", licensing.PlanEnterprise, "warning"},
	{"grace", licensing.PlanEnterprise, "grace"},
	{"degraded", licensing.PlanCommunity, "fallback"},
	{"invalid", licensing.PlanCommunity, "fallback"},
	{"missing", licensing.PlanCommunity, "active"},
}

// kernelLicenseEnterpriseStates are the states whose bundle, invariants and
// velocity cells this file covers.
var kernelLicenseEnterpriseStates = []string{"valid", "warning", "grace"}

// The base policy allows by default and holds four velocity rules, the deny
// last.
const kernelLicenseBasePolicy = `default_tenant: default
default_decision: allow
tenants:
  default:
    allow_topics:
      - job.*
rules:
  - id: license-base-deny
    match:
      topics: [job.license.base]
    decision: deny
    reason: base policy deny
  - id: license-velocity-filler-1
    match:
      topics: [job.license.filler-1]
    velocity: {max_requests: 1, window_seconds: 60, key: labels.session_id}
    decision: deny
    reason: filler velocity rule
  - id: license-velocity-filler-2
    match:
      topics: [job.license.filler-2]
    velocity: {max_requests: 1, window_seconds: 60, key: labels.session_id}
    decision: deny
    reason: filler velocity rule
  - id: license-velocity-filler-3
    match:
      topics: [job.license.filler-3]
    velocity: {max_requests: 1, window_seconds: 60, key: labels.session_id}
    decision: deny
    reason: filler velocity rule
  - id: license-velocity-deny
    match:
      topics: [job.license.velocity]
    velocity: {max_requests: 1, window_seconds: 60, key: labels.session_id}
    decision: deny
    reason: velocity deny
`

const kernelLicenseCustomBundle = `rules:
  - id: license-custom-deny
    match:
      topics: [job.license.custom]
    decision: deny
    reason: custom bundle deny
`

const kernelLicenseInvariantsBundle = `rules:
  - id: license-invariant-deny
    match:
      topics: [job.license.invariant]
    decision: deny
    reason: invariants deny
`

// kernelAuditCapture is the exporter behind the production action-gate sink.
type kernelAuditCapture struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (c *kernelAuditCapture) Send(event audit.SIEMEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *kernelAuditCapture) Close() error { return nil }

func (c *kernelAuditCapture) recorded() []audit.SIEMEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.events)
}

type kernelLicenseFixture struct {
	srv     *server
	loader  *policyLoader
	records *kernelAuditCapture
	jobs    int
}

func newKernelLicenseFixture(t *testing.T, st kernelLicenseState) *kernelLicenseFixture {
	t.Helper()
	resolver := licensing.NewEntitlementResolver()
	resolver.ForceStateWithStatus(licensing.PlanEnterprise, licensing.DefaultEntitlements(licensing.PlanEnterprise), nil, "active")
	loader := fragmentLoaderForTest(t, map[string]any{
		"secops/license-custom":   map[string]any{"content": kernelLicenseCustomBundle},
		kernelInvariantsBundleKey: map[string]any{"content": kernelLicenseInvariantsBundle},
	})
	loader.source = filepath.Join(t.TempDir(), "safety.yaml")
	if err := os.WriteFile(loader.source, []byte(kernelLicenseBasePolicy), 0o600); err != nil {
		t.Fatalf("write base policy: %v", err)
	}
	loader.entitlements = resolver
	srv, _ := newTestServerWithVelocity(t, nil, "")
	srv.entitlements = resolver
	f := &kernelLicenseFixture{srv: srv, loader: loader, records: &kernelAuditCapture{}}
	if err := wireActionGatePipeline(srv, f.records); err != nil {
		t.Fatalf("wire action gates: %v", err)
	}
	f.reload(t)
	f.requireDeny(t, "job.license.base", "license-base-deny", nil)
	f.requireDeny(t, "job.license.custom", "license-custom-deny", nil)
	f.requireDeny(t, "job.license.invariant", "license-invariant-deny", nil)

	resolver.ForceStateWithStatus(st.plan, licensing.DefaultEntitlements(st.plan), nil, st.status)
	if got := resolver.LicenseInfo().Status; got != st.status {
		t.Fatalf("license status = %q, want %q", got, st.status)
	}
	if srv.resolvedPlan() != st.plan || loader.resolvedPlan() != st.plan {
		t.Fatalf("plan: server %q, loader %q, want %q", srv.resolvedPlan(), loader.resolvedPlan(), st.plan)
	}
	f.reload(t)
	return f
}

func (f *kernelLicenseFixture) reload(t *testing.T) {
	t.Helper()
	if err := f.srv.reloadPolicy(context.Background(), f.loader, "license-state"); err != nil {
		t.Fatalf("reload policy: %v", err)
	}
}

// requireDeny sends one Check and returns its job id. The deny must come from
// ruleID, so a default deny or another rule cannot pass it.
func (f *kernelLicenseFixture) requireDeny(t *testing.T, topic, ruleID string, labels map[string]string) string {
	t.Helper()
	jobID := f.nextJobID()
	resp := f.check(t, jobID, topic, labels)
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY || resp.GetRuleId() != ruleID {
		t.Fatalf("%s: decision %v, rule %q, reason %q; want DENY by %s",
			topic, resp.GetDecision(), resp.GetRuleId(), resp.GetReason(), ruleID)
	}
	return jobID
}

func (f *kernelLicenseFixture) check(t *testing.T, jobID, topic string, labels map[string]string) *pb.PolicyCheckResponse {
	t.Helper()
	resp, err := f.srv.Check(context.Background(), &pb.PolicyCheckRequest{
		JobId: jobID, Topic: topic, Tenant: "default", Labels: labels,
	})
	if err != nil {
		t.Fatalf("check %s: %v", topic, err)
	}
	return resp
}

func (f *kernelLicenseFixture) nextJobID() string {
	f.jobs++
	return fmt.Sprintf("job-license-%d", f.jobs)
}

// forKernelLicenseStates runs one subtest per named state; nil names every state.
func forKernelLicenseStates(t *testing.T, names []string, run func(*testing.T, *kernelLicenseFixture)) {
	t.Helper()
	for _, st := range kernelLicenseStates {
		if names != nil && !slices.Contains(names, st.name) {
			continue
		}
		t.Run(st.name, func(t *testing.T) { run(t, newKernelLicenseFixture(t, st)) })
	}
}

func TestLicenseBehaviorKernel_BaseDeny(t *testing.T) {
	forKernelLicenseStates(t, nil, func(t *testing.T, f *kernelLicenseFixture) {
		f.requireDeny(t, "job.license.base", "license-base-deny", nil)
	})
}

func TestLicenseBehaviorKernel_CustomBundleDeny(t *testing.T) {
	forKernelLicenseStates(t, kernelLicenseEnterpriseStates, func(t *testing.T, f *kernelLicenseFixture) {
		f.requireDeny(t, "job.license.custom", "license-custom-deny", nil)
	})
}

func TestLicenseBehaviorKernel_InvariantsDeny(t *testing.T) {
	forKernelLicenseStates(t, kernelLicenseEnterpriseStates, func(t *testing.T, f *kernelLicenseFixture) {
		f.requireDeny(t, "job.license.invariant", "license-invariant-deny", nil)
	})
}

func TestLicenseBehaviorKernel_VelocityDeny(t *testing.T) {
	forKernelLicenseStates(t, kernelLicenseEnterpriseStates, func(t *testing.T, f *kernelLicenseFixture) {
		labels := map[string]string{"session_id": "license-velocity"}
		f.check(t, f.nextJobID(), "job.license.velocity", labels)
		f.requireDeny(t, "job.license.velocity", "license-velocity-deny", labels)
	})
}

// TestLicenseBehaviorKernel_DecisionRecord: a production action gate denies,
// and the kernel's action-gate audit sink records that decision.
func TestLicenseBehaviorKernel_DecisionRecord(t *testing.T) {
	descriptor, err := json.Marshal(&config.ActionDescriptor{Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete})
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	labels := map[string]string{LabelActionDescriptorJSON: string(descriptor)}
	forKernelLicenseStates(t, nil, func(t *testing.T, f *kernelLicenseFixture) {
		before := len(f.records.recorded())
		jobID := f.requireDeny(t, "job.license.record", actiongates.GateIDTenant, labels)
		events := f.records.recorded()[before:]
		if len(events) != 1 {
			t.Fatalf("recorded %d decisions, want 1: %+v", len(events), events)
		}
		got := events[0]
		if got.EventType != audit.EventActionGateDenied || got.JobID != jobID || got.TenantID != "default" ||
			got.Decision != pb.DecisionType_DECISION_TYPE_DENY.String() || got.MatchedRule != actiongates.GateIDTenant {
			t.Fatalf("recorded decision %+v, want %s DENY by %s for %s in tenant default",
				got, audit.EventActionGateDenied, actiongates.GateIDTenant, jobID)
		}
	})
}
