package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/redis/go-redis/v9"
)

func TestAuditChainSenderChainsAndForwards(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()

	chainer := audit.NewChainer(client, "")
	downstream := &testAuditSender{}
	sender := newAuditChainSender(chainer, downstream)

	sender.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSafetyApproval,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "approve",
		Reason:    "smoke approval",
	})

	if downstream.Len() != 1 {
		t.Fatalf("downstream event count = %d, want 1", downstream.Len())
	}
	forwarded := downstream.Get(0)
	if forwarded.Seq != 1 || forwarded.EventHash == "" {
		t.Fatalf("forwarded event missing chain fields: seq=%d hash=%q", forwarded.Seq, forwarded.EventHash)
	}

	result, err := audit.VerifyChain(context.Background(), client, chainer.StreamKey("default"), audit.VerifyOptions{})
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if result.Status != audit.VerifyStatusOK || result.TotalEvents != 1 {
		t.Fatalf("verify result = status %q total %d, want ok/1", result.Status, result.TotalEvents)
	}
}

func TestAuditChainSenderSkipsTenantlessEventsButForwards(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()

	downstream := &testAuditSender{}
	sender := newAuditChainSender(audit.NewChainer(client, ""), downstream)

	sender.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSystemAuth,
		Severity:  audit.SeverityMedium,
		Action:    "auth.failure",
	})

	if downstream.Len() != 1 {
		t.Fatalf("downstream event count = %d, want 1", downstream.Len())
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("tenantless event should not create audit chain keys, got %v", keys)
	}
}
