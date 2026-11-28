package auth

import (
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRedisStoreConstructorsCloseClientWhenPingFails(t *testing.T) {
	t.Setenv("REDIS_CLUSTER_ADDRESSES", "")
	for _, tc := range []struct {
		name string
		new  func(string) (bool, error)
	}{
		{"keys", func(url string) (bool, error) {
			store, err := NewRedisKeyStore(url)
			return store == nil, err
		}},
		{"users", func(url string) (bool, error) {
			store, err := NewRedisUserStore(url)
			return store == nil, err
		}},
		{"rbac", func(url string) (bool, error) {
			store, err := NewRBACStore(url)
			return store == nil, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := miniredis.RunT(t)
			srv.SetError("ERR injected ping failure")
			isNil, err := tc.new(testRedisURL(srv.Addr()))
			if !isNil {
				t.Fatal("expected nil store after failed ping")
			}
			if err == nil || err.Error() != "redis ping: ERR injected ping failure" {
				t.Fatalf("unexpected ping error: %v", err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for srv.CurrentConnectionCount() != 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if count := srv.CurrentConnectionCount(); count != 0 {
				t.Fatalf("connections left after failed ping: %d", count)
			}
		})
	}
}
