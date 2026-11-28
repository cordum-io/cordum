package replay

import (
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/redis/go-redis/v9"
)

const testLease = time.Minute

func claimTestMessage(t *testing.T, store *RedisReplayStore, digest string, expires time.Time) Claim {
	t.Helper()
	c, err := store.Claim("tenant-a", "sys.job.result", "worker-1", []byte("msg-1"), []byte(digest), expires, testLease)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	return c
}

// newClockedTestStore shares one miniredis with a store whose clock the test
// moves, so leases end without waiting.
func newClockedTestStore(t *testing.T, now *time.Time) (*RedisReplayStore, *miniredis.Miniredis) {
	t.Helper()
	_, srv := newTestStore(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisReplayStore(client, WithClock(func() time.Time { return *now })), srv
}

func TestReplayClaimSettleOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		outcome SettleOutcome
		want    ClaimState
	}{
		{name: "succeeded completes", outcome: SettleSucceeded, want: ClaimCompleted},
		{name: "failed completes", outcome: SettleFailed, want: ClaimCompleted},
		{name: "released is taken over at once", outcome: SettleReleased, want: ClaimAcquired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			first := claimTestMessage(t, store, "digest-1", expiry())
			if first.State != ClaimAcquired {
				t.Fatalf("first Claim() state = %v, want ClaimAcquired", first.State)
			}
			if err := store.Settle(first, tc.outcome); err != nil {
				t.Fatalf("Settle() error = %v", err)
			}
			if got := claimTestMessage(t, store, "digest-1", expiry()).State; got != tc.want {
				t.Fatalf("Claim() after settle state = %v, want %v", got, tc.want)
			}
		})
	}
}

// A live lease keeps a second delivery out; once it ends, the next delivery
// takes the message over and the first owner can no longer settle it.
func TestReplayClaimLeaseTakeoverFencesStaleOwner(t *testing.T) {
	now := time.Now()
	store, _ := newClockedTestStore(t, &now)
	expires := now.Add(time.Hour)
	stale := claimTestMessage(t, store, "digest-1", expires)
	busy := claimTestMessage(t, store, "digest-1", expires)
	if busy.State != ClaimBusy || busy.RetryAfter != testLease {
		t.Fatalf("Claim() during lease = %v/%v, want ClaimBusy/%v", busy.State, busy.RetryAfter, testLease)
	}
	now = now.Add(testLease)
	owner := claimTestMessage(t, store, "digest-1", expires)
	if owner.State != ClaimAcquired {
		t.Fatalf("Claim() after lease state = %v, want ClaimAcquired", owner.State)
	}
	if err := store.Settle(stale, SettleSucceeded); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale Settle() error = %v, want ErrClaimLost", err)
	}
	if got := claimTestMessage(t, store, "digest-1", expires).State; got != ClaimBusy {
		t.Fatalf("Claim() after stale settle state = %v, want ClaimBusy (owner kept)", got)
	}
	if err := store.Settle(owner, SettleSucceeded); err != nil {
		t.Fatalf("owner Settle() error = %v", err)
	}
	if got := claimTestMessage(t, store, "digest-1", expires).State; got != ClaimCompleted {
		t.Fatalf("Claim() after owner settle state = %v, want ClaimCompleted", got)
	}
}

func TestReplayClaimDigestChecks(t *testing.T) {
	cases := []struct {
		name    string
		record  func(*testing.T, *RedisReplayStore)
		digest  string
		want    ClaimState
		wantErr error
	}{
		{name: "admit record is completed", record: admitTestRecord, digest: "digest-1", want: ClaimCompleted},
		{name: "admit record with other digest conflicts", record: admitTestRecord, digest: "digest-2", wantErr: capsdk.ErrReplayConflict},
		{name: "claim with other digest conflicts", record: claimTestRecord, digest: "digest-2", wantErr: capsdk.ErrReplayConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			tc.record(t, store)
			got, err := store.Claim("tenant-a", "sys.job.result", "worker-1", []byte("msg-1"), []byte(tc.digest), expiry(), testLease)
			if !errors.Is(err, tc.wantErr) || got.State != tc.want {
				t.Fatalf("Claim() = %v/%v, want %v/%v", got.State, err, tc.want, tc.wantErr)
			}
		})
	}
}

func admitTestRecord(t *testing.T, store *RedisReplayStore) {
	t.Helper()
	if _, err := store.Admit("tenant-a", "sys.job.result", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry()); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
}

func claimTestRecord(t *testing.T, store *RedisReplayStore) {
	t.Helper()
	claimTestMessage(t, store, "digest-1", expiry())
}

// Takeover and settle rewrite the record; it must still expire with the packet.
func TestReplayClaimRewritesKeepPacketTTL(t *testing.T) {
	now := time.Now()
	store, srv := newClockedTestStore(t, &now)
	claimTestMessage(t, store, "digest-1", now.Add(2*testLease))
	now = now.Add(testLease)
	owner := claimTestMessage(t, store, "digest-1", now.Add(time.Hour))
	if err := store.Settle(owner, SettleSucceeded); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	srv.FastForward(2 * testLease)
	if got := claimTestMessage(t, store, "digest-1", now.Add(time.Hour)).State; got != ClaimAcquired {
		t.Fatalf("Claim() after packet expiry state = %v, want ClaimAcquired (record outlived packet)", got)
	}
}

func TestReplayClaimFailsClosedWhenStoreUnavailable(t *testing.T) {
	store, srv := newTestStore(t)
	owner := claimTestMessage(t, store, "digest-1", expiry())
	srv.SetError("ERR store unavailable")
	if _, err := store.Claim("tenant-a", "sys.job.result", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry(), testLease); !errors.Is(err, capsdk.ErrReplayStoreUnavailable) {
		t.Fatalf("Claim() error = %v, want ErrReplayStoreUnavailable", err)
	}
	if err := store.Settle(owner, SettleSucceeded); !errors.Is(err, capsdk.ErrReplayStoreUnavailable) {
		t.Fatalf("Settle() error = %v, want ErrReplayStoreUnavailable", err)
	}
}

func TestReplayClaimConcurrentClaimsAcquireOnce(t *testing.T) {
	store, _ := newTestStore(t)
	const goroutines = 20
	states := make(chan ClaimState, goroutines)
	errs := make(chan error, goroutines)
	start := make(chan struct{})
	for range goroutines {
		go func() {
			<-start
			c, err := store.Claim("tenant-a", "sys.job.result", "worker-1", []byte("msg-1"), []byte("digest-1"), expiry(), testLease)
			states <- c.State
			errs <- err
		}()
	}
	close(start)
	acquired := 0
	for range goroutines {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Claim() error = %v", err)
		}
		if <-states == ClaimAcquired {
			acquired++
		}
	}
	if acquired != 1 {
		t.Fatalf("concurrent Claim() acquired %d times, want exactly 1", acquired)
	}
}
