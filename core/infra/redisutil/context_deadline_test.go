package redisutil

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// silentServer accepts loopback TCP connections and never replies, so every
// Redis read on them waits until a deadline ends it.
type silentServer struct {
	url      string
	accepted atomic.Int32
}

func startSilentServer(t *testing.T) *silentServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &silentServer{url: "redis://" + ln.Addr().String()}
	var (
		mu    sync.Mutex
		conns []net.Conn
		done  = make(chan struct{})
	)
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.accepted.Add(1)
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return srv
}

// timedSet runs one SET against srv and fails the test unless it errors within
// limit after reaching the server.
func timedSet(ctx context.Context, t *testing.T, srv *silentServer, client redis.Cmdable, limit time.Duration) {
	t.Helper()
	start := time.Now()
	err := client.Set(ctx, "k", "v", 0).Err()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("SET against a server that never replies returned no error")
	}
	if srv.accepted.Load() == 0 {
		t.Fatalf("SET failed without reaching the server: %v", err)
	}
	if elapsed >= limit {
		t.Fatalf("SET returned after %v (err %v); want under %v", elapsed, err, limit)
	}
}

func TestNewClientAppliesContextDeadline(t *testing.T) {
	t.Setenv(envRedisReadTimeout, "10s")
	t.Setenv(envRedisWriteTimeout, "10s")
	t.Setenv(envRedisMinIdleConns, "0")
	srv := startSilentServer(t)
	client, err := NewClient(srv.url)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	timedSet(ctx, t, srv, client, 2*time.Second)
}

func TestParseOptionsAppliesContextDeadline(t *testing.T) {
	srv := startSilentServer(t)
	opts, err := ParseOptions(srv.url + "?read_timeout=10s&write_timeout=10s")
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	timedSet(ctx, t, srv, client, 2*time.Second)
}

func TestNewClientKeepsSocketTimeoutWithoutDeadline(t *testing.T) {
	t.Setenv(envRedisReadTimeout, "500ms")
	t.Setenv(envRedisMinIdleConns, "0")
	srv := startSilentServer(t)
	client, err := NewClient(srv.url)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// No context deadline: the 500ms read timeout ends each attempt. Four
	// attempts plus at most 1s of backoff between them stay well under 10s.
	timedSet(context.Background(), t, srv, client, 10*time.Second)
}
