package shadow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/redis/go-redis/v9"
)

func newExceptionCapTestStore(t *testing.T, poolSize int) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: poolSize})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })

	now := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	var idCounter atomic.Int64
	s, err := NewRedisStore(client,
		WithClock(func() time.Time { return now }),
		WithIDGen(func() string {
			next := idCounter.Add(1)
			return strings.Repeat("0", 28) + fmt.Sprintf("%04x", next)
		}),
	)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return s, mr
}

func installMultiBarrier(t *testing.T, mr *miniredis.Miniredis, parties int) {
	t.Helper()
	var arrived atomic.Int32
	var once sync.Once
	release := make(chan struct{})
	mr.Server().SetPreHook(func(_ *server.Peer, cmd string, _ ...string) bool {
		if !strings.EqualFold(cmd, "multi") {
			return false
		}
		got := int(arrived.Add(1))
		if got == parties {
			once.Do(func() { close(release) })
		}
		select {
		case <-release:
			return false
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for %d MULTI calls; got %d", parties, got)
			once.Do(func() { close(release) })
			return false
		}
	})
	t.Cleanup(func() { mr.Server().SetPreHook(nil) })
}

func seedExceptionCap(t *testing.T, ctx context.Context, s *RedisStore, tenantID string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		req := minimalCreateExceptionReq(tenantID)
		req.Reason = fmt.Sprintf("seed exception %d", i)
		if _, err := s.CreateException(ctx, req); err != nil {
			t.Fatalf("CreateException seed[%d]: %v", i, err)
		}
	}
}

func raceCreateExceptions(t *testing.T, ctx context.Context, s *RedisStore, tenantID string, racers int) (int, int) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := minimalCreateExceptionReq(tenantID)
			req.Reason = fmt.Sprintf("racing exception %d", i)
			_, err := s.CreateException(ctx, req)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var succeeded, limitExceeded int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrExceptionLimitExceeded):
			limitExceeded++
		default:
			t.Fatalf("CreateException race err = %v, want nil or ErrExceptionLimitExceeded", err)
		}
	}
	return succeeded, limitExceeded
}

func requireExceptionTenantIndexCount(t *testing.T, mr *miniredis.Miniredis, tenantID string, want int) {
	t.Helper()
	members, err := mr.ZMembers(exceptionTenantIndexKey(tenantID))
	if err != nil {
		t.Fatalf("ZMembers tenant index: %v", err)
	}
	if len(members) != want {
		t.Fatalf("tenant %s exception index count = %d, want %d", tenantID, len(members), want)
	}
}

func TestShadowException_PerTenantCapIsAtomic(t *testing.T) {
	const racers = 64
	s, mr := newExceptionCapTestStore(t, racers+4)
	ctx := context.Background()
	tenantID := "tenant-race"
	seedExceptionCap(t, ctx, s, tenantID, maxExceptionsPerTenant-1)
	installMultiBarrier(t, mr, racers)

	succeeded, limitExceeded := raceCreateExceptions(t, ctx, s, tenantID, racers)
	if succeeded != 1 || limitExceeded != racers-1 {
		t.Fatalf("concurrent CreateException outcomes: successes=%d limitExceeded=%d, want 1/%d", succeeded, limitExceeded, racers-1)
	}
	requireExceptionTenantIndexCount(t, mr, tenantID, maxExceptionsPerTenant)
}

func TestShadowException_PerTenantCapRejectsWhenAlreadyFull(t *testing.T) {
	const racers = 8
	s, mr := newExceptionCapTestStore(t, racers+4)
	ctx := context.Background()
	tenantID := "tenant-full"
	seedExceptionCap(t, ctx, s, tenantID, maxExceptionsPerTenant)

	succeeded, limitExceeded := raceCreateExceptions(t, ctx, s, tenantID, racers)
	if succeeded != 0 || limitExceeded != racers {
		t.Fatalf("full tenant CreateException outcomes: successes=%d limitExceeded=%d, want 0/%d", succeeded, limitExceeded, racers)
	}
	requireExceptionTenantIndexCount(t, mr, tenantID, maxExceptionsPerTenant)
}

func TestShadowException_PerTenantCapIsTenantIsolated(t *testing.T) {
	const racers = 8
	s, mr := newExceptionCapTestStore(t, racers+4)
	ctx := context.Background()
	seedExceptionCap(t, ctx, s, "tenant-full", maxExceptionsPerTenant)

	fullSucceeded, fullLimitExceeded := raceCreateExceptions(t, ctx, s, "tenant-full", racers)
	if fullSucceeded != 0 || fullLimitExceeded != racers {
		t.Fatalf("full tenant outcomes: successes=%d limitExceeded=%d, want 0/%d", fullSucceeded, fullLimitExceeded, racers)
	}
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.CreateException(ctx, minimalCreateExceptionReq(fmt.Sprintf("tenant-other-%d", i)))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("different-tenant CreateException err = %v, want nil", err)
		}
	}
	requireExceptionTenantIndexCount(t, mr, "tenant-full", maxExceptionsPerTenant)
	for i := 0; i < racers; i++ {
		requireExceptionTenantIndexCount(t, mr, fmt.Sprintf("tenant-other-%d", i), 1)
	}
}

func TestRevokeException_ConcurrentRevokeProducesOneWinner(t *testing.T) {
	s, mr := newExceptionCapTestStore(t, 4)
	ctx := context.Background()
	created, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-race-revoke"))
	if err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	installMultiBarrier(t, mr, 2)

	type outcome struct {
		revoker string
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, revoker := range []string{"alice@example.com", "bob@example.com"} {
		revoker := revoker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.RevokeException(ctx, created.TenantID, created.ExceptionID, RevokeExceptionRequest{
				RevokedBy: revoker,
				Reason:    "concurrent operator correction",
			})
			results <- outcome{revoker: revoker, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var succeeded, conflicted int
	for res := range results {
		switch {
		case res.err == nil:
			succeeded++
		case errors.Is(res.err, ErrTerminalConflict):
			conflicted++
		default:
			t.Fatalf("RevokeException by %s err = %v, want nil or ErrTerminalConflict", res.revoker, res.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent revokes succeeded=%d conflicted=%d, want exactly one winner and one ErrTerminalConflict", succeeded, conflicted)
	}
}
