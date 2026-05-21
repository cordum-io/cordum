package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

func TestAuditChainApprovalVerifier_OKAndPartial(t *testing.T) {
	t.Parallel()

	t.Run("intact chain maps to ok", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-ok")
		appendVerifierTestEvents(t, chainer, "tenant-ok", 2)
		appendApprovalEvidenceEvent(t, chainer, approval, nil)

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		if outcome.Status != actiongates.ChainStatusOK {
			t.Fatalf("status = %q, want %q", outcome.Status, actiongates.ChainStatusOK)
		}
		if outcome.HasEvidenceGap {
			t.Fatalf("HasEvidenceGap = true, want false: %+v", outcome)
		}
	})

	t.Run("retention trimmed prefix maps to partial", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-partial")
		appendVerifierTestEvents(t, chainer, "tenant-partial", 3)
		appendApprovalEvidenceEvent(t, chainer, approval, nil)
		deleteStreamEntry(t, client, chainer.StreamKey("tenant-partial"), 0)

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		if outcome.Status != actiongates.ChainStatusPartial {
			t.Fatalf("status = %q, want %q (detail %q)", outcome.Status, actiongates.ChainStatusPartial, outcome.Detail)
		}
		if outcome.HasEvidenceGap {
			t.Fatalf("HasEvidenceGap = true for retention-only prefix, want false: %+v", outcome)
		}
	})
}

func TestAuditChainApprovalVerifier_HMACMismatchCompromised(t *testing.T) {
	t.Parallel()
	goodKey := randomVerifierTestKey(t)
	wrongKey := randomVerifierTestKey(t)
	client, _, writer := newVerifierTestChain(t, goodKey)
	approval := approvalWindow("tenant-hmac")
	appendApprovalEvidenceEvent(t, writer, approval, nil)

	verifierChainer := audit.NewChainer(client, "", audit.WithHMACKey(wrongKey))
	outcome := verifyApprovalForTest(t, client, verifierChainer, approval)
	if outcome.Status != actiongates.ChainStatusCompromised {
		t.Fatalf("status = %q, want %q (detail %q)", outcome.Status, actiongates.ChainStatusCompromised, outcome.Detail)
	}
	if outcome.Detail == "" {
		t.Fatal("compromised HMAC outcome should carry bounded detail for operator diagnostics")
	}
}

func TestAuditChainApprovalVerifier_HMACKeyForVerifyAllowsMatchingKey(t *testing.T) {
	t.Parallel()
	key := randomVerifierTestKey(t)
	client, _, chainer := newVerifierTestChain(t, key)
	approval := approvalWindow("tenant-hmac-ok")
	appendApprovalEvidenceEvent(t, chainer, approval, nil)

	outcome := verifyApprovalForTest(t, client, chainer, approval)
	if outcome.Status != actiongates.ChainStatusOK {
		t.Fatalf("status = %q, want %q (detail %q)", outcome.Status, actiongates.ChainStatusOK, outcome.Detail)
	}
	if outcome.HasEvidenceGap {
		t.Fatalf("HasEvidenceGap = true, want false: %+v", outcome)
	}
}

func TestAuditChainApprovalVerifier_ReturnsDependencyErrors(t *testing.T) {
	t.Parallel()
	chainer := audit.NewChainer(nil, "")
	verifier := newAuditChainApprovalVerifier(nil, chainer)
	if _, err := verifier.VerifyForApproval(context.Background(), "tenant-err", approvalWindow("tenant-err")); err == nil {
		t.Fatal("VerifyForApproval with nil Redis client returned nil error")
	}
}

func TestAuditChainApprovalVerifier_ZeroEventWindowIsEvidenceGap(t *testing.T) {
	t.Parallel()
	client, _, chainer := newVerifierTestChain(t, nil)

	outcome := verifyApprovalForTest(t, client, chainer, approvalWindow("tenant-empty"))
	if outcome.Status != actiongates.ChainStatusOK {
		t.Fatalf("status = %q, want %q for empty-but-readable chain", outcome.Status, actiongates.ChainStatusOK)
	}
	if !outcome.HasEvidenceGap {
		t.Fatalf("HasEvidenceGap = false, want true for zero-event approval window: %+v", outcome)
	}
}

func TestAuditChainApprovalVerifier_RequestedOnlyEvidenceIsGap(t *testing.T) {
	t.Parallel()
	client, _, chainer := newVerifierTestChain(t, nil)
	approval := approvalWindow("tenant-requested-only")
	appendApprovalRequestedEvidenceEvent(t, chainer, approval, nil)

	outcome := verifyApprovalForTest(t, client, chainer, approval)
	requireApprovalEvidenceGap(t, outcome)
}

func TestAuditChainApprovalVerifier_RequiresExactApprovalEvidence(t *testing.T) {
	t.Parallel()

	t.Run("unrelated valid events in window are not sufficient", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-unrelated")
		appendVerifierTestEvents(t, chainer, approval.TenantID, 2)

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("missing action hash is an evidence gap", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-missing-action-hash")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, func(ev *audit.SIEMEvent) {
			delete(ev.Extra, "action_hash")
		})

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("wrong action hash is an evidence gap", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-wrong-action-hash")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, func(ev *audit.SIEMEvent) {
			ev.Extra["action_hash"] = "action_hash_other"
		})

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("wrong approval ref is an evidence gap", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-wrong-ref")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, func(ev *audit.SIEMEvent) {
			ev.Extra["approval_ref"] = "edge_appr_other"
		})

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("wrong tenant is an evidence gap", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-right")
		foreign := *approval
		foreign.TenantID = "tenant-wrong"
		appendApprovalResolvedEvidenceEvent(t, chainer, &foreign, nil)

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("missing resolved decision is an evidence gap", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-missing-decision")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, func(ev *audit.SIEMEvent) {
			ev.Decision = ""
		})

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("wrong resolved decision is an evidence gap", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-wrong-decision")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, func(ev *audit.SIEMEvent) {
			ev.Decision = "rejected"
		})

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		requireApprovalEvidenceGap(t, outcome)
	})

	t.Run("exact approval ref and action hash allows", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-exact")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, nil)

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		if outcome.Status != actiongates.ChainStatusOK || outcome.HasEvidenceGap {
			t.Fatalf("outcome = %+v, want OK without evidence gap", outcome)
		}
	})

	t.Run("exact HMAC approval event allows", func(t *testing.T) {
		t.Parallel()
		key := randomVerifierTestKey(t)
		client, _, chainer := newVerifierTestChain(t, key)
		approval := approvalWindow("tenant-exact-hmac")
		appendApprovalResolvedEvidenceEvent(t, chainer, approval, nil)

		outcome := verifyApprovalForTest(t, client, chainer, approval)
		if outcome.Status != actiongates.ChainStatusOK || outcome.HasEvidenceGap {
			t.Fatalf("outcome = %+v, want OK without evidence gap", outcome)
		}
	})
}

func TestAuditChainApprovalVerifier_FindsApprovalEvidenceBeyondDefaultScanLimit(t *testing.T) {
	client, _, chainer := newVerifierTestChain(t, nil)
	approval := approvalWindow("tenant-high-volume-evidence")
	appendVerifierTestEvents(t, chainer, approval.TenantID, int(audit.DefaultVerifyLimit)+1)
	appendApprovalEvidenceEvent(t, chainer, approval, nil)

	outcome := verifyApprovalForTest(t, client, chainer, approval)
	if outcome.Status != actiongates.ChainStatusOK || outcome.HasEvidenceGap {
		t.Fatalf("outcome = %+v, want OK without evidence gap after %d unrelated events",
			outcome, audit.DefaultVerifyLimit+1)
	}
}

func TestAuditChainApprovalVerifier_RequestedEvidenceBeyondDefaultScanLimitIsGap(t *testing.T) {
	client, _, chainer := newVerifierTestChain(t, nil)
	approval := approvalWindow("tenant-requested-high-volume-evidence")
	appendVerifierTestEvents(t, chainer, approval.TenantID, int(audit.DefaultVerifyLimit)+1)
	appendApprovalRequestedEvidenceEvent(t, chainer, approval, nil)

	outcome := verifyApprovalForTest(t, client, chainer, approval)
	requireApprovalEvidenceGap(t, outcome)
}

func TestAuditChainApprovalVerifier_RejectsCorruptEvidenceBeyondDefaultScanLimit(t *testing.T) {
	client, _, chainer := newVerifierTestChain(t, nil)
	approval := approvalWindow("tenant-corrupt-high-volume-evidence")
	appendVerifierTestEvents(t, chainer, approval.TenantID, int(audit.DefaultVerifyLimit)+1)
	appendCorruptApprovalEvidenceEvent(t, client, chainer, approval)

	outcome := verifyApprovalForTest(t, client, chainer, approval)
	if outcome.Status != actiongates.ChainStatusCompromised {
		t.Fatalf("status = %q, want %q for corrupt evidence beyond %d events: %+v",
			outcome.Status, actiongates.ChainStatusCompromised, audit.DefaultVerifyLimit, outcome)
	}
	if strings.Contains(outcome.Detail, approval.ActionHash) || strings.ContainsAny(outcome.Detail, "{}") {
		t.Fatalf("Detail = %q, want bounded corruption detail without raw approval evidence", outcome.Detail)
	}
}

func TestApprovalEvidenceExists_FailClosedEdges(t *testing.T) {
	t.Run("malformed event json is ignored", func(t *testing.T) {
		t.Parallel()
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-malformed-evidence")
		if err := client.XAdd(context.Background(), &redis.XAddArgs{
			Stream: chainer.StreamKey(approval.TenantID),
			Values: map[string]any{"event": "{not-json"},
		}).Err(); err != nil {
			t.Fatalf("append malformed evidence candidate: %v", err)
		}

		found, err := approvalEvidenceExists(
			context.Background(),
			client,
			chainer.StreamKey(approval.TenantID),
			auditVerifyOptionsForApproval(approval, time.Now().UTC()),
			approval,
		)
		if err != nil {
			t.Fatalf("approvalEvidenceExists returned error for malformed event: %v", err)
		}
		if found {
			t.Fatal("malformed event JSON matched approval evidence")
		}
	})

	t.Run("cap exhaustion fails closed before exact evidence", func(t *testing.T) {
		client, _, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-cap-exhausted")
		appendVerifierTestEvents(t, chainer, approval.TenantID, 2)
		appendApprovalEvidenceEvent(t, chainer, approval, nil)
		opts := auditVerifyOptionsForApproval(approval, time.Now().UTC())
		opts.Limit = 2

		found, err := approvalEvidenceExists(context.Background(), client, chainer.StreamKey(approval.TenantID), opts, approval)
		if err != nil {
			t.Fatalf("approvalEvidenceExists cap exhaustion returned error: %v", err)
		}
		if found {
			t.Fatal("approval evidence matched after explicit scan cap was exhausted")
		}
	})

	t.Run("stream read error surfaces bounded error", func(t *testing.T) {
		client, srv, chainer := newVerifierTestChain(t, nil)
		approval := approvalWindow("tenant-read-error")
		srv.Close()

		found, err := approvalEvidenceExists(
			context.Background(),
			client,
			chainer.StreamKey(approval.TenantID),
			auditVerifyOptionsForApproval(approval, time.Now().UTC()),
			approval,
		)
		if err == nil {
			t.Fatal("approvalEvidenceExists returned nil error after Redis shutdown")
		}
		if found {
			t.Fatal("approval evidence matched despite Redis read error")
		}
		if msg := err.Error(); !strings.Contains(msg, "scan approval evidence") || strings.Contains(msg, approval.ActionHash) {
			t.Fatalf("error = %q, want bounded scan error without raw approval payload", msg)
		}
	})
}

func verifyApprovalForTest(
	t *testing.T,
	client redis.UniversalClient,
	chainer *audit.Chainer,
	approval *edgecore.EdgeApproval,
) actiongates.ChainVerifyOutcome {
	t.Helper()
	verifier := newAuditChainApprovalVerifier(client, chainer)
	outcome, err := verifier.VerifyForApproval(context.Background(), approval.TenantID, approval)
	if err != nil {
		t.Fatalf("VerifyForApproval: %v", err)
	}
	return outcome
}

func newVerifierTestChain(t *testing.T, hmacKey []byte) (redis.UniversalClient, *miniredis.Miniredis, *audit.Chainer) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	var opts []audit.ChainerOption
	if len(hmacKey) > 0 {
		opts = append(opts, audit.WithHMACKey(hmacKey))
	}
	return client, mr, audit.NewChainer(client, "", opts...)
}

func appendVerifierTestEvents(t *testing.T, chainer *audit.Chainer, tenant string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		event := audit.SIEMEvent{
			Timestamp: time.Now().UTC(),
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			TenantID:  tenant,
			Action:    "approval-verify-" + strconv.Itoa(i),
			JobID:     "job-" + strconv.Itoa(i),
		}
		if err := chainer.Append(context.Background(), &event); err != nil {
			t.Fatalf("append audit event %d: %v", i, err)
		}
	}
}

func appendApprovalEvidenceEvent(
	t *testing.T,
	chainer *audit.Chainer,
	approval *edgecore.EdgeApproval,
	mutate func(*audit.SIEMEvent),
) {
	t.Helper()
	appendApprovalResolvedEvidenceEvent(t, chainer, approval, mutate)
}

func appendApprovalRequestedEvidenceEvent(
	t *testing.T,
	chainer *audit.Chainer,
	approval *edgecore.EdgeApproval,
	mutate func(*audit.SIEMEvent),
) {
	t.Helper()
	event := audit.SIEMEvent{
		Timestamp: approval.CreatedAt.Add(time.Minute),
		EventType: audit.EventEdgeApprovalRequested,
		Severity:  audit.SeverityMedium,
		TenantID:  approval.TenantID,
		Action:    "edge_approval_requested",
		Decision:  "require_approval",
		Extra: map[string]string{
			"approval_ref": approval.ApprovalRef,
			"action_hash":  approval.ActionHash,
		},
	}
	if mutate != nil {
		mutate(&event)
	}
	if err := chainer.Append(context.Background(), &event); err != nil {
		t.Fatalf("append approval evidence: %v", err)
	}
}

func appendApprovalResolvedEvidenceEvent(
	t *testing.T,
	chainer *audit.Chainer,
	approval *edgecore.EdgeApproval,
	mutate func(*audit.SIEMEvent),
) {
	t.Helper()
	resolvedAt := approval.CreatedAt.Add(time.Minute)
	if approval.ResolvedAt != nil && !approval.ResolvedAt.IsZero() {
		resolvedAt = *approval.ResolvedAt
	}
	event := edgecore.SIEMEventForApprovalResolved(
		approval.TenantID,
		approval.ApprovalRef,
		approval.RuleID,
		"approved",
		"principal-resolver",
		resolvedAt,
		map[string]string{"action_hash": approval.ActionHash},
	)
	if mutate != nil {
		mutate(&event)
	}
	if err := chainer.Append(context.Background(), &event); err != nil {
		t.Fatalf("append resolved approval evidence: %v", err)
	}
}

func appendCorruptApprovalEvidenceEvent(
	t *testing.T,
	client redis.UniversalClient,
	chainer *audit.Chainer,
	approval *edgecore.EdgeApproval,
) {
	t.Helper()
	last := readLastVerifierEvent(t, client, chainer.StreamKey(approval.TenantID))
	event := edgecore.SIEMEventForApprovalResolved(
		approval.TenantID,
		approval.ApprovalRef,
		approval.RuleID,
		"approved",
		"principal-resolver",
		approval.CreatedAt.Add(time.Minute),
		map[string]string{"action_hash": approval.ActionHash},
	)
	event.Seq = last.Seq + 1
	event.PrevHash = last.EventHash
	event.EventHash = "corrupt_event_hash"
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal corrupt approval evidence: %v", err)
	}
	if err := client.XAdd(context.Background(), &redis.XAddArgs{
		Stream: chainer.StreamKey(approval.TenantID),
		Values: map[string]any{"seq": strconv.FormatInt(event.Seq, 10), "event": string(payload)},
	}).Err(); err != nil {
		t.Fatalf("append corrupt approval evidence: %v", err)
	}
}

func readLastVerifierEvent(t *testing.T, client redis.UniversalClient, streamKey string) audit.SIEMEvent {
	t.Helper()
	entries, err := client.XRevRangeN(context.Background(), streamKey, "+", "-", 1).Result()
	if err != nil {
		t.Fatalf("xrevrange last audit event: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("last audit event count = %d, want 1", len(entries))
	}
	payload, ok := entries[0].Values["event"].(string)
	if !ok || payload == "" {
		t.Fatalf("last audit event missing event payload: %#v", entries[0].Values)
	}
	var event audit.SIEMEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("unmarshal last audit event: %v", err)
	}
	return event
}

func requireApprovalEvidenceGap(t *testing.T, outcome actiongates.ChainVerifyOutcome) {
	t.Helper()
	if outcome.Status != actiongates.ChainStatusOK {
		t.Fatalf("status = %q, want OK evidence gap: %+v", outcome.Status, outcome)
	}
	if !outcome.HasEvidenceGap {
		t.Fatalf("HasEvidenceGap = false, want true for missing exact approval evidence: %+v", outcome)
	}
	if !strings.HasPrefix(outcome.Detail, "approval_evidence_missing:") {
		t.Fatalf("Detail = %q, want approval evidence gap without raw event contents", outcome.Detail)
	}
	if len(outcome.Detail) > len("approval_evidence_missing:")+16 {
		t.Fatalf("Detail = %q, want bounded approval ref prefix only", outcome.Detail)
	}
	if strings.Contains(outcome.Detail, "action_hash_") || strings.ContainsAny(outcome.Detail, "{}") {
		t.Fatalf("Detail = %q, want no raw event JSON or action hash", outcome.Detail)
	}
}

func approvalWindow(tenant string) *edgecore.EdgeApproval {
	created := time.Now().UTC().Add(-time.Hour)
	resolved := time.Now().UTC().Add(time.Minute)
	consumed := resolved.Add(time.Second)
	expires := consumed.Add(time.Hour)
	return &edgecore.EdgeApproval{
		ApprovalRef: "edge_appr_" + tenant,
		TenantID:    tenant,
		Status:      edgecore.ApprovalStatusApproved,
		Decision:    edgecore.ApprovalDecisionApprove,
		ActionHash:  "action_hash_" + tenant,
		CreatedAt:   created,
		ResolvedAt:  &resolved,
		ConsumedAt:  &consumed,
		ExpiresAt:   &expires,
	}
}

func deleteStreamEntry(t *testing.T, client redis.UniversalClient, streamKey string, index int) {
	t.Helper()
	entries, err := client.XRange(context.Background(), streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if index < 0 || index >= len(entries) {
		t.Fatalf("delete index %d outside stream length %d", index, len(entries))
	}
	if err := client.XDel(context.Background(), streamKey, entries[index].ID).Err(); err != nil {
		t.Fatalf("xdel: %v", err)
	}
}

func randomVerifierTestKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return key
}
