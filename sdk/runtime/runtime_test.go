package runtime

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestPingRedis_Success(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	if err := PingRedis("redis://" + srv.Addr()); err != nil {
		t.Fatalf("expected successful ping, got: %v", err)
	}
}

func TestPingRedis_WithPassword(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	srv.RequireAuth("test-pass")

	// Without password should fail.
	if err := PingRedis("redis://" + srv.Addr()); err == nil {
		t.Fatal("expected auth failure without password")
	}

	// With password should succeed.
	if err := PingRedis("redis://:test-pass@" + srv.Addr()); err != nil {
		t.Fatalf("expected successful ping with password, got: %v", err)
	}
}

func TestPingRedis_BadURL(t *testing.T) {
	if err := PingRedis("not-a-url"); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestValidateRedisURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"redis://:password@localhost:6379", true},
		{"redis://user:pass@localhost:6379", true},
		{"redis://localhost:6379", false},
		{"redis://127.0.0.1:6379/0", false},
	}
	for _, tt := range tests {
		if got := ValidateRedisURL(tt.url); got != tt.want {
			t.Errorf("ValidateRedisURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
