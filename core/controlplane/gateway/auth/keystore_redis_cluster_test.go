package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// oneKeyPerMultiHook fails a MULTI that names more than one distinct key, and
// a WATCH of more than one key. On one node that means one slot, which a real
// Redis Cluster requires and a Watch callback's MULTI never checks client-side.
type oneKeyPerMultiHook struct{}

func (oneKeyPerMultiHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (oneKeyPerMultiHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), "watch") && len(cmd.Args()) > 2 {
			return fmt.Errorf("watch of %d keys", len(cmd.Args())-1)
		}
		return next(ctx, cmd)
	}
}

func (oneKeyPerMultiHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) == 0 || !strings.EqualFold(cmds[0].Name(), "multi") {
			return next(ctx, cmds)
		}
		keys := map[any]bool{}
		for _, cmd := range cmds {
			if name := strings.ToLower(cmd.Name()); name != "multi" && name != "exec" && len(cmd.Args()) > 1 {
				keys[cmd.Args()[1]] = true
			}
		}
		if len(keys) > 1 {
			return fmt.Errorf("multi over %d keys", len(keys))
		}
		return next(ctx, cmds)
	}
}

// failCmdHook fails every command name on key without sending it.
type failCmdHook struct {
	name, key string
}

func (failCmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h failCmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if args := cmd.Args(); strings.EqualFold(cmd.Name(), h.name) && len(args) > 1 && args[1] == h.key {
			return fmt.Errorf("injected %s failure", h.name)
		}
		return next(ctx, cmd)
	}
}

func (failCmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func newClusterTestKey(t *testing.T) (*ManagedKey, string) {
	t.Helper()
	t.Setenv("CORDUM_BCRYPT_COST", "4")
	raw, err := GenerateRawKey()
	if err != nil {
		t.Fatalf("generate raw key: %v", err)
	}
	return &ManagedKey{Name: "k", Tenant: "tenant-a", Scopes: []string{"admin"}, CreatedAt: time.Now().UTC()}, raw
}

func listedIDs(ctx context.Context, t *testing.T, store *RedisKeyStore, tenant string) []string {
	t.Helper()
	keys, err := store.List(ctx, tenant)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	return ids
}

func isMember(ctx context.Context, t *testing.T, client redis.UniversalClient, set, id string) bool {
	t.Helper()
	ok, err := client.SIsMember(ctx, set, id).Result()
	if err != nil {
		t.Fatalf("SISMEMBER %s: %v", set, err)
	}
	return ok
}

func storedKey(ctx context.Context, t *testing.T, client redis.UniversalClient, id string) ManagedKey {
	t.Helper()
	raw, err := client.Get(ctx, keyRecordKey(id)).Bytes()
	if err != nil {
		t.Fatalf("GET record %s: %v", id, err)
	}
	mk, err := unmarshalManagedKey(raw)
	if err != nil {
		t.Fatalf("unmarshal record %s: %v", id, err)
	}
	return mk
}

func TestKeyStoreCreateRevokeOnCluster(t *testing.T) {
	ctx := context.Background()
	client := testredis.NewMiniClusterClient(t, newTestMiniredis(t).Addr())
	store := NewRedisKeyStoreFromClient(client)
	mk, raw := newClusterTestKey(t)

	if err := store.Create(ctx, mk, raw); err != nil {
		t.Fatalf("Create on cluster: %v", err)
	}
	if got, err := store.ValidateKey(ctx, raw); err != nil || got.ID != mk.ID {
		t.Fatalf("ValidateKey after Create = %v, %v; want key %s", got, err, mk.ID)
	}
	if ids := listedIDs(ctx, t, store, mk.Tenant); !slices.Equal(ids, []string{mk.ID}) {
		t.Fatalf("List after Create = %v; want [%s]", ids, mk.ID)
	}

	if err := store.Revoke(ctx, mk.ID, mk.Tenant); err != nil {
		t.Fatalf("Revoke on cluster: %v", err)
	}
	if !storedKey(ctx, t, client, mk.ID).Revoked {
		t.Fatal("record not revoked after Revoke")
	}
	if isMember(ctx, t, client, keyPrefixIndexKey(mk.Prefix), mk.ID) {
		t.Fatal("prefix index entry left after Revoke")
	}
	if _, err := store.ValidateKey(ctx, raw); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("ValidateKey after Revoke error = %v; want ErrInvalidKey", err)
	}
	if ids := listedIDs(ctx, t, store, mk.Tenant); len(ids) != 0 {
		t.Fatalf("List after Revoke = %v; want none", ids)
	}
}

func TestKeyStoreMultiWritesOneKey(t *testing.T) {
	ctx := context.Background()
	client := newTestRedisClient(t, newTestMiniredis(t).Addr())
	client.AddHook(oneKeyPerMultiHook{})
	store := NewRedisKeyStoreFromClient(client)
	mk, raw := newClusterTestKey(t)

	if err := store.Create(ctx, mk, raw); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.ValidateKey(ctx, raw); err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	if err := store.Revoke(ctx, mk.ID, mk.Tenant); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := store.ValidateKey(ctx, raw); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("ValidateKey after Revoke error = %v; want ErrInvalidKey", err)
	}
}

// A crash between Revoke's CAS and its SREM leaves the prefix entry; the
// revoked record must still refuse the key.
func TestRevokedKeyRefusedWithPrefixEntryLeft(t *testing.T) {
	ctx := context.Background()
	client := newTestRedisClient(t, newTestMiniredis(t).Addr())
	mk, raw := newClusterTestKey(t)
	prefix, err := rawKeyPrefix(raw)
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	client.AddHook(failCmdHook{name: "srem", key: keyPrefixIndexKey(prefix)})
	store := NewRedisKeyStoreFromClient(client)

	if err := store.Create(ctx, mk, raw); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Revoke(ctx, mk.ID, mk.Tenant); err != nil {
		t.Fatalf("Revoke with a failing SREM = %v; want nil, the revoke took effect", err)
	}
	if !storedKey(ctx, t, client, mk.ID).Revoked {
		t.Fatal("record not revoked")
	}
	if !isMember(ctx, t, client, keyPrefixIndexKey(prefix), mk.ID) {
		t.Fatal("prefix entry gone; the test needs it left in place")
	}
	if got, err := store.ValidateKey(ctx, raw); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("ValidateKey with the prefix entry left = %v, %v; want ErrInvalidKey", got, err)
	}
}

func TestHalfCreatedKeyNeverAccepted(t *testing.T) {
	ctx := context.Background()

	t.Run("record write fails", func(t *testing.T) {
		client := newTestRedisClient(t, newTestMiniredis(t).Addr())
		mk, raw := newClusterTestKey(t)
		mk.ID = "half-1"
		client.AddHook(failCmdHook{name: "set", key: keyRecordKey(mk.ID)})
		store := NewRedisKeyStoreFromClient(client)
		prefix, err := rawKeyPrefix(raw)
		if err != nil {
			t.Fatalf("prefix: %v", err)
		}

		if err := store.Create(ctx, mk, raw); err == nil {
			t.Fatal("Create with a failing record SET returned nil")
		}
		if isMember(ctx, t, client, keyPrefixIndexKey(prefix), mk.ID) || isMember(ctx, t, client, keyTenantKey(mk.Tenant), mk.ID) {
			t.Fatal("an index names a key whose record was never written")
		}
		if got, err := store.ValidateKey(ctx, raw); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("ValidateKey = %v, %v; want ErrInvalidKey", got, err)
		}
	})

	t.Run("indexes without a record", func(t *testing.T) {
		store, _ := newTestKeyStore(t)
		mk, raw := newClusterTestKey(t)
		prefix, err := rawKeyPrefix(raw)
		if err != nil {
			t.Fatalf("prefix: %v", err)
		}
		for _, set := range []string{keyPrefixIndexKey(prefix), keyTenantKey(mk.Tenant)} {
			if err := store.client.SAdd(ctx, set, "ghost-1").Err(); err != nil {
				t.Fatalf("seed %s: %v", set, err)
			}
		}
		if got, err := store.ValidateKey(ctx, raw); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("ValidateKey = %v, %v; want ErrInvalidKey", got, err)
		}
		if ids := listedIDs(ctx, t, store, mk.Tenant); len(ids) != 0 {
			t.Fatalf("List = %v; want none", ids)
		}
	})
}
