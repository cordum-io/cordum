package gateway

import (
	"context"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newTestUserStore(t *testing.T) (*RedisUserStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	store, err := NewRedisUserStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewRedisUserStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store, srv
}

func TestLoginThrottle_BlocksAfterMaxAttempts(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	username := "alice"

	// First 5 attempts should not be throttled.
	for i := 0; i < maxLoginAttempts; i++ {
		if err := store.CheckLoginThrottle(ctx, username); err != nil {
			t.Fatalf("attempt %d: unexpected throttle: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, username)
	}

	// 6th attempt should be throttled.
	if err := store.CheckLoginThrottle(ctx, username); err == nil {
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
		store.RecordFailedLogin(ctx, username)
	}

	// Not yet throttled.
	if err := store.CheckLoginThrottle(ctx, username); err != nil {
		t.Fatalf("unexpected throttle after 3 attempts: %v", err)
	}

	// Successful login clears the counter.
	store.ClearFailedLogins(ctx, username)

	// Can now do 5 more failures without being throttled.
	for i := 0; i < maxLoginAttempts; i++ {
		if err := store.CheckLoginThrottle(ctx, username); err != nil {
			t.Fatalf("attempt %d after reset: unexpected throttle: %v", i+1, err)
		}
		store.RecordFailedLogin(ctx, username)
	}

	// Now should be throttled again.
	if err := store.CheckLoginThrottle(ctx, username); err == nil {
		t.Fatal("expected throttle after max attempts post-reset")
	}
}

func TestLoginThrottle_IndependentPerUsername(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()

	// Lock out alice.
	for i := 0; i < maxLoginAttempts; i++ {
		store.RecordFailedLogin(ctx, "alice")
	}

	// Alice is throttled.
	if err := store.CheckLoginThrottle(ctx, "alice"); err == nil {
		t.Fatal("expected alice to be throttled")
	}

	// Bob is NOT throttled.
	if err := store.CheckLoginThrottle(ctx, "bob"); err != nil {
		t.Fatalf("bob should not be throttled: %v", err)
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	err := validatePassword("Ab1!short")
	if err == nil {
		t.Fatal("expected error for short password")
	}
	if !strings.Contains(err.Error(), "at least 12 characters") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_NoUppercase(t *testing.T) {
	err := validatePassword("alllowercase1!")
	if err == nil {
		t.Fatal("expected error for missing uppercase")
	}
	if !strings.Contains(err.Error(), "uppercase letter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_NoDigit(t *testing.T) {
	err := validatePassword("AllLettersOnly!!")
	if err == nil {
		t.Fatal("expected error for missing digit")
	}
	if !strings.Contains(err.Error(), "digit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePassword_NoSpecialChar(t *testing.T) {
	err := validatePassword("AllLetters1234")
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
		if err := validatePassword(pw); err != nil {
			t.Errorf("validatePassword(%q) = %v, want nil", pw, err)
		}
	}
}

func TestValidatePassword_MultipleViolations(t *testing.T) {
	err := validatePassword("alllowernodigit")
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
	for i := 0; i < maxLoginAttempts; i++ {
		store.RecordFailedLogin(ctx, username)
	}

	// Charlie is throttled.
	if err := store.CheckLoginThrottle(ctx, username); err == nil {
		t.Fatal("expected throttle")
	}

	// Fast-forward past lockout period.
	srv.FastForward(loginLockoutPeriod + 1)

	// Charlie can try again.
	if err := store.CheckLoginThrottle(ctx, username); err != nil {
		t.Fatalf("expected throttle to expire: %v", err)
	}
}
