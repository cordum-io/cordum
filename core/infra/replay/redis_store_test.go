package replay

import (
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*RedisReplayStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(srv.Close)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisReplayStore(client), srv
}

func expiry() time.Time { return time.Now().Add(5 * time.Minute) }

func TestAdmitFirstDeliveryIsAccepted(t *testing.T) {
	store, _ := newTestStore(t)
	got, err := store.Admit("tenant-a", "sys.job.request", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry())
	if err != nil {
		t.Fatalf("Admit() error = %v, want nil", err)
	}
	if got != capsdk.ReplayOutcomeFirst {
		t.Fatalf("Admit() = %v, want ReplayOutcomeFirst", got)
	}
}

// Identical JetStream redelivery must be reported as a duplicate so the
// caller ACKs it without re-running the handler — not as a conflict.
func TestAdmitIdenticalRedeliveryIsDuplicate(t *testing.T) {
	store, _ := newTestStore(t)
	args := func() (string, string, string, []byte, []byte, time.Time) {
		return "tenant-a", "sys.job.request", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry()
	}
	if _, err := store.Admit(args()); err != nil {
		t.Fatalf("first Admit() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		got, err := store.Admit(args())
		if err != nil {
			t.Fatalf("redelivery %d Admit() error = %v, want nil", i, err)
		}
		if got != capsdk.ReplayOutcomeDuplicate {
			t.Fatalf("redelivery %d Admit() = %v, want ReplayOutcomeDuplicate", i, got)
		}
	}
}

// Same message ID with a different signed body is a replay/forgery attempt and
// must fail closed, never be treated as a benign duplicate.
func TestAdmitSameMessageIDDifferentDigestConflicts(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Admit("tenant-a", "sys.job.request", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry()); err != nil {
		t.Fatalf("first Admit() error = %v", err)
	}
	got, err := store.Admit("tenant-a", "sys.job.request", "worker-1", []byte("msg-1"), []byte("digest-2"), expiry())
	if !errors.Is(err, capsdk.ErrReplayConflict) {
		t.Fatalf("conflicting Admit() error = %v, want ErrReplayConflict", err)
	}
	if got == capsdk.ReplayOutcomeFirst {
		t.Fatal("conflicting Admit() returned ReplayOutcomeFirst")
	}
}

// The replay identity is the full tuple. Changing any component must yield an
// independent first-delivery, otherwise a message replayed onto a different
// subject or from a different sender would be silently suppressed.
func TestAdmitScopesByFullTuple(t *testing.T) {
	base := struct {
		tenant, audience, sender string
		messageID                []byte
	}{"tenant-a", "sys.job.request", "worker-1", []byte("msg-1")}

	variants := map[string]struct {
		tenant, audience, sender string
		messageID                []byte
	}{
		"different tenant":    {"tenant-b", base.audience, base.sender, base.messageID},
		"different audience":  {base.tenant, "sys.job.result", base.sender, base.messageID},
		"different sender":    {base.tenant, base.audience, "worker-2", base.messageID},
		"different messageID": {base.tenant, base.audience, base.sender, []byte("msg-2")},
	}
	for name, v := range variants {
		store, _ := newTestStore(t)
		if _, err := store.Admit(base.tenant, base.audience, base.sender, base.messageID, []byte("d"), expiry()); err != nil {
			t.Fatalf("%s: base Admit() error = %v", name, err)
		}
		got, err := store.Admit(v.tenant, v.audience, v.sender, v.messageID, []byte("d"), expiry())
		if err != nil {
			t.Fatalf("%s: variant Admit() error = %v", name, err)
		}
		if got != capsdk.ReplayOutcomeFirst {
			t.Fatalf("%s: variant Admit() = %v, want ReplayOutcomeFirst", name, got)
		}
	}
}

// Tenant/sender/subject identifiers may legitimately contain the ':' used as a
// Redis key separator. Any join-based encoding is ambiguous across ADJACENT
// tuple components: {tenant "a", audience "b:c"} and {tenant "a:b", audience
// "c"} both render as "a:b:c", letting one tenant suppress another tenant's
// messages. Each pair below is chosen to collide under a naive join, so the
// test fails unless the encoding is injective.
func TestAdmitKeyEncodingIsDelimiterSafe(t *testing.T) {
	cases := map[string]struct {
		firstTenant, firstAudience, firstSender    string
		firstMsg                                   []byte
		secondTenant, secondAudience, secondSender string
		secondMsg                                  []byte
	}{
		"tenant/audience boundary": {
			"a", "b:c", "s", []byte("m"),
			"a:b", "c", "s", []byte("m"),
		},
		"audience/sender boundary": {
			"t", "a", "b:c", []byte("m"),
			"t", "a:b", "c", []byte("m"),
		},
		"sender/messageID boundary": {
			"t", "a", "s", []byte(":m"),
			"t", "a", "s:", []byte("m"),
		},
	}
	for name, c := range cases {
		store, _ := newTestStore(t)
		if _, err := store.Admit(c.firstTenant, c.firstAudience, c.firstSender, c.firstMsg, []byte("digest-1"), expiry()); err != nil {
			t.Fatalf("%s: first Admit() error = %v", name, err)
		}
		got, err := store.Admit(c.secondTenant, c.secondAudience, c.secondSender, c.secondMsg, []byte("digest-1"), expiry())
		if err != nil {
			t.Fatalf("%s: colliding-shape Admit() error = %v, want nil (distinct identity)", name, err)
		}
		if got != capsdk.ReplayOutcomeFirst {
			t.Fatalf("%s: colliding-shape Admit() = %v, want ReplayOutcomeFirst; key encoding is ambiguous", name, got)
		}
	}
}

// Store unavailability must surface as ErrReplayStoreUnavailable so the raw
// admission boundary retries instead of admitting unverified traffic.
func TestAdmitFailsClosedWhenStoreUnavailable(t *testing.T) {
	store, srv := newTestStore(t)
	srv.Close()
	got, err := store.Admit("tenant-a", "sys.job.request", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry())
	if !errors.Is(err, capsdk.ErrReplayStoreUnavailable) {
		t.Fatalf("Admit() with dead store error = %v, want ErrReplayStoreUnavailable", err)
	}
	if got == capsdk.ReplayOutcomeFirst {
		t.Fatal("Admit() admitted a packet while the replay store was unavailable")
	}
}

func TestAdmitRejectsIncompleteIdentity(t *testing.T) {
	store, _ := newTestStore(t)
	cases := map[string]struct {
		tenant, audience, sender string
		messageID, digest        []byte
	}{
		"empty tenant":    {"", "sys.job.request", "worker-1", []byte("m"), []byte("d")},
		"empty audience":  {"t", "", "worker-1", []byte("m"), []byte("d")},
		"empty sender":    {"t", "sys.job.request", "", []byte("m"), []byte("d")},
		"empty messageID": {"t", "sys.job.request", "worker-1", nil, []byte("d")},
		"empty digest":    {"t", "sys.job.request", "worker-1", []byte("m"), nil},
	}
	for name, c := range cases {
		got, err := store.Admit(c.tenant, c.audience, c.sender, c.messageID, c.digest, expiry())
		if err == nil {
			t.Fatalf("%s: Admit() error = nil, want rejection", name)
		}
		if got == capsdk.ReplayOutcomeFirst {
			t.Fatalf("%s: Admit() admitted an incomplete identity", name)
		}
	}
}

// An already-expired packet must never consume a replay slot: accepting it
// would let an attacker pre-seed entries, and the TTL would be non-positive.
func TestAdmitRejectsAlreadyExpiredPacket(t *testing.T) {
	store, _ := newTestStore(t)
	got, err := store.Admit("t", "sys.job.request", "w", []byte("m"), []byte("d"), time.Now().Add(-time.Second))
	if err == nil {
		t.Fatal("Admit() with past expiry error = nil, want rejection")
	}
	if got == capsdk.ReplayOutcomeFirst {
		t.Fatal("Admit() accepted an already-expired packet")
	}
}

// The entry must not outlive the packet's validity window, otherwise the
// replay set grows without bound.
func TestAdmitEntryExpiresWithPacket(t *testing.T) {
	store, srv := newTestStore(t)
	if _, err := store.Admit("t", "sys.job.request", "w", []byte("m"), []byte("d"), time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	srv.FastForward(2 * time.Minute)
	got, err := store.Admit("t", "sys.job.request", "w", []byte("m"), []byte("d2"), expiry())
	if err != nil {
		t.Fatalf("post-expiry Admit() error = %v, want nil", err)
	}
	if got != capsdk.ReplayOutcomeFirst {
		t.Fatalf("post-expiry Admit() = %v, want ReplayOutcomeFirst (entry outlived packet)", got)
	}
}

// Concurrent replicas racing the same message must produce exactly one
// first-delivery; anything else means the handler could run twice.
func TestAdmitConcurrentRaceYieldsExactlyOneFirst(t *testing.T) {
	store, _ := newTestStore(t)
	const goroutines = 32
	results := make(chan capsdk.ReplayOutcome, goroutines)
	errs := make(chan error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			out, err := store.Admit("t", "sys.job.request", "w", []byte("msg"), []byte("digest"), expiry())
			results <- out
			errs <- err
		}()
	}
	close(start)

	firsts, duplicates := 0, 0
	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Admit() error = %v", err)
		}
		switch <-results {
		case capsdk.ReplayOutcomeFirst:
			firsts++
		case capsdk.ReplayOutcomeDuplicate:
			duplicates++
		}
	}
	if firsts != 1 {
		t.Fatalf("concurrent Admit() produced %d first-deliveries, want exactly 1", firsts)
	}
	if duplicates != goroutines-1 {
		t.Fatalf("concurrent Admit() produced %d duplicates, want %d", duplicates, goroutines-1)
	}
}

func TestNewRedisReplayStoreRejectsNilClient(t *testing.T) {
	store := NewRedisReplayStore(nil)
	if store == nil {
		t.Fatal("NewRedisReplayStore(nil) = nil, want a fail-closed store")
	}
	if _, err := store.Admit("t", "a", "s", []byte("m"), []byte("d"), expiry()); !errors.Is(err, capsdk.ErrReplayStoreUnavailable) {
		t.Fatalf("Admit() on nil-client store error = %v, want ErrReplayStoreUnavailable", err)
	}
}

// Compile-time proof the store satisfies the interface the admission boundary
// consumes.
var _ capsdk.ReplayStore = (*RedisReplayStore)(nil)
