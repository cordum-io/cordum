package testredis

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestOptionsBoundsRedisPool(t *testing.T) {
	t.Parallel()

	opts := Options("127.0.0.1:6379")
	if opts.Addr != "127.0.0.1:6379" {
		t.Fatalf("Addr = %q, want 127.0.0.1:6379", opts.Addr)
	}
	if opts.PoolSize != PoolSize {
		t.Fatalf("PoolSize = %d, want %d", opts.PoolSize, PoolSize)
	}
	if opts.MinIdleConns != MinIdleConns {
		t.Fatalf("MinIdleConns = %d, want %d", opts.MinIdleConns, MinIdleConns)
	}
}

func TestURLBoundsRedisPool(t *testing.T) {
	t.Parallel()

	parsed, err := redis.ParseURL(URL("127.0.0.1:6379"))
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	if parsed.Addr != "127.0.0.1:6379" {
		t.Fatalf("Addr = %q, want 127.0.0.1:6379", parsed.Addr)
	}
	if parsed.PoolSize != PoolSize {
		t.Fatalf("PoolSize = %d, want %d", parsed.PoolSize, PoolSize)
	}
	if parsed.MinIdleConns != MinIdleConns {
		t.Fatalf("MinIdleConns = %d, want %d", parsed.MinIdleConns, MinIdleConns)
	}
}

func TestNewClientPingsMiniredis(t *testing.T) {
	t.Parallel()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	client := NewClient(t, mr.Addr())
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping miniredis: %v", err)
	}
	if client.Options().PoolSize != PoolSize {
		t.Fatalf("PoolSize = %d, want %d", client.Options().PoolSize, PoolSize)
	}
}

func TestApplyPoolEnvRestoresPreviousValues(t *testing.T) {
	originalPool, hadPool := os.LookupEnv(RedisPoolSizeEnv)
	originalIdle, hadIdle := os.LookupEnv(RedisMinIdleConnsEnv)
	t.Cleanup(func() {
		restoreEnv(RedisPoolSizeEnv, originalPool, hadPool)
		restoreEnv(RedisMinIdleConnsEnv, originalIdle, hadIdle)
	})

	if err := os.Setenv(RedisPoolSizeEnv, "99"); err != nil {
		t.Fatalf("set pool env: %v", err)
	}
	if err := os.Unsetenv(RedisMinIdleConnsEnv); err != nil {
		t.Fatalf("unset idle env: %v", err)
	}

	restore := ApplyPoolEnv()
	if got := os.Getenv(RedisPoolSizeEnv); got != "1" {
		t.Fatalf("pool env = %q, want 1", got)
	}
	if got := os.Getenv(RedisMinIdleConnsEnv); got != "0" {
		t.Fatalf("idle env = %q, want 0", got)
	}

	restore()
	if got := os.Getenv(RedisPoolSizeEnv); got != "99" {
		t.Fatalf("restored pool env = %q, want 99", got)
	}
	if _, ok := os.LookupEnv(RedisMinIdleConnsEnv); ok {
		t.Fatal("idle env should be unset after restore")
	}
}

func restoreEnv(name, value string, set bool) {
	if set {
		_ = os.Setenv(name, value)
		return
	}
	_ = os.Unsetenv(name)
}
