package audit

import (
	"context"
	"encoding/json"
	"io"
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
				if i == 1 {
					c = NewChainer(c.client, c.keyPrefix, WithHMACKey(testHMACKey()))
				}
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

type activationJSON struct {
	Seq             int64  `json:"hmac_activated_seq"`
	Unauthenticated int    `json:"unauthenticated_events"`
	Integrity       string `json:"integrity"`
}

func TestHMACActivationVerification(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		before, signed          int
		strip, noKey, preWindow bool
		status                  VerifyStatus
		gap                     VerifyGapType
		want                    activationJSON
	}{
		{name: "never activated", before: 2, status: VerifyStatusOK, want: activationJSON{0, 2, "hash_chain_only"}},
		{name: "mixed history", before: 1, signed: 1, status: VerifyStatusOK, want: activationJSON{2, 1, "authenticated"}},
		{name: "history only window", before: 1, signed: 1, preWindow: true, status: VerifyStatusOK, want: activationJSON{2, 1, "hash_chain_only"}},
		{name: "missing signature", signed: 2, strip: true, status: VerifyStatusCompromised, gap: "hmac_missing", want: activationJSON{1, 0, "hash_chain_only"}},
		{name: "removed key", signed: 2, noKey: true, status: VerifyStatusPartial, gap: "hmac_unverified", want: activationJSON{1, 0, "hash_chain_only"}},
		{name: "removed key and all tags", signed: 1, strip: true, noKey: true, status: VerifyStatusPartial, gap: "hmac_unverified", want: activationJSON{1, 0, "hash_chain_only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, client := newTestChainerWithHMAC(t)
			ctx := context.Background()
			seedActivationHistory(t, c, tc.before, tc.signed)
			if tc.strip {
				chainSigningRemoveLastHMAC(t, ctx, client, c.StreamKey("tenant"))
			}
			opts := VerifyOptions{HMACKey: testHMACKey()}
			if tc.noKey {
				opts.HMACKey = nil
			}
			if tc.preWindow {
				opts.Limit = 1
			}
			result, err := VerifyChain(ctx, client, c.StreamKey("tenant"), opts)
			if err != nil {
				t.Fatal(err)
			}
			checkActivationResult(t, result, tc.status, tc.gap, tc.want)
		})
	}
}

func seedActivationHistory(t *testing.T, c *Chainer, before, signed int) {
	t.Helper()
	unsigned := NewChainer(c.client, c.keyPrefix)
	for i := 0; i < before+signed; i++ {
		writer := c
		if i < before {
			writer = unsigned
		}
		if err := writer.Append(context.Background(), baseEvent("tenant", "event")); err != nil {
			t.Fatal(err)
		}
	}
}

func checkActivationResult(t *testing.T, result *VerifyResult, status VerifyStatus, gap VerifyGapType, want activationJSON) {
	t.Helper()
	if result.Status != status {
		t.Errorf("status=%s want %s", result.Status, status)
	}
	if gap == "" {
		if len(result.Gaps) != 0 {
			t.Errorf("unexpected gaps: %+v", result.Gaps)
		}
	} else if len(result.Gaps) != 1 || result.Gaps[0].Type != gap {
		t.Errorf("gaps=%+v, want one %s", result.Gaps, gap)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got activationJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("activation report=%+v want %+v", got, want)
	}
}

func TestHMACActivationComplianceExport(t *testing.T) {
	for _, format := range []ComplianceExportFormat{ComplianceExportFormatJSON, ComplianceExportFormatCSV} {
		t.Run(string(format), func(t *testing.T) {
			c, _, client := newTestChainerWithHMAC(t)
			ctx := context.Background()
			seedActivationHistory(t, c, 0, 2)
			chainSigningRemoveLastHMAC(t, ctx, client, c.StreamKey("tenant"))
			manifest, err := WriteComplianceExport(ctx, io.Discard, client, ComplianceExportOptions{
				TenantID: "tenant", StreamKey: c.StreamKey("tenant"), Format: format, HMACKey: testHMACKey(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if manifest.ChainVerification == nil || manifest.EventCount != 2 {
				t.Fatalf("missing export verification or events: %+v", manifest)
			}
			checkActivationResult(t, manifest.ChainVerification, VerifyStatusCompromised, "hmac_missing", activationJSON{1, 0, "hash_chain_only"})
		})
	}
}

func TestHMACActivationInvalidMarkerFailsClosed(t *testing.T) {
	for _, marker := range []string{"invalid", "0", "-1"} {
		t.Run(marker, func(t *testing.T) {
			c, _, client := newTestChainerWithHMAC(t)
			ctx := context.Background()
			if err := client.HSet(ctx, activationTestKey, c.StreamKey("tenant"), marker).Err(); err != nil {
				t.Fatal(err)
			}
			result, err := VerifyChain(ctx, client, c.StreamKey("tenant"), VerifyOptions{})
			if err == nil || result != nil {
				t.Fatalf("invalid marker accepted: result=%+v err=%v", result, err)
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
