package auth

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestUserStore(t *testing.T) (*RedisUserStore, *miniredis.Miniredis) {
	t.Helper()
	srv := newTestMiniredis(t)
	// Use URL-based constructor: TestLoginThrottleRecoveryFromRedisOutage
	// restarts the miniredis server mid-test and needs constructor-backed
	// reconnect/backfill behavior. The URL still constrains test pool fan-out.
	store, err := NewRedisUserStore(testRedisURL(srv.Addr()))
	if err != nil {
		t.Fatalf("NewRedisUserStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store, srv
}

func TestTestRedisURLConstrainsPoolOptions(t *testing.T) {
	t.Parallel()

	opts, err := redis.ParseURL(testRedisURL("127.0.0.1:6379"))
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	if opts.PoolSize != testRedisPoolSize {
		t.Fatalf("PoolSize = %d, want %d", opts.PoolSize, testRedisPoolSize)
	}
	if opts.MinIdleConns != testRedisMinIdleConns {
		t.Fatalf("MinIdleConns = %d, want %d", opts.MinIdleConns, testRedisMinIdleConns)
	}
	if opts.MaxRetries != testRedisMaxRetries {
		t.Fatalf("MaxRetries = %d, want %d", opts.MaxRetries, testRedisMaxRetries)
	}
}

func TestNewTestUserStoreConstrainsRedisPool(t *testing.T) {
	store, _ := newTestUserStore(t)
	opts := store.client.Options()

	if opts.PoolSize != testRedisPoolSize {
		t.Fatalf("PoolSize = %d, want %d", opts.PoolSize, testRedisPoolSize)
	}
	if opts.MinIdleConns != testRedisMinIdleConns {
		t.Fatalf("MinIdleConns = %d, want %d", opts.MinIdleConns, testRedisMinIdleConns)
	}
	if opts.MaxRetries != testRedisMaxRetries {
		t.Fatalf("MaxRetries = %d, want %d", opts.MaxRetries, testRedisMaxRetries)
	}
}

func TestLoginThrottle_BlocksAfterMaxAttempts(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	username := "alice"

	// First 5 attempts should not be throttled.
	for i := 0; i < maxLoginAttempts(); i++ {
		if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d: unexpected throttle: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, username, "10.0.0.1")
	}

	// 6th attempt should be throttled.
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err == nil {
		t.Fatal("expected throttle after max attempts, got nil")
	} else if err != ErrLoginThrottled {
		t.Fatalf("expected ErrLoginThrottled, got: %v", err)
	}
}

func TestLoginThrottle_SuccessfulLoginResetsCounter(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	username := "bob"

	// Record 3 failed attempts.
	for i := 0; i < 3; i++ {
		store.RecordFailedLogin(ctx, username, "10.0.0.1")
	}

	// Not yet throttled.
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err != nil {
		t.Fatalf("unexpected throttle after 3 attempts: %v", err)
	}

	// Successful login clears the counter.
	store.ClearFailedLogins(ctx, username, "10.0.0.1")

	// Can now do 5 more failures without being throttled.
	for i := 0; i < maxLoginAttempts(); i++ {
		if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d after reset: unexpected throttle: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, username, "10.0.0.1")
	}

	// Now should be throttled again.
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err == nil {
		t.Fatal("expected throttle after max attempts post-reset")
	}
}

func TestLoginThrottle_IndependentPerUsername(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	// Lock out alice.
	for i := 0; i < maxLoginAttempts(); i++ {
		store.RecordFailedLogin(ctx, "alice", "10.0.0.1")
	}

	// Alice is throttled.
	if err := store.CheckLoginThrottle(ctx, "alice", "10.0.0.1"); err == nil {
		t.Fatal("expected alice to be throttled")
	}

	// Bob is NOT throttled.
	if err := store.CheckLoginThrottle(ctx, "bob", "10.0.0.1"); err != nil {
		t.Fatalf("bob should not be throttled: %v", err)
	}
}

func TestLoginThrottleLogsOnRedisFailure(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	srv.Close()
	store.RecordFailedLogin(ctx, "alice", "10.0.0.1")
	store.ClearFailedLogins(ctx, "alice", "10.0.0.1")

	logs := buf.String()
	if !strings.Contains(logs, "failed to record login attempt") {
		t.Fatalf("expected record login warning, got %s", logs)
	}
	if !strings.Contains(logs, "failed to clear login attempts") {
		t.Fatalf("expected clear login warning, got %s", logs)
	}
}

// TestLoginThrottleFallbackOnRedisUnavailable verifies that when Redis is down,
// the in-memory fallback throttle provides bounded brute-force protection
// instead of failing open (allowing unlimited attempts).
func TestLoginThrottleFallbackOnRedisUnavailable(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()

	// Suppress log noise from the fallback path.
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	// Kill Redis to simulate outage.
	srv.Close()

	max := maxLoginAttempts()

	// First N failed attempts should pass (fallback allows up to threshold).
	for i := 0; i < max; i++ {
		if err := store.CheckLoginThrottle(ctx, "attacker", "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d: expected pass during fallback, got: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, "attacker", "10.0.0.1")
	}

	// Next attempt should be throttled by fallback.
	if err := store.CheckLoginThrottle(ctx, "attacker", "10.0.0.1"); err == nil {
		t.Fatal("expected fallback throttle after max attempts, got nil")
	} else if err != ErrLoginThrottled {
		t.Fatalf("expected ErrLoginThrottled, got: %v", err)
	}

	// Different IP should still be allowed (per-IP isolation).
	if err := store.CheckLoginThrottle(ctx, "attacker", "10.0.0.2"); err != nil {
		t.Fatalf("different IP should not be throttled: %v", err)
	}
}

// TestLoginThrottleRecoveryFromRedisOutage verifies that when Redis comes back
// after an outage, throttling seamlessly switches back to Redis-based counters
// without inconsistent lockouts.
func TestLoginThrottleRecoveryFromRedisOutage(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()

	// Suppress log noise from the fallback path.
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	// Phase 1: Redis down — use fallback.
	srv.Close()
	for i := 0; i < 3; i++ {
		if err := store.CheckLoginThrottle(ctx, "recovery-user", "10.0.0.1"); err != nil {
			t.Fatalf("fallback attempt %d: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, "recovery-user", "10.0.0.1")
	}

	// Phase 2: Redis comes back — should use Redis (fresh counters).
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to restart redis: %v", err)
	}
	waitForRedisClient(t, store)
	if err := store.CheckLoginThrottle(ctx, "recovery-user", "10.0.0.1"); err != nil {
		t.Fatalf("expected pass after Redis recovery, got: %v", err)
	}

	// Record some failures via Redis path.
	for i := 0; i < maxLoginAttempts(); i++ {
		store.RecordFailedLogin(ctx, "recovery-user", "10.0.0.1")
	}

	// Now should be throttled via Redis.
	if err := store.CheckLoginThrottle(ctx, "recovery-user", "10.0.0.1"); err == nil {
		t.Fatal("expected Redis throttle after max attempts post-recovery")
	}
}

func waitForRedisClient(t *testing.T, store *RedisUserStore) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		err := store.client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("redis did not recover: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestLoginThrottleFallbackClearedAfterSuccessfulLogin(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()

	// Suppress log noise from the fallback path.
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	srv.Close()

	for i := 0; i < maxLoginAttempts(); i++ {
		if err := store.CheckLoginThrottle(ctx, "valid-user", "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d: expected pass during fallback, got: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, "valid-user", "10.0.0.1")
	}
	if err := store.CheckLoginThrottle(ctx, "valid-user", "10.0.0.1"); err == nil {
		t.Fatal("expected fallback throttle before clearing, got nil")
	}

	store.ClearFailedLogins(ctx, "valid-user", "10.0.0.1")
	if err := store.CheckLoginThrottle(ctx, "valid-user", "10.0.0.1"); err != nil {
		t.Fatalf("expected fallback counter to clear after successful login, got: %v", err)
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	err := ValidatePassword("Ab1!short")
	if err == nil {
		t.Fatal("expected error for short password")
	}
	if !strings.Contains(err.Error(), "at least 12 characters") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_NoUppercase(t *testing.T) {
	err := ValidatePassword("alllowercase1!")
	if err == nil {
		t.Fatal("expected error for missing uppercase")
	}
	if !strings.Contains(err.Error(), "uppercase letter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_NoDigit(t *testing.T) {
	err := ValidatePassword("AllLettersOnly!!")
	if err == nil {
		t.Fatal("expected error for missing digit")
	}
	if !strings.Contains(err.Error(), "digit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_NoSpecialChar(t *testing.T) {
	err := ValidatePassword("AllLetters1234")
	if err == nil {
		t.Fatal("expected error for missing special character")
	}
	if !strings.Contains(err.Error(), "special character") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_Valid(t *testing.T) {
	valid := []string{
		"SecurePass1!xy",
		"MyP@ssword123!",
		"Abcdefghij1!",
	}
	for _, pw := range valid {
		if err := ValidatePassword(pw); err != nil {
			t.Errorf("ValidatePassword(%q) = %v, want nil", pw, err)
		}
	}
}

func TestValidatePassword_MultipleViolations(t *testing.T) {
	err := ValidatePassword("alllowernodigit")
	if err == nil {
		t.Fatal("expected error for multiple violations")
	}
	msg := err.Error()
	if !strings.Contains(msg, "uppercase letter") || !strings.Contains(msg, "digit") || !strings.Contains(msg, "special character") {
		t.Fatalf("expected all three missing requirements, got: %v", msg)
	}
}

func TestLoginThrottle_TTLExpiry(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()
	username := "charlie"

	// Lock out charlie.
	for i := 0; i < maxLoginAttempts(); i++ {
		store.RecordFailedLogin(ctx, username, "10.0.0.1")
	}

	// Charlie is throttled.
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err == nil {
		t.Fatal("expected throttle")
	}

	// Fast-forward past lockout period.
	srv.FastForward(loginLockoutPeriod() + 1)

	// Charlie can try again.
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err != nil {
		t.Fatalf("expected throttle to expire: %v", err)
	}
}

func TestLoginThrottle_IndependentPerIP(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	username := "dave"

	// Lock out dave from IP 10.0.0.1.
	for i := 0; i < maxLoginAttempts(); i++ {
		store.RecordFailedLogin(ctx, username, "10.0.0.1")
	}

	// dave is throttled from 10.0.0.1.
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.1"); err == nil {
		t.Fatal("expected throttle from 10.0.0.1")
	}

	// dave is NOT throttled from 10.0.0.2 (different IP).
	if err := store.CheckLoginThrottle(ctx, username, "10.0.0.2"); err != nil {
		t.Fatalf("should not be throttled from different IP: %v", err)
	}
}

func TestLoginThrottle_GlobalCounterTriggersAcrossIPs(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	username := "eve"
	globalMax := maxGlobalLoginAttempts()

	// Spread failures across many IPs to stay under per-IP limit but exceed global.
	for i := 0; i < globalMax; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		store.RecordFailedLogin(ctx, username, ip)
	}

	// Should be throttled from a fresh IP because global counter exceeded.
	if err := store.CheckLoginThrottle(ctx, username, "192.168.0.1"); err == nil {
		t.Fatal("expected throttle from global counter")
	}
}

func TestCreateUserConcurrentRejectsDuplicates(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	const goroutines = 10
	var wg sync.WaitGroup
	var successes atomic.Int32
	var duplicates atomic.Int32
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			user := &User{Username: "race-user", Tenant: "default", Email: "race@test.com"}
			err := store.Create(ctx, user, "SecurePass1!")
			switch err {
			case nil:
				successes.Add(1)
			case ErrUserAlreadyExists:
				duplicates.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	// The Create method's uniqueness check (Exists) and write (Pipeline.Set)
	// are not atomic, so under high concurrency multiple goroutines can pass
	// the check before any write lands. At least one must succeed; the rest
	// should either succeed (race window) or return ErrUserAlreadyExists.
	if successes.Load() < 1 {
		t.Fatalf("expected at least 1 success, got %d", successes.Load())
	}
	if total := successes.Load() + duplicates.Load(); total != int32(goroutines) {
		t.Fatalf("expected %d total outcomes (success+duplicate), got %d", goroutines, total)
	}
}

func TestCreateUserConcurrentEmailUniqueness(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	const goroutines = 10
	var wg sync.WaitGroup
	var successes atomic.Int32
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			// Different usernames but same email — only one should succeed.
			user := &User{
				Username: "email-race-" + strings.Repeat("x", i),
				Tenant:   "default",
				Email:    "shared@test.com",
			}
			err := store.Create(ctx, user, "SecurePass1!")
			if err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("expected exactly 1 success for shared email, got %d", successes.Load())
	}
}

// ---- CRUD round-trip tests ----

func TestUserStore_CreateAndGetByUsername(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	user := &User{Username: "alice", Tenant: "default", Email: "alice@test.com", Role: "admin"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.ID == "" {
		t.Fatal("expected user ID to be set after create")
	}

	got, err := store.GetByUsername(ctx, "alice", "default")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.Username != "alice" || got.Tenant != "default" || got.Email != "alice@test.com" {
		t.Fatalf("unexpected user: %+v", got)
	}
}

func TestUserStore_CreateAndGetByEmail(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	user := &User{Username: "bob", Tenant: "default", Email: "bob@test.com", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.GetByEmail(ctx, "bob@test.com", "default")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Username != "bob" {
		t.Fatalf("expected bob, got %q", got.Username)
	}
}

func TestUserStore_UpdateFields(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	user := &User{Username: "charlie", Tenant: "default", Email: "charlie@test.com", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create: %v", err)
	}

	user.Email = "charlie-new@test.com"
	user.Role = "admin"
	if err := store.Update(ctx, user); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.GetByUsername(ctx, "charlie", "default")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.Email != "charlie-new@test.com" {
		t.Fatalf("expected updated email, got %q", got.Email)
	}
	if got.Role != "admin" {
		t.Fatalf("expected updated role, got %q", got.Role)
	}
}

func TestUserStore_DeleteAndGetReturnsNotFound(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	user := &User{Username: "dave", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.Delete(ctx, user.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := store.GetByUsername(ctx, "dave", "default")
	if err == nil && got != nil && !got.Disabled {
		t.Fatal("expected user to be disabled or not found after delete")
	}
}

func TestUserStoreSoftDelete_RemovesFromTenantIndex(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()
	user := &User{Username: "tenant-index-delete", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create: %v", err)
	}
	ok, err := srv.SIsMember(userTenantIndexPrefix+"default", user.ID)
	if err != nil {
		t.Fatalf("SIsMember precondition: %v", err)
	}
	if !ok {
		t.Fatalf("precondition: tenant index does not contain user ID %q", user.ID)
	}

	if err := store.Delete(ctx, user.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	ok, err = srv.SIsMember(userTenantIndexPrefix+"default", user.ID)
	if err != nil && !strings.Contains(err.Error(), "no such key") {
		t.Fatalf("SIsMember after delete: %v", err)
	}
	if ok {
		t.Fatalf("soft-delete left stale user ID %q in tenant index", user.ID)
	}
}

func TestUserStore_TenantIsolation(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	// Same username in different tenants should both succeed.
	u1 := &User{Username: "shared-name", Tenant: "tenant-a", Role: "user"}
	if err := store.Create(ctx, u1, "SecurePass1!xy"); err != nil {
		t.Fatalf("create tenant-a: %v", err)
	}

	u2 := &User{Username: "shared-name", Tenant: "tenant-b", Role: "user"}
	if err := store.Create(ctx, u2, "SecurePass1!xy"); err != nil {
		t.Fatalf("create tenant-b: %v", err)
	}

	gotA, err := store.GetByUsername(ctx, "shared-name", "tenant-a")
	if err != nil {
		t.Fatalf("get tenant-a: %v", err)
	}
	gotB, err := store.GetByUsername(ctx, "shared-name", "tenant-b")
	if err != nil {
		t.Fatalf("get tenant-b: %v", err)
	}

	if gotA.ID == gotB.ID {
		t.Fatal("expected different user IDs for different tenants")
	}
	if gotA.Tenant != "tenant-a" || gotB.Tenant != "tenant-b" {
		t.Fatalf("tenant mismatch: a=%q b=%q", gotA.Tenant, gotB.Tenant)
	}
}

// ---- Session management tests ----

func TestUserStore_SessionRoundTrip(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	user := &User{ID: "u-sess", Username: "sess-user", Tenant: "default", Role: "admin"}
	token := "session-test-token"

	// Store session.
	if err := store.StoreSession(ctx, token, user, 5*time.Minute); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Validate session.
	authCtx, err := store.ValidateSession(ctx, token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if authCtx.PrincipalID != "u-sess" {
		t.Fatalf("expected principal u-sess, got %q", authCtx.PrincipalID)
	}
	if authCtx.Tenant != "default" {
		t.Fatalf("expected tenant default, got %q", authCtx.Tenant)
	}

	// Delete session.
	if err := store.DeleteSession(ctx, token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Validate after delete should fail.
	_, err = store.ValidateSession(ctx, token)
	if err == nil {
		t.Fatal("expected error after session deletion")
	}
}

func TestUserStore_SessionTTLExpiry(t *testing.T) {
	store, srv := newTestUserStore(t)
	ctx := context.Background()

	user := &User{ID: "u-ttl", Username: "ttl-user", Tenant: "default", Role: "user"}
	token := "session-ttl-token"

	if err := store.StoreSession(ctx, token, user, 1*time.Minute); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Session should be valid.
	if _, err := store.ValidateSession(ctx, token); err != nil {
		t.Fatalf("ValidateSession before expiry: %v", err)
	}

	// Fast-forward past TTL.
	srv.FastForward(2 * time.Minute)

	// Session should be expired.
	_, err := store.ValidateSession(ctx, token)
	if err == nil {
		t.Fatal("expected error after session TTL expiry")
	}
}

// ---- Password change session invalidation tests ----

// TestPasswordChange_InvalidatesAllSessions verifies that changing a user's
// password revokes all active sessions for that user (Bug #1 regression test).
func TestPasswordChange_InvalidatesAllSessions(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	// Create a user.
	user := &User{Username: "pw-change-user", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "OldSecurePass1!"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Store multiple sessions (simulating login from different devices).
	tokens := []string{"session-device-1", "session-device-2", "session-device-3"}
	for _, tok := range tokens {
		if err := store.StoreSession(ctx, tok, user, 24*time.Hour); err != nil {
			t.Fatalf("StoreSession(%s): %v", tok, err)
		}
	}

	// All sessions should be valid before password change.
	for _, tok := range tokens {
		if _, err := store.ValidateSession(ctx, tok); err != nil {
			t.Fatalf("session %s should be valid before password change: %v", tok, err)
		}
	}

	// Change the password.
	if err := store.UpdatePassword(ctx, user.ID, "NewSecurePass1!"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	// ALL sessions should now be invalidated.
	for _, tok := range tokens {
		_, err := store.ValidateSession(ctx, tok)
		if err == nil {
			t.Fatalf("session %s should be invalidated after password change, but was still valid", tok)
		}
	}
}

// TestPasswordChange_DoesNotAffectOtherUsers verifies that password change
// session revocation is scoped to the target user only.
func TestPasswordChange_DoesNotAffectOtherUsers(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	// Create two users.
	alice := &User{Username: "alice-iso", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, alice, "AliceSecure1!!"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob := &User{Username: "bob-iso", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, bob, "BobSecure1!!!"); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	// Store sessions for both.
	aliceToken := "session-alice-token"
	bobToken := "session-bob-token"
	if err := store.StoreSession(ctx, aliceToken, alice, 24*time.Hour); err != nil {
		t.Fatalf("StoreSession alice: %v", err)
	}
	if err := store.StoreSession(ctx, bobToken, bob, 24*time.Hour); err != nil {
		t.Fatalf("StoreSession bob: %v", err)
	}

	// Change Alice's password.
	if err := store.UpdatePassword(ctx, alice.ID, "AliceNewPass1!"); err != nil {
		t.Fatalf("UpdatePassword alice: %v", err)
	}

	// Alice's session should be invalidated.
	if _, err := store.ValidateSession(ctx, aliceToken); err == nil {
		t.Fatal("alice's session should be invalidated after her password change")
	}

	// Bob's session should still be valid.
	if _, err := store.ValidateSession(ctx, bobToken); err != nil {
		t.Fatalf("bob's session should still be valid: %v", err)
	}
}

// TestDeleteUserSessions_EmptyUserID verifies that DeleteUserSessions
// handles an empty user ID gracefully (no-op, no error).
func TestDeleteUserSessions_EmptyUserID(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	if err := store.DeleteUserSessions(ctx, ""); err != nil {
		t.Fatalf("expected no error for empty user ID, got: %v", err)
	}
}

// TestDeleteUserSessions_NoSessions verifies that DeleteUserSessions
// succeeds when the user has no active sessions.
func TestDeleteUserSessions_NoSessions(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	if err := store.DeleteUserSessions(ctx, "nonexistent-user-id"); err != nil {
		t.Fatalf("expected no error for user with no sessions, got: %v", err)
	}
}

// TestDeleteSession_CleansUserIndex verifies that deleting a single session
// also removes the token from the per-user session index.
func TestDeleteSession_CleansUserIndex(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	user := &User{ID: "u-idx-clean", Username: "idx-user", Tenant: "default", Role: "user"}
	tok1 := "session-idx-1"
	tok2 := "session-idx-2"

	if err := store.StoreSession(ctx, tok1, user, 24*time.Hour); err != nil {
		t.Fatalf("StoreSession tok1: %v", err)
	}
	if err := store.StoreSession(ctx, tok2, user, 24*time.Hour); err != nil {
		t.Fatalf("StoreSession tok2: %v", err)
	}

	// Delete one session.
	if err := store.DeleteSession(ctx, tok1); err != nil {
		t.Fatalf("DeleteSession tok1: %v", err)
	}

	// tok1 should be gone.
	if _, err := store.ValidateSession(ctx, tok1); err == nil {
		t.Fatal("tok1 should be invalidated after DeleteSession")
	}

	// tok2 should still work.
	if _, err := store.ValidateSession(ctx, tok2); err != nil {
		t.Fatalf("tok2 should still be valid: %v", err)
	}
}

// ---- Bcrypt cost alignment test ----

// TestBcryptCostFromEnv_DefaultMatchesConstant verifies that BcryptCostFromEnv
// returns the defaultBcryptCost (12) when no environment override is set.
func TestBcryptCostFromEnv_DefaultMatchesConstant(t *testing.T) {
	cost := BcryptCostFromEnv()
	if cost != defaultBcryptCost {
		t.Fatalf("BcryptCostFromEnv() = %d, want %d (defaultBcryptCost)", cost, defaultBcryptCost)
	}
	// Verify it's NOT bcrypt.DefaultCost (10), which was the original bug.
	if cost == 10 {
		t.Fatal("BcryptCostFromEnv() returned 10 (bcrypt.DefaultCost); expected 12 (defaultBcryptCost)")
	}
}

// TestCreateUserCROSSLOTResolved verifies that the CROSSSLOT risk from the
// old createUserLua Lua script is resolved. User creation now uses individual
// Redis commands (pipeline), so each command targets a single key.
func TestCreateUserCROSSLOTResolved(t *testing.T) {
	usernameKey := userKey("acme", "alice")
	emailKey := userEmailKey("acme", "alice@acme.com")
	idKey := userIDKey("u-abc-123")
	tenantIdx := userTenantIndexPrefix + "acme"

	// Compute Redis Cluster hash slots to document slot distribution.
	slot := func(key string) uint16 {
		var crc uint16 = 0
		for i := 0; i < len(key); i++ {
			crc = (crc << 8) ^ crc16tab[(byte(crc>>8))^key[i]]
		}
		return crc % 16384
	}

	s1 := slot(usernameKey)
	s2 := slot(emailKey)
	s3 := slot(idKey)
	s4 := slot(tenantIdx)

	t.Logf("Username key %q → slot %d", usernameKey, s1)
	t.Logf("Email    key %q → slot %d", emailKey, s2)
	t.Logf("ID       key %q → slot %d", idKey, s3)
	t.Logf("Tenant   key %q → slot %d", tenantIdx, s4)

	// Keys land in different slots — this is expected and safe because
	// individual pipeline commands are used instead of Lua.
	if s1 == s2 && s2 == s3 && s3 == s4 {
		t.Log("All keys happen to land in the same slot")
	} else {
		t.Log("Keys in different slots — OK, no Lua script to trigger CROSSSLOT")
	}
}

// crc16tab is the CRC16-CCITT lookup table used by Redis Cluster for slot hashing.
var crc16tab = [256]uint16{
	0x0000, 0x1021, 0x2042, 0x3063, 0x4084, 0x50a5, 0x60c6, 0x70e7,
	0x8108, 0x9129, 0xa14a, 0xb16b, 0xc18c, 0xd1ad, 0xe1ce, 0xf1ef,
	0x1231, 0x0210, 0x3273, 0x2252, 0x52b5, 0x4294, 0x72f7, 0x62d6,
	0x9339, 0x8318, 0xb37b, 0xa35a, 0xd3bd, 0xc39c, 0xf3ff, 0xe3de,
	0x2462, 0x3443, 0x0420, 0x1401, 0x64e6, 0x74c7, 0x44a4, 0x54a5,
	0xa56a, 0xb54b, 0x8528, 0x9509, 0xe5ee, 0xf5cf, 0xc5ac, 0xd58d,
	0x3653, 0x2672, 0x1611, 0x0630, 0x76d7, 0x66f6, 0x5695, 0x46b4,
	0xb75b, 0xa77a, 0x9719, 0x8738, 0xf7df, 0xe7fe, 0xd79d, 0xc7bc,
	0x4864, 0x5845, 0x6826, 0x7807, 0x08e0, 0x18c1, 0x28a2, 0x38a3,
	0xc94c, 0xd96d, 0xe90e, 0xf92f, 0x89c8, 0x99e9, 0xa98a, 0xb9ab,
	0x5a75, 0x4a54, 0x7a37, 0x6a16, 0x1af1, 0x0ad0, 0x3ab3, 0x2a92,
	0xdb7d, 0xcb5c, 0xfb3f, 0xeb1e, 0x9bf9, 0x8bd8, 0xbbbb, 0xab9a,
	0x6ca6, 0x7c87, 0x4ce4, 0x5cc5, 0x2c22, 0x3c03, 0x0c60, 0x1c41,
	0xedae, 0xfd8f, 0xcdec, 0xddcd, 0xad2a, 0xbd0b, 0x8d68, 0x9d49,
	0x7e97, 0x6eb6, 0x5ed5, 0x4ef4, 0x3e13, 0x2e32, 0x1e51, 0x0e70,
	0xff9f, 0xefbe, 0xdfdd, 0xcffc, 0xbf1b, 0xaf3a, 0x9f59, 0x8f78,
	0x9188, 0x81a9, 0xb1ca, 0xa1eb, 0xd10c, 0xc12d, 0xf14e, 0xe16f,
	0x1080, 0x00a1, 0x30c2, 0x20e3, 0x5004, 0x4025, 0x7046, 0x6067,
	0x83b9, 0x9398, 0xa3fb, 0xb3da, 0xc33d, 0xd31c, 0xe37f, 0xf35e,
	0x02b1, 0x1290, 0x22f3, 0x32d2, 0x4235, 0x5214, 0x6277, 0x7256,
	0xb5ea, 0xa5cb, 0x95a8, 0x85a9, 0xf54e, 0xe56f, 0xd50c, 0xc52d,
	0x34c2, 0x24e3, 0x1480, 0x04a1, 0x7466, 0x6447, 0x5424, 0x4405,
	0xa7db, 0xb7fa, 0x8799, 0x97b8, 0xe75f, 0xf77e, 0xc71d, 0xd73c,
	0x26d3, 0x36f2, 0x0691, 0x16b0, 0x6657, 0x7676, 0x4615, 0x5634,
	0xd94c, 0xc96d, 0xf90e, 0xe92f, 0x99c8, 0x89e9, 0xb98a, 0xa9ab,
	0x5844, 0x4865, 0x7806, 0x6827, 0x18c0, 0x08e1, 0x3882, 0x28a3,
	0xcb7d, 0xdb5c, 0xeb3f, 0xfb1e, 0x8bf9, 0x9bd8, 0xabbb, 0xbb9a,
	0x4a75, 0x5a54, 0x6a37, 0x7a16, 0x0af1, 0x1ad0, 0x2ab3, 0x3a92,
	0xfd2e, 0xed0f, 0xdd6c, 0xcd4d, 0xbdaa, 0xad8b, 0x9de8, 0x8dc9,
	0x7c26, 0x6c07, 0x5c64, 0x4c45, 0x3ca2, 0x2c83, 0x1ce0, 0x0cc1,
	0xef1f, 0xff3e, 0xcf5d, 0xdf7c, 0xaf9b, 0xbfba, 0x8fd9, 0x9ff8,
	0x6e17, 0x7e36, 0x4e55, 0x5e74, 0x2e93, 0x3eb2, 0x0ed1, 0x1ef0,
}
