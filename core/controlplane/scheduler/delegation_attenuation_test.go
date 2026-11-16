package scheduler

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/infra/config"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// The scheduler builds its token service on every dispatch-time verify, so
// these tests change the depth limit with t.Setenv and never run in parallel.
const delegationMaxDepthEnv = "CORDUM_DELEGATION_MAX_DEPTH"

func chainAgent(i int) string { return fmt.Sprintf("agent-%d", i) }

// delegationChainFixture holds a chain agent-0 -> ... -> agent-n, issued hop by
// hop through one token service, each hop presenting the previous token.
type delegationChainFixture struct {
	depth       int
	jobStore    *infraStore.RedisJobStore
	revocations *delegation.RedisRevocationStore
	service     *delegation.TokenService
	leaf        string
	verified    delegation.VerifiedToken
}

type delegatedDispatch struct {
	bus  *fakeBus
	sink *recordingSink
	log  *fakeDecisionLogStore
}

func newDelegationChain(t *testing.T, depth int) delegationChainFixture {
	t.Helper()
	ctx := context.Background()
	signingKey := setSchedulerDelegationKeys(t)
	jobStore, agentStore, revocations := newDelegationDispatchTestStore(t)
	for i := 0; i <= depth; i++ {
		createSchedulerDelegationAgent(t, agentStore, "default", chainAgent(i), []string{"read"}, []string{"job.default"})
	}
	service, err := delegation.NewTokenService(
		signingKey,
		map[string]ed25519.PublicKey{signingKey.KID: signingKey.PublicKey()},
		schedulerDelegationPermissionsResolver{store: agentStore},
		revocations,
	)
	if err != nil {
		t.Fatalf("NewTokenService() error = %v", err)
	}
	f := delegationChainFixture{depth: depth, jobStore: jobStore, revocations: revocations, service: service}
	for i := 0; i < depth; i++ {
		f.leaf, _, err = service.IssueDelegationToken(ctx, delegation.IssueRequest{
			Tenant:            "default",
			DelegatingAgentID: chainAgent(i),
			TargetAgentID:     chainAgent(i + 1),
			AllowedActions:    []string{"read"},
			AllowedTopics:     []string{"job.default"},
			ParentToken:       f.leaf,
		})
		if err != nil {
			t.Fatalf("IssueDelegationToken(hop %d) error = %v", i+1, err)
		}
	}
	if f.verified, err = service.VerifyDelegationToken(ctx, f.leaf, chainAgent(depth)); err != nil {
		t.Fatalf("VerifyDelegationToken(leaf) error = %v", err)
	}
	if f.verified.ChainDepth != depth || len(f.verified.DelegationChain) != depth {
		t.Fatalf("leaf depth = %d, links = %d, want %d", f.verified.ChainDepth, len(f.verified.DelegationChain), depth)
	}
	return f
}

// dispatch stores the leaf as jobID's dispatch token and runs processJob with
// the labels the gateway attaches to a delegated submission.
func (f delegationChainFixture) dispatch(t *testing.T, jobID string) delegatedDispatch {
	t.Helper()
	if err := f.jobStore.SetDelegationDispatchToken(context.Background(), jobID, model.DelegationDispatchToken{
		Token:    f.leaf,
		Audience: chainAgent(f.depth),
	}); err != nil {
		t.Fatalf("SetDelegationDispatchToken() error = %v", err)
	}
	issuers := make([]string, 0, f.depth)
	for i := 0; i < f.depth; i++ {
		issuers = append(issuers, chainAgent(i))
	}
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.default",
		TenantId: "default",
		Labels: map[string]string{
			config.LabelDelegationDepth:       strconv.Itoa(f.depth),
			config.LabelDelegationIssuerChain: strings.Join(issuers, ","),
			config.LabelDelegationJTI:         f.verified.JTI,
		},
	}
	d := delegatedDispatch{bus: &fakeBus{}, sink: &recordingSink{}, log: &fakeDecisionLogStore{}}
	engine := NewEngine(d.bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), f.jobStore, nil).
		WithDispatchAuditSink(d.sink).
		WithDecisionLog(d.log)
	if err := engine.processJob(testCtx(t), req, "trace-"+jobID); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}
	return d
}

func (f delegationChainFixture) lineage(t *testing.T, jobID string) model.DelegationLineage {
	t.Helper()
	lineage, err := f.jobStore.GetDelegationLineage(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetDelegationLineage() error = %v", err)
	}
	return lineage
}

// assertDispatchRefused proves the job never reached its topic: it is FAILED
// with reasonCode, its only publish is the DLQ, and no lineage was stored.
func (f delegationChainFixture) assertDispatchRefused(t *testing.T, d delegatedDispatch, jobID, reasonCode string) {
	t.Helper()
	ctx := context.Background()
	state, err := f.jobStore.GetState(ctx, jobID)
	if err != nil || state != JobStateFailed {
		t.Fatalf("state = %s (err %v), want %s", state, err, JobStateFailed)
	}
	reason, err := f.jobStore.GetFailureReason(ctx, jobID)
	if err != nil || reason != reasonCode {
		t.Fatalf("failure reason = %q (err %v), want %q", reason, err, reasonCode)
	}
	published := d.bus.snapshotPublished()
	if len(published) != 1 || published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("publishes = %+v, want only one to %s", published, capsdk.SubjectDLQ)
	}
	if lineage := f.lineage(t, jobID); lineage.TokenJTI != "" || len(lineage.IssuerChain) != 0 {
		t.Fatalf("lineage = %#v, want none", lineage)
	}
}

func TestDelegationAttenuationDispatchDepth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		depth int
	}{
		{name: "default", env: "", depth: 3},
		{name: "configured", env: "5", depth: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(delegationMaxDepthEnv, tc.env)
			chain := newDelegationChain(t, tc.depth)

			t.Run("max_depth", func(t *testing.T) {
				const jobID = "job-depth-max"
				d := chain.dispatch(t, jobID)
				published := d.bus.snapshotPublished()
				if len(published) != 1 || published[0].subject != "job.default" {
					t.Fatalf("publishes = %+v, want one to job.default", published)
				}
				if got := chain.lineage(t, jobID).ChainDepth; got != tc.depth {
					t.Fatalf("lineage ChainDepth = %d, want %d", got, tc.depth)
				}
			})

			t.Run("one_deeper", func(t *testing.T) {
				token, _, err := chain.service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
					Tenant:            "default",
					DelegatingAgentID: chainAgent(tc.depth),
					TargetAgentID:     chainAgent(tc.depth + 1),
					AllowedActions:    []string{"read"},
					AllowedTopics:     []string{"job.default"},
					ParentToken:       chain.leaf,
				})
				if !errors.Is(err, delegation.ErrChainTooDeep) || token != "" {
					t.Fatalf("hop %d: token issued = %t, err = %v, want ErrChainTooDeep", tc.depth+1, token != "", err)
				}
			})

			t.Run("lowered_limit", func(t *testing.T) {
				t.Setenv(delegationMaxDepthEnv, strconv.Itoa(tc.depth-1))
				const jobID = "job-depth-lowered"
				d := chain.dispatch(t, jobID)
				chain.assertDispatchRefused(t, d, jobID, delegationChainBeforeDispatchReasonCode)
			})
		})
	}
}

func TestDelegationAttenuationDispatchRevocation(t *testing.T) {
	t.Setenv(delegationMaxDepthEnv, "")
	const depth = 3
	for i := 0; i < depth; i++ {
		t.Run(fmt.Sprintf("link_%d", i), func(t *testing.T) {
			chain := newDelegationChain(t, depth)
			link := chain.verified.DelegationChain[i]
			if err := chain.revocations.Revoke(context.Background(), link.JTI, chain.verified.ExpiresAt); err != nil {
				t.Fatalf("Revoke(link %d) error = %v", i, err)
			}
			const jobID = "job-revoked-link"
			d := chain.dispatch(t, jobID)
			chain.assertDispatchRefused(t, d, jobID, delegationRevokedBeforeDispatchReasonCode)
		})
	}
}

// assertRecordHoldsChain checks that the JSON form of value names every agent
// of the delegation chain, agent-0..agent-(n-1), in chain order.
func assertRecordHoldsChain(t *testing.T, value any, n int) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}
	record := string(encoded)
	rest := record
	for i := 0; i < n; i++ {
		at := strings.Index(rest, chainAgent(i))
		if at < 0 {
			t.Fatalf("record does not name %s in chain order: %s", chainAgent(i), record)
		}
		rest = rest[at+len(chainAgent(i)):]
	}
}

func TestDelegationAttenuationDispatchRecords(t *testing.T) {
	t.Setenv(delegationMaxDepthEnv, "")
	const depth, jobID = 3, "job-delegation-records"
	chain := newDelegationChain(t, depth)
	d := chain.dispatch(t, jobID)
	if published := d.bus.snapshotPublished(); len(published) != 1 || published[0].subject != "job.default" {
		t.Fatalf("publishes = %+v, want one to job.default", published)
	}

	for _, cell := range []struct {
		name   string
		record func(t *testing.T) any
	}{
		{name: "lineage", record: func(t *testing.T) any {
			lineage := chain.lineage(t, jobID)
			parent := ""
			for i, link := range lineage.IssuerChain {
				if link.ParentJTI != parent {
					t.Fatalf("link %d ParentJTI = %q, want %q", i, link.ParentJTI, parent)
				}
				parent = link.JTI
			}
			return lineage
		}},
		{name: "lineage_audit", record: func(t *testing.T) any {
			event := d.sink.last()
			if d.sink.count() != 1 || event.EventType != audit.EventDelegationLineage {
				t.Fatalf("audit events = %d, last = %q, want one %q", d.sink.count(), event.EventType, audit.EventDelegationLineage)
			}
			return event
		}},
		{name: "decision_log", record: func(t *testing.T) any {
			t.Skip("PR1-29")
			records := d.log.snapshotRecords()
			if len(records) != 1 {
				t.Fatalf("decision log records = %d, want 1", len(records))
			}
			return records[0]
		}},
		{name: "safety_decision", record: func(t *testing.T) any {
			t.Skip("PR1-29")
			record, err := chain.jobStore.GetSafetyDecision(context.Background(), jobID)
			if err != nil {
				t.Fatalf("GetSafetyDecision() error = %v", err)
			}
			return record
		}},
	} {
		t.Run(cell.name, func(t *testing.T) { assertRecordHoldsChain(t, cell.record(t), depth) })
	}
}
