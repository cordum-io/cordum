package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/redis/go-redis/v9"
)

func TestHandleGetMemoryCompatibilityBoundsRedisData(t *testing.T) {
	s, _, _ := newTestGateway(t)
	redisStore := s.memStore.(*store.RedisStore)
	client := redisStore.Client()
	ctx := context.Background()
	if err := s.jobStore.SetTenant(ctx, "limit", "tenant-a"); err != nil {
		t.Fatalf("SetTenant() error = %v", err)
	}
	tests := []struct {
		name  string
		key   string
		setup func(redis.UniversalClient) error
	}{
		{
			name: "string bytes", key: "mem:limit:string",
			setup: func(client redis.UniversalClient) error {
				return client.Set(ctx, "mem:limit:string", strings.Repeat("x", maxResolvedMemoryBytes+1), 0).Err()
			},
		},
		{
			name: "list entries", key: "mem:limit:list",
			setup: func(client redis.UniversalClient) error {
				items := make([]any, maxLegacyMemoryEntries+1)
				for i := range items {
					items[i] = "x"
				}
				return client.RPush(ctx, "mem:limit:list", items...).Err()
			},
		},
		{
			name: "list cumulative bytes", key: "mem:limit:list-bytes",
			setup: func(client redis.UniversalClient) error {
				item := strings.Repeat("x", maxResolvedMemoryBytes/2+1)
				return client.RPush(ctx, "mem:limit:list-bytes", item, item).Err()
			},
		},
		{
			name: "set entries", key: "mem:limit:set",
			setup: func(client redis.UniversalClient) error {
				items := make([]any, maxLegacyMemoryEntries+1)
				for i := range items {
					items[i] = strings.Repeat("0", 8) + string(rune(0x1000+i))
				}
				return client.SAdd(ctx, "mem:limit:set", items...).Err()
			},
		},
		{
			name: "hash entries", key: "mem:limit:hash",
			setup: func(client redis.UniversalClient) error {
				values := make(map[string]any, maxLegacyMemoryEntries+1)
				for i := 0; i <= maxLegacyMemoryEntries; i++ {
					values[strings.Repeat("f", i%10+1)+string(rune(0x1000+i))] = "x"
				}
				return client.HSet(ctx, "mem:limit:hash", values).Err()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.setup(client); err != nil {
				t.Fatalf("setup error = %v", err)
			}
			req := withAuth(
				httptest.NewRequest(http.MethodGet, "/api/v1/memory?key="+test.key, nil),
				&auth.AuthContext{Tenant: "tenant-a", Role: "admin"},
			)
			rec := httptest.NewRecorder()
			s.handleGetMemory(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413; body bytes = %d", rec.Code, rec.Body.Len())
			}
		})
	}
}
