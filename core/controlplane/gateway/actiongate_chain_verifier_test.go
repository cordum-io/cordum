package gateway

import (
	"context"
	"crypto/rand"
	"strconv"
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
		appendVerifierTestEvents(t, chainer, "tenant-ok", 3)

		outcome := verifyApprovalForTest(t, client, chainer, approvalWindow("tenant-ok"))
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
		appendVerifierTestEvents(t, chainer, "tenant-partial", 4)
		deleteStreamEntry(t, client, chainer.StreamKey("tenant-partial"), 0)

		outcome := verifyApprovalForTest(t, client, chainer, approvalWindow("tenant-partial"))
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
	appendVerifierTestEvents(t, writer, "tenant-hmac", 2)

	verifierChainer := audit.NewChainer(client, "", audit.WithHMACKey(wrongKey))
	outcome := verifyApprovalForTest(t, client, verifierChainer, approvalWindow("tenant-hmac"))
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
	appendVerifierTestEvents(t, chainer, "tenant-hmac-ok", 2)

	outcome := verifyApprovalForTest(t, client, chainer, approvalWindow("tenant-hmac-ok"))
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
