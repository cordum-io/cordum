package audit

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

const activationTestKey = "audit:chain-meta:hmac-activation"

func activationTestChainer(t *testing.T, cluster bool) (*Chainer, *redis.Client) {
	t.Helper()
	if cluster {
		f := newClusterChainFixture(t, WithHMACKey(testHMACKey()))
		return f.chainer, f.plain
	}
	c, _, client := newTestChainerWithHMAC(t)
	return c, client
}

func TestHMACActivationMarker(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "cluster"}[cluster], func(t *testing.T) {
			c, client := activationTestChainer(t, cluster)
			ctx := context.Background()
			const tenant = "hmac-activation"
			unsigned := NewChainer(c.client, c.keyPrefix)
			if err := unsigned.Append(ctx, baseEvent(tenant, "before")); err != nil {
				t.Fatal(err)
			}
			if n, err := client.Exists(ctx, activationTestKey).Result(); err != nil || n != 0 {
				t.Fatalf("unsigned writer created marker: exists=%d err=%v", n, err)
			}
			for i := 0; i < 3; i++ {
				if err := c.Append(ctx, baseEvent(tenant, "signed")); err != nil {
					t.Fatal(err)
				}
				if seq, err := client.HGet(ctx, activationTestKey, c.StreamKey(tenant)).Result(); err != nil || seq != "2" {
					t.Fatalf("activation seq=%q err=%v, want 2", seq, err)
				}
				// A fresh process must preserve the durable activation point.
				c = NewChainer(c.client, c.keyPrefix, WithHMACKey(testHMACKey()))
			}
			if err := c.Append(ctx, baseEvent("other", "signed")); err != nil {
				t.Fatal(err)
			}
			if seq, err := client.HGet(ctx, activationTestKey, c.StreamKey("other")).Result(); err != nil || seq != "1" {
				t.Fatalf("other tenant activation seq=%q err=%v, want 1", seq, err)
			}
		})
	}
}

func TestHMACActivationWriteFailurePreventsAppend(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "cluster"}[cluster], func(t *testing.T) {
			c, client := activationTestChainer(t, cluster)
			ctx := context.Background()
			if err := client.Set(ctx, activationTestKey, "wrong-type", 0).Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.Append(ctx, baseEvent("blocked", "signed")); err == nil {
				t.Error("append succeeded without durable activation")
			}
			if n, err := client.Exists(ctx, c.StreamKey("blocked"), c.HeadKey("blocked")).Result(); err != nil || n != 0 {
				t.Fatalf("failed append persisted chain data: keys=%d err=%v", n, err)
			}
			if err := client.Del(ctx, activationTestKey).Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.Append(ctx, baseEvent("blocked", "retry")); err != nil {
				t.Fatal(err)
			}
			if seq, err := client.HGet(ctx, activationTestKey, c.StreamKey("blocked")).Result(); err != nil || seq != "1" {
				t.Fatalf("retry activation seq=%q err=%v, want 1", seq, err)
			}
		})
	}
}
