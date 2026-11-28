package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/governance"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

func TestAuditVerifyHMACActivation(t *testing.T) {
	for _, noKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured key", true: "removed key"}[noKey], func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			client := s.redisClient()
			writer := audit.NewChainer(client, "", audit.WithHMACKey(randomVerifierTestKey(t)))
			approval := approvalWindow("default")
			appendApprovalEvidenceEvent(t, writer, approval, nil)
			stripLastActivationHMAC(t, client, writer.StreamKey("default"))
			s.auditChainer = writer
			wantStatus, wantGap := audit.VerifyStatusCompromised, audit.GapTypeHMACMissing
			if noKey {
				s.auditChainer = audit.NewChainer(client, "")
				wantStatus, wantGap = audit.VerifyStatusPartial, audit.GapTypeHMACUnverified
			}
			rr := httptest.NewRecorder()
			req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil))
			s.handleAuditVerify(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("http status=%d body=%s", rr.Code, rr.Body.String())
			}
			var result audit.VerifyResult
			if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != wantStatus || result.HMACSeen || result.HMACActivatedSeq != 1 ||
				result.Integrity != "hash_chain_only" || len(result.Gaps) != 1 || result.Gaps[0].Type != wantGap {
				t.Fatalf("unexpected activation verification: %+v", result)
			}
			status, err := newGovernanceHealthDeps(s, "default").VerifyChain(context.Background())
			if err != nil || status != governance.ChainStatus(wantStatus) {
				t.Fatalf("governance status=%s err=%v, want %s", status, err, wantStatus)
			}
		})
	}
}

func TestAuditApprovalHMACActivation(t *testing.T) {
	for _, noKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured key", true: "removed key"}[noKey], func(t *testing.T) {
			client, _, writer := newVerifierTestChain(t, randomVerifierTestKey(t))
			approval := approvalWindow("tenant")
			appendApprovalEvidenceEvent(t, writer, approval, nil)
			stripLastActivationHMAC(t, client, writer.StreamKey("tenant"))
			reader := writer
			if noKey {
				reader = audit.NewChainer(client, "")
			}
			outcome := verifyApprovalForTest(t, client, reader, approval)
			if outcome.Status != actiongates.ChainStatusCompromised || outcome.HasEvidenceGap {
				t.Fatalf("unsigned approval evidence was not compromised: %+v", outcome)
			}
		})
	}
}

func stripLastActivationHMAC(t *testing.T, client redis.UniversalClient, key string) {
	t.Helper()
	ctx := context.Background()
	entries, err := client.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("read last event: count=%d err=%v", len(entries), err)
	}
	entry := entries[0]
	payload, ok := entry.Values["event"].(string)
	if !ok {
		t.Fatal("event is not JSON text")
	}
	var event audit.SIEMEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatal(err)
	}
	if event.HMAC == "" {
		t.Fatal("event was not signed")
	}
	event.HMAC = ""
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	entry.Values["event"] = string(data)
	if err := client.XDel(ctx, key, entry.ID).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: entry.Values}).Err(); err != nil {
		t.Fatal(err)
	}
}
