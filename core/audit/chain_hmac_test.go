package audit

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// testHMACKey returns a deterministic 32-byte key for reproducible tests.
func testHMACKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func newTestChainerWithHMAC(t *testing.T) (*Chainer, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())
	return NewChainer(client, "test:hmac:", WithHMACKey(testHMACKey())), mr, client
}

// ---------------------------------------------------------------------------
// Construction tests
// ---------------------------------------------------------------------------

func TestWithHMACKey_Enabled(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())

	c := NewChainer(client, "", WithHMACKey(testHMACKey()))
	if !c.HMACEnabled() {
		t.Fatal("HMACEnabled() should be true")
	}
}

func TestWithHMACKey_NilKeyDisabled(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())

	c := NewChainer(client, "", WithHMACKey(nil))
	if c.HMACEnabled() {
		t.Fatal("HMACEnabled() should be false for nil key")
	}
}

func TestWithHMACKey_EmptyKeyDisabled(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())

	c := NewChainer(client, "", WithHMACKey([]byte{}))
	if c.HMACEnabled() {
		t.Fatal("HMACEnabled() should be false for empty key")
	}
}

// BUG-013: short non-empty keys now PANIC inside WithHMACKey (callers cannot
// recover from a silent downgrade-to-no-HMAC). Pre-validation lives at
// gateway.go:1032 so a misconfigured CORDUM_AUDIT_HMAC_KEY yields a clean
// startup error rather than a panic-crash.
func TestWithHMACKey_ShortKeyPanics(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on short non-empty key")
		}
	}()
	_ = NewChainer(client, "", WithHMACKey([]byte("too-short")))
}

// ---------------------------------------------------------------------------
// Append + HMAC population tests
// ---------------------------------------------------------------------------

func TestHMAC_AppendPopulatesHMACField(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainerWithHMAC(t)
	ctx := context.Background()

	ev := baseEvent("tenant-hmac-a", "action-1")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	if ev.HMAC == "" {
		t.Fatal("HMAC should be populated after Append with HMAC key")
	}
	if len(ev.HMAC) != chainHashHexLen {
		t.Fatalf("HMAC length = %d, want %d", len(ev.HMAC), chainHashHexLen)
	}
}

func TestHMAC_AppendWithoutKeyNoHMAC(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainer(t) // no HMAC key
	ctx := context.Background()

	ev := baseEvent("tenant-no-hmac", "action-1")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	if ev.HMAC != "" {
		t.Fatalf("HMAC should be empty without key, got %q", ev.HMAC)
	}
}

func TestHMAC_VerifyEventHMACSuccess(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainerWithHMAC(t)
	ctx := context.Background()

	ev := baseEvent("tenant-hmac-verify", "verify-test")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	ok, err := VerifyEventHMAC(ev, testHMACKey())
	if err != nil {
		t.Fatalf("verify hmac: %v", err)
	}
	if !ok {
		t.Fatal("HMAC should verify successfully")
	}
}

func TestHMAC_VerifyEventHMACTamperedPayload(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainerWithHMAC(t)
	ctx := context.Background()

	ev := baseEvent("tenant-hmac-tamper", "original")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Tamper with the event payload.
	ev.Action = "tampered"

	ok, err := VerifyEventHMAC(ev, testHMACKey())
	if err != nil {
		t.Fatalf("verify hmac: %v", err)
	}
	if ok {
		t.Fatal("HMAC should fail after payload tampering")
	}
}

func TestHMAC_VerifyEventHMACWrongKey(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainerWithHMAC(t)
	ctx := context.Background()

	ev := baseEvent("tenant-hmac-wrongkey", "test")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	wrongKey := make([]byte, 32)
	for i := range wrongKey {
		wrongKey[i] = byte(i + 100)
	}

	ok, err := VerifyEventHMAC(ev, wrongKey)
	if err != nil {
		t.Fatalf("verify hmac: %v", err)
	}
	if ok {
		t.Fatal("HMAC should fail with wrong key")
	}
}

func TestHMAC_VerifyEventHMACSkippedWhenEmpty(t *testing.T) {
	t.Parallel()
	ev := &SIEMEvent{
		EventType: EventSafetyDecision,
		TenantID:  "tenant-x",
		Action:    "test",
		// No HMAC set
	}

	ok, err := VerifyEventHMAC(ev, testHMACKey())
	if err != nil {
		t.Fatalf("verify hmac: %v", err)
	}
	if !ok {
		t.Fatal("should return true when event has no HMAC (backward compat)")
	}
}

func TestHMAC_VerifyEventHMACSkippedWhenNoKey(t *testing.T) {
	t.Parallel()
	ev := &SIEMEvent{
		EventType: EventSafetyDecision,
		TenantID:  "tenant-x",
		Action:    "test",
		HMAC:      "deadbeef",
	}

	ok, err := VerifyEventHMAC(ev, nil)
	if err != nil {
		t.Fatalf("verify hmac: %v", err)
	}
	if !ok {
		t.Fatal("should return true when no key provided")
	}
}

// ---------------------------------------------------------------------------
// Chain walk with HMAC verification
// ---------------------------------------------------------------------------

func TestHMAC_VerifyChainWithHMAC(t *testing.T) {
	t.Parallel()
	c, _, client := newTestChainerWithHMAC(t)
	ctx := context.Background()

	const n = 10
	for i := 0; i < n; i++ {
		ev := baseEvent("tenant-verify-hmac", "action")
		if err := c.Append(ctx, ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
	}

	result, err := VerifyChain(ctx, client, c.StreamKey("tenant-verify-hmac"), VerifyOptions{
		HMACKey: testHMACKey(),
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Status != VerifyStatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if result.HMACVerified != n {
		t.Fatalf("HMACVerified = %d, want %d", result.HMACVerified, n)
	}
	if result.HMACSkipped != 0 {
		t.Fatalf("HMACSkipped = %d, want 0", result.HMACSkipped)
	}
}

func TestHMAC_VerifyChainDetectsTamperedHMAC(t *testing.T) {
	t.Parallel()
	c, _, client := newTestChainerWithHMAC(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		ev := baseEvent("tenant-tamper-hmac", "action")
		if err := c.Append(ctx, ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
	}

	// Verify with wrong key to simulate attacker re-sign attempt.
	wrongKey := make([]byte, 32)
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatalf("rand: %v", err)
	}

	result, err := VerifyChain(ctx, client, c.StreamKey("tenant-tamper-hmac"), VerifyOptions{
		HMACKey: wrongKey,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Status != VerifyStatusCompromised {
		t.Fatalf("status = %q, want compromised", result.Status)
	}

	// All 5 events should have HMAC mismatch gaps.
	hmacGaps := 0
	for _, g := range result.Gaps {
		if g.Type == GapTypeHMACMismatch {
			hmacGaps++
		}
	}
	if hmacGaps != 5 {
		t.Fatalf("hmac_mismatch gaps = %d, want 5", hmacGaps)
	}
}

func TestHMAC_VerifyChainMixedPreHMACEvents(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())
	ctx := context.Background()

	// Phase 1: append 3 events WITHOUT HMAC.
	cNoHMAC := NewChainer(client, "test:mixed:")
	for i := 0; i < 3; i++ {
		ev := baseEvent("tenant-mixed", "pre-hmac")
		if err := cNoHMAC.Append(ctx, ev); err != nil {
			t.Fatalf("append pre-hmac[%d]: %v", i, err)
		}
	}

	// Phase 2: append 3 events WITH HMAC (same chain, key rollout).
	cWithHMAC := NewChainer(client, "test:mixed:", WithHMACKey(testHMACKey()))
	for i := 0; i < 3; i++ {
		ev := baseEvent("tenant-mixed", "post-hmac")
		if err := cWithHMAC.Append(ctx, ev); err != nil {
			t.Fatalf("append post-hmac[%d]: %v", i, err)
		}
	}

	// Verify with HMAC key — pre-HMAC events should be skipped, not failed.
	result, err := VerifyChain(ctx, client, cNoHMAC.StreamKey("tenant-mixed"), VerifyOptions{
		HMACKey: testHMACKey(),
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Status != VerifyStatusOK {
		t.Fatalf("status = %q, want ok (mixed chain should pass)", result.Status)
	}
	if result.HMACVerified != 3 {
		t.Fatalf("HMACVerified = %d, want 3", result.HMACVerified)
	}
	if result.HMACSkipped != 3 {
		t.Fatalf("HMACSkipped = %d, want 3", result.HMACSkipped)
	}
}

// ---------------------------------------------------------------------------
// Concurrent HMAC append
// ---------------------------------------------------------------------------

func TestHMAC_ConcurrentAppendMonotonic(t *testing.T) {
	t.Parallel()
	c, _, client := newTestChainerWithHMAC(t)
	ctx := context.Background()

	const (
		producers   = 8
		perProducer = 20
		total       = producers * perProducer
	)

	var (
		wg       sync.WaitGroup
		errCount atomic.Int64
	)
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				ev := baseEvent("tenant-hmac-concurrent", "concurrent")
				if err := c.Append(ctx, ev); err != nil {
					t.Errorf("append: %v", err)
					errCount.Add(1)
					return
				}
			}
		}()
	}
	wg.Wait()
	if errCount.Load() != 0 {
		t.Fatalf("%d goroutines failed", errCount.Load())
	}

	// Read the full stream and verify chain + HMAC integrity.
	entries, err := client.XRange(ctx, c.StreamKey("tenant-hmac-concurrent"), "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if len(entries) != total {
		t.Fatalf("stream length = %d, want %d", len(entries), total)
	}

	var prevHash string
	for i, e := range entries {
		payload, ok := e.Values[chainStreamFieldEvent].(string)
		if !ok {
			t.Fatalf("entry[%d] missing event field", i)
		}
		var got SIEMEvent
		if err := json.Unmarshal([]byte(payload), &got); err != nil {
			t.Fatalf("unmarshal[%d]: %v", i, err)
		}

		if int(got.Seq) != i+1 {
			t.Fatalf("Seq[%d] = %d, want %d (gap or reorder)", i, got.Seq, i+1)
		}
		if got.PrevHash != prevHash {
			t.Fatalf("PrevHash[%d] = %q, want %q", i, got.PrevHash, prevHash)
		}

		// Verify SHA-256 hash.
		ok, err := VerifyEventHash(&got)
		if err != nil {
			t.Fatalf("verify hash[%d]: %v", i, err)
		}
		if !ok {
			t.Fatalf("event[%d] hash did not verify", i)
		}

		// Verify HMAC.
		if got.HMAC == "" {
			t.Fatalf("event[%d] HMAC is empty", i)
		}
		ok, err = VerifyEventHMAC(&got, testHMACKey())
		if err != nil {
			t.Fatalf("verify hmac[%d]: %v", i, err)
		}
		if !ok {
			t.Fatalf("event[%d] HMAC did not verify", i)
		}

		prevHash = got.EventHash
	}
}

// ---------------------------------------------------------------------------
// Determinism tests
// ---------------------------------------------------------------------------

func TestHMAC_ComputeDeterministic(t *testing.T) {
	t.Parallel()
	ev := &SIEMEvent{
		Timestamp: time.Unix(1700000000, 0).UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "tenant-det",
		Action:    "test",
		PrevHash:  "abcdef",
		Extra:     map[string]string{"b": "2", "a": "1"},
	}

	h1, err := computeEventHMAC(ev, testHMACKey())
	if err != nil {
		t.Fatalf("hmac: %v", err)
	}

	// Mutating Seq/EventHash/HMAC must not change the computed HMAC.
	ev.Seq = 42
	ev.EventHash = "junk"
	ev.HMAC = "garbage"
	h2, err := computeEventHMAC(ev, testHMACKey())
	if err != nil {
		t.Fatalf("hmac2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("HMAC not stable across Seq/EventHash/HMAC mutation: %q vs %q", h1, h2)
	}

	// Changing PrevHash MUST change the HMAC.
	ev.PrevHash = "different"
	h3, err := computeEventHMAC(ev, testHMACKey())
	if err != nil {
		t.Fatalf("hmac3: %v", err)
	}
	if h1 == h3 {
		t.Fatal("HMAC should change when PrevHash changes")
	}

	// Different key MUST produce different HMAC.
	altKey := make([]byte, 32)
	for i := range altKey {
		altKey[i] = byte(i + 100)
	}
	ev.PrevHash = "abcdef"
	h4, err := computeEventHMAC(ev, altKey)
	if err != nil {
		t.Fatalf("hmac4: %v", err)
	}
	if h1 == h4 {
		t.Fatal("HMAC should differ with different key")
	}
}

// ---------------------------------------------------------------------------
// Hash backward compatibility — ensure existing hash computation is not
// broken by the HMAC field addition.
// ---------------------------------------------------------------------------

func TestHMAC_ExistingHashUnchangedByHMACField(t *testing.T) {
	t.Parallel()
	ev := &SIEMEvent{
		Timestamp: time.Unix(1700000000, 0).UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "tenant-compat",
		Action:    "x",
		PrevHash:  "deadbeef",
	}

	// Compute hash without HMAC field set.
	h1, err := computeEventHash(ev)
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}

	// Set the HMAC field and recompute — should be identical because
	// computeEventHash clears HMAC before marshalling.
	ev.HMAC = "some-hmac-value"
	h2, err := computeEventHash(ev)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash changed when HMAC field set: %q vs %q", h1, h2)
	}
}
