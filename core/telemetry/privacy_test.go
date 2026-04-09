package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestNormalizeMode(t *testing.T) {
	t.Setenv(EnvTelemetryMode, "local_only")
	if got := ModeFromEnv(); got != ModeLocalOnly {
		t.Fatalf("ModeFromEnv() = %q, want %q", got, ModeLocalOnly)
	}

	cases := map[string]Mode{
		"":           ModeLocalOnly,
		"off":        ModeOff,
		"local":      ModeLocalOnly,
		"anonymous":  ModeAnonymous,
		"unexpected": ModeLocalOnly,
	}
	for input, want := range cases {
		if got := NormalizeMode(input); got != want {
			t.Fatalf("NormalizeMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHashIdentifier(t *testing.T) {
	hashA := HashIdentifier("install-1", "tenant-123")
	hashB := HashIdentifier("install-1", "tenant-123")
	hashC := HashIdentifier("install-2", "tenant-123")

	if hashA == "" {
		t.Fatal("expected non-empty hash")
	}
	if hashA != hashB {
		t.Fatal("expected stable hash for same inputs")
	}
	if hashA == hashC {
		t.Fatal("expected salt to change hash output")
	}
	if got := HashIdentifier("", "tenant-123"); got != "" {
		t.Fatalf("expected empty hash for empty install id, got %q", got)
	}
}

func TestGetInstallIDGeneratesAndPersists(t *testing.T) {
	t.Setenv("REDIS_POOL_SIZE", "1")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "0")

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer srv.Close()

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	previousReader := randomReader
	previousHostname := hostnameLookup
	previousStart := processStartTime
	randomReader = strings.NewReader(strings.Repeat("a", 64))
	hostnameLookup = func() (string, error) { return "test-host", nil }
	processStartTime = time.Unix(1_700_000_000, 0).UTC()
	t.Cleanup(func() {
		randomReader = previousReader
		hostnameLookup = previousHostname
		processStartTime = previousStart
	})

	got, err := GetInstallID(context.Background(), client)
	if err != nil {
		t.Fatalf("GetInstallID() error = %v", err)
	}
	if got == "" {
		t.Fatal("expected generated install id")
	}

	stored, err := client.Get(context.Background(), installIDKey).Result()
	if err != nil {
		t.Fatalf("redis get install id: %v", err)
	}
	if stored != got {
		t.Fatalf("stored install id = %q, want %q", stored, got)
	}

	gotAgain, err := GetInstallID(context.Background(), client)
	if err != nil {
		t.Fatalf("GetInstallID() second call error = %v", err)
	}
	if gotAgain != got {
		t.Fatalf("GetInstallID() second call = %q, want %q", gotAgain, got)
	}
}
