package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestChainSigningRequiresEveryLaterHMAC(t *testing.T) {
	for _, unsigned := range []bool{false, true} {
		name := "all_signed"
		if unsigned {
			name = "later_event_requires_signature"
		}
		t.Run(name, func(t *testing.T) { chainSigningCheck(t, unsigned) })
	}
}

func chainSigningCheck(t *testing.T, unsigned bool) {
	t.Helper()
	chainer, _, client := newTestChainerWithHMAC(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const tenant = "signing-tenant"
	for i := 0; i < 2; i++ {
		if err := chainer.Append(ctx, baseEvent(tenant, "recorded-action")); err != nil {
			t.Fatal(err)
		}
	}
	key := chainer.StreamKey(tenant)
	if unsigned {
		chainSigningRemoveLastHMAC(t, ctx, client, key)
	}
	result, err := VerifyChain(ctx, client, key, VerifyOptions{HMACKey: testHMACKey()})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalEvents != 2 {
		t.Fatalf("verified %d events, want 2", result.TotalEvents)
	}
	if unsigned && result.Status == VerifyStatusOK {
		t.Errorf("unsigned event after signing began must not verify ok: status=%s hmac_skipped=%d", result.Status, result.HMACSkipped)
	} else if !unsigned && (result.Status != VerifyStatusOK || result.HMACVerified != 2) {
		t.Errorf("signed chain: status=%s hmac_verified=%d, want ok/2", result.Status, result.HMACVerified)
	}
}

func chainSigningRemoveLastHMAC(t *testing.T, ctx context.Context, client *redis.Client, key string) {
	t.Helper()
	entries, err := client.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("read last event: count=%d err=%v", len(entries), err)
	}
	entry := entries[0]
	payload, ok := entry.Values[chainStreamFieldEvent].(string)
	if !ok {
		t.Fatal("stored event is not JSON text")
	}
	var event SIEMEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatal(err)
	}
	if event.HMAC == "" {
		t.Fatal("append did not sign the event")
	}
	event.HMAC = ""
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	entry.Values[chainStreamFieldEvent] = string(data)
	if err := client.XDel(ctx, key, entry.ID).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: entry.Values}).Err(); err != nil {
		t.Fatal(err)
	}
}
