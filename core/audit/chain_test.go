package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func newTestChainer(t *testing.T) (*Chainer, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())
	return NewChainer(client, "test:chain:"), mr, client
}

type redisGetCounterHook struct {
	gets atomic.Int64
}

func (h *redisGetCounterHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *redisGetCounterHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.FullName() == "get" {
			h.gets.Add(1)
		}
		return next(ctx, cmd)
	}
}

func (h *redisGetCounterHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if cmd.FullName() == "get" {
				h.gets.Add(1)
			}
		}
		return next(ctx, cmds)
	}
}

func baseEvent(tenant, action string) *SIEMEvent {
	return &SIEMEvent{
		Timestamp: time.Unix(1700000000, 0).UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  tenant,
		Action:    action,
	}
}

func TestChainer_AppendWarmCacheAvoidsHeadGET(t *testing.T) {
	t.Parallel()
	c, _, client := newTestChainer(t)
	hook := &redisGetCounterHook{}
	client.AddHook(hook)
	ctx := context.Background()

	first := baseEvent("tenant-warm-cache", "first")
	if err := c.Append(ctx, first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	getsAfterFirst := hook.gets.Load()
	if getsAfterFirst == 0 {
		t.Fatal("expected first append to read the Redis head at least once")
	}

	second := baseEvent("tenant-warm-cache", "second")
	if err := c.Append(ctx, second); err != nil {
		t.Fatalf("append second: %v", err)
	}
	if second.Seq != 2 {
		t.Fatalf("second Seq = %d, want 2", second.Seq)
	}
	if second.PrevHash != first.EventHash {
		t.Fatalf("second PrevHash = %q, want first EventHash %q", second.PrevHash, first.EventHash)
	}
	if got := hook.gets.Load(); got != getsAfterFirst {
		t.Fatalf("warm append performed an extra Redis GET: got %d GETs, want %d", got, getsAfterFirst)
	}
}

func TestChainer_AppendWarmCacheSurvivesExternalWriter(t *testing.T) {
	t.Parallel()
	chainerA, _, client := newTestChainer(t)
	chainerB := NewChainer(client, "test:chain:")
	ctx := context.Background()
	const tenant = "tenant-cache-external"

	aFirst := baseEvent(tenant, "a-first")
	if err := chainerA.Append(ctx, aFirst); err != nil {
		t.Fatalf("append a first: %v", err)
	}

	bSecond := baseEvent(tenant, "b-second")
	if err := chainerB.Append(ctx, bSecond); err != nil {
		t.Fatalf("append b second: %v", err)
	}

	aThird := baseEvent(tenant, "a-third")
	if err := chainerA.Append(ctx, aThird); err != nil {
		t.Fatalf("append a third: %v", err)
	}
	if aThird.Seq != 3 {
		t.Fatalf("a third Seq = %d, want 3", aThird.Seq)
	}
	if aThird.PrevHash != bSecond.EventHash {
		t.Fatalf("a third PrevHash = %q, want b second EventHash %q", aThird.PrevHash, bSecond.EventHash)
	}

	entries, err := client.XRange(ctx, chainerA.StreamKey(tenant), "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("stream length = %d, want 3", len(entries))
	}
	prevHash := ""
	for i, entry := range entries {
		payload, ok := entry.Values[chainStreamFieldEvent].(string)
		if !ok {
			t.Fatalf("entry[%d] missing event field", i)
		}
		var got SIEMEvent
		if err := json.Unmarshal([]byte(payload), &got); err != nil {
			t.Fatalf("unmarshal[%d]: %v", i, err)
		}
		if got.Seq != int64(i+1) {
			t.Fatalf("Seq[%d] = %d, want %d", i, got.Seq, i+1)
		}
		if got.PrevHash != prevHash {
			t.Fatalf("PrevHash[%d] = %q, want %q", i, got.PrevHash, prevHash)
		}
		ok, err := VerifyEventHash(&got)
		if err != nil {
			t.Fatalf("verify[%d]: %v", i, err)
		}
		if !ok {
			t.Fatalf("event[%d] hash did not verify", i)
		}
		prevHash = got.EventHash
	}
}

func TestChainer_AppendGenesisEvent(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainer(t)
	ctx := context.Background()

	ev := baseEvent("tenant-a", "first")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	if ev.Seq != 1 {
		t.Errorf("Seq = %d, want 1", ev.Seq)
	}
	if ev.PrevHash != "" {
		t.Errorf("PrevHash = %q, want empty for genesis", ev.PrevHash)
	}
	if len(ev.EventHash) != chainHashHexLen {
		t.Errorf("EventHash length = %d, want %d", len(ev.EventHash), chainHashHexLen)
	}
}

func TestChainer_AppendMonotonicSingleThreaded(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainer(t)
	ctx := context.Background()

	const n = 50
	hashes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ev := baseEvent("tenant-b", "a")
		if err := c.Append(ctx, ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
		if int(ev.Seq) != i+1 {
			t.Fatalf("Seq[%d] = %d, want %d", i, ev.Seq, i+1)
		}
		if i == 0 {
			if ev.PrevHash != "" {
				t.Fatalf("genesis PrevHash should be empty, got %q", ev.PrevHash)
			}
		} else if ev.PrevHash != hashes[i-1] {
			t.Fatalf("PrevHash[%d] = %q, want %q", i, ev.PrevHash, hashes[i-1])
		}
		hashes = append(hashes, ev.EventHash)
	}
}

func TestChainer_AppendRejectsMissingTenant(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainer(t)
	ctx := context.Background()

	ev := &SIEMEvent{EventType: EventSafetyDecision, Action: "x"}
	err := c.Append(ctx, ev)
	if !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("err = %v, want ErrTenantRequired", err)
	}
}

func TestChainer_AppendNilEvent(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainer(t)
	if err := c.Append(context.Background(), nil); !errors.Is(err, ErrNilEvent) {
		t.Fatalf("err = %v, want ErrNilEvent", err)
	}
}

func TestChainer_AppendTenantIsolation(t *testing.T) {
	t.Parallel()
	c, _, _ := newTestChainer(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ev := baseEvent("tenant-iso-a", "x")
		if err := c.Append(ctx, ev); err != nil {
			t.Fatalf("append a[%d]: %v", i, err)
		}
	}
	ev := baseEvent("tenant-iso-b", "y")
	if err := c.Append(ctx, ev); err != nil {
		t.Fatalf("append b: %v", err)
	}
	if ev.Seq != 1 {
		t.Errorf("tenant-iso-b Seq = %d, want 1 (tenants isolated)", ev.Seq)
	}
	if ev.PrevHash != "" {
		t.Errorf("tenant-iso-b PrevHash = %q, want empty (tenants isolated)", ev.PrevHash)
	}
}

func TestChainer_AppendConcurrentMonotonic(t *testing.T) {
	t.Parallel()
	c, _, client := newTestChainer(t)
	ctx := context.Background()

	const (
		producers     = 16
		perProducer   = 25
		totalExpected = producers * perProducer
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
				ev := baseEvent("tenant-cc", "concurrent")
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

	// Read the full stream and assert seqs are 1..N with no gaps and
	// every prev_hash points to its predecessor's event_hash.
	entries, err := client.XRange(ctx, c.StreamKey("tenant-cc"), "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if len(entries) != totalExpected {
		t.Fatalf("stream length = %d, want %d", len(entries), totalExpected)
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
		ok, err := VerifyEventHash(&got)
		if err != nil {
			t.Fatalf("verify[%d]: %v", i, err)
		}
		if !ok {
			t.Fatalf("event[%d] hash did not verify", i)
		}
		prevHash = got.EventHash
	}
}

func TestComputeEventHash_Deterministic(t *testing.T) {
	t.Parallel()
	ev := &SIEMEvent{
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "tenant-1",
		Action:    "x",
		Extra:     map[string]string{"b": "2", "a": "1"},
		PrevHash:  "deadbeef",
	}
	h1, err := computeEventHash(ev)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Mutating Seq/EventHash must not change the computed hash — those
	// fields are cleared before hashing.
	ev.Seq = 99
	ev.EventHash = "junk"
	h2, err := computeEventHash(ev)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash not stable across Seq/EventHash mutation: %q vs %q", h1, h2)
	}
	// Changing PrevHash MUST change the hash — that's what gives the
	// chain forward tamper propagation.
	ev.PrevHash = "cafebabe"
	h3, err := computeEventHash(ev)
	if err != nil {
		t.Fatalf("hash3: %v", err)
	}
	if h1 == h3 {
		t.Fatalf("hash did not change when PrevHash changed")
	}
}

func TestParseChainHead(t *testing.T) {
	t.Parallel()
	seq, hash, err := parseChainHead("")
	if err != nil || seq != 0 || hash != "" {
		t.Errorf("empty: seq=%d hash=%q err=%v", seq, hash, err)
	}

	long := make([]byte, chainHashHexLen)
	for i := range long {
		long[i] = 'a'
	}
	seq, hash, err = parseChainHead("42:" + string(long))
	if err != nil || seq != 42 || hash != string(long) {
		t.Errorf("parse: seq=%d hash=%q err=%v", seq, hash, err)
	}

	if _, _, err := parseChainHead("bad"); err == nil {
		t.Errorf("expected error for malformed head")
	}
	if _, _, err := parseChainHead("x:deadbeef"); err == nil {
		t.Errorf("expected error for non-numeric seq")
	}
	if _, _, err := parseChainHead("-1:" + string(long)); err == nil {
		t.Errorf("expected error for negative seq")
	}
	if _, _, err := parseChainHead("1:short"); err == nil {
		t.Errorf("expected error for truncated hash")
	}
}

// BUG-013: WithHMACKey used to slog.Error + silently set hmacKey=nil for keys
// shorter than 32 bytes. Callers cannot recover, and a silent downgrade
// weakens the chain invisibly. Now PANICS on 0<len<32; empty key still
// disables cleanly; 32+ byte key still works.
func TestWithHMACKey_PanicsOnShortNonEmptyKey(t *testing.T) {
	t.Run("short_key_panics", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic on 16-byte key")
			}
			msg, ok := r.(string)
			if !ok {
				t.Fatalf("panic value type = %T, want string; got %v", r, r)
			}
			if !strings.Contains(msg, "at least 32 bytes") {
				t.Fatalf("panic message missing length requirement: %q", msg)
			}
		}()
		c := &Chainer{}
		WithHMACKey([]byte("16-byte-key-xxxx"))(c)
	})

	t.Run("empty_key_disables_silently", func(t *testing.T) {
		c := &Chainer{hmacKey: []byte("preexisting")}
		WithHMACKey(nil)(c)
		if c.hmacKey != nil {
			t.Fatalf("empty key should disable HMAC, got %v", c.hmacKey)
		}
	})

	t.Run("valid_32_byte_key_enabled", func(t *testing.T) {
		key := make([]byte, 32)
		for i := range key {
			key[i] = byte(i)
		}
		c := &Chainer{}
		WithHMACKey(key)(c)
		if len(c.hmacKey) != 32 {
			t.Fatalf("valid key not stored: got %d bytes", len(c.hmacKey))
		}
	})
}
