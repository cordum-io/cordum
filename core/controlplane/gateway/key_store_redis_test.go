package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type failPipelineHook struct {
	err error
}

func (h failPipelineHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h failPipelineHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return next
}

func (h failPipelineHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return h.err
	}
}

type conflictHook struct {
	key     string
	client  *redis.Client
	trigger *atomic.Bool
	always  bool
}

func (h conflictHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h conflictHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if strings.EqualFold(cmd.Name(), "get") && len(cmd.Args()) > 1 {
			if key, ok := cmd.Args()[1].(string); ok && key == h.key {
				if h.always || (h.trigger != nil && h.trigger.CompareAndSwap(false, true)) {
					_ = h.client.Set(ctx, h.key, `{"id":"id-1","tenant":"tenant-a","prefix":"ck_00000000","revoked":false}`, 0).Err()
				}
			}
		}
		return err
	}
}

func (h conflictHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func newTestKeyStore(t *testing.T) (*RedisKeyStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisKeyStore("redis://" + srv.Addr())
	if err != nil {
		srv.Close()
		t.Fatalf("new redis key store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		srv.Close()
	})
	return store, srv
}

func seedManagedKey(ctx context.Context, t *testing.T, store *RedisKeyStore, tenant, id string) {
	t.Helper()
	prefix := "ck_00000000"
	raw := `{"id":"` + id + `","tenant":"` + tenant + `","prefix":"` + prefix + `","revoked":false}`
	if err := store.client.SAdd(ctx, keyTenantKey(tenant), id).Err(); err != nil {
		t.Fatalf("seed tenant set: %v", err)
	}
	if err := store.client.Set(ctx, keyRecordKey(id), raw, 0).Err(); err != nil {
		t.Fatalf("seed key record: %v", err)
	}
	if err := store.client.SAdd(ctx, keyPrefixIndexKey(prefix), id).Err(); err != nil {
		t.Fatalf("seed prefix index: %v", err)
	}
}

func TestListKeys_ConnectionError(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()
	seedManagedKey(ctx, t, store, "tenant-a", "id-1")

	store.client.AddHook(failPipelineHook{err: errors.New("connection error")})
	if _, err := store.List(ctx, "tenant-a"); err == nil {
		t.Fatalf("expected pipeline error")
	}
}

func TestListKeys_IndividualKeyMissing(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()

	if err := store.client.SAdd(ctx, keyTenantKey("tenant-a"), "id-1", "id-2").Err(); err != nil {
		t.Fatalf("seed tenant set: %v", err)
	}
	if err := store.client.Set(ctx, keyRecordKey("id-1"), `{"id":"id-1","tenant":"tenant-a","revoked":false}`, 0).Err(); err != nil {
		t.Fatalf("seed key record: %v", err)
	}

	keys, err := store.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != "id-1" {
		t.Fatalf("expected only id-1, got %#v", keys)
	}
}

func TestListKeys_IndividualKeyError(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()

	if err := store.client.SAdd(ctx, keyTenantKey("tenant-a"), "id-1", "id-2").Err(); err != nil {
		t.Fatalf("seed tenant set: %v", err)
	}
	if err := store.client.Set(ctx, keyRecordKey("id-1"), `{"id":"id-1","tenant":"tenant-a","revoked":false}`, 0).Err(); err != nil {
		t.Fatalf("seed key record: %v", err)
	}
	if err := store.client.LPush(ctx, keyRecordKey("id-2"), "bad").Err(); err != nil {
		t.Fatalf("seed wrong type: %v", err)
	}

	if _, err := store.List(ctx, "tenant-a"); err == nil {
		t.Fatalf("expected error for wrong-type key record")
	}
}

func TestListKeys_EmptyTenant(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()

	keys, err := store.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty list, got %#v", keys)
	}
}

func TestRevoke_WrongTenant(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()
	seedManagedKey(ctx, t, store, "tenant-a", "id-1")

	if err := store.Revoke(ctx, "id-1", "tenant-b"); err == nil || err.Error() != "key not found" {
		t.Fatalf("expected key not found, got %v", err)
	}
	raw, err := store.client.Get(ctx, keyRecordKey("id-1")).Result()
	if err != nil {
		t.Fatalf("get key record: %v", err)
	}
	var mk ManagedKey
	if err := json.Unmarshal([]byte(raw), &mk); err != nil {
		t.Fatalf("unmarshal key record: %v", err)
	}
	if mk.Revoked {
		t.Fatalf("expected key to remain unrevoked")
	}
}

func TestRevoke_TOCTOU(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()
	seedManagedKey(ctx, t, store, "tenant-a", "id-1")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.Revoke(ctx, "id-1", "tenant-a")
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected revoke error: %v", err)
		}
	}

	raw, err := store.client.Get(ctx, keyRecordKey("id-1")).Result()
	if err != nil {
		t.Fatalf("get key record: %v", err)
	}
	var mk ManagedKey
	if err := json.Unmarshal([]byte(raw), &mk); err != nil {
		t.Fatalf("unmarshal key record: %v", err)
	}
	if !mk.Revoked {
		t.Fatalf("expected key revoked after concurrent revoke")
	}
	if exists, err := store.client.SIsMember(ctx, keyPrefixIndexKey("ck_00000000"), "id-1").Result(); err != nil || exists {
		t.Fatalf("expected prefix index entry removed, got exists=%v err=%v", exists, err)
	}
}

func TestRevoke_KeyModifiedDuringRevoke(t *testing.T) {
	store, srv := newTestKeyStore(t)
	ctx := context.Background()
	seedManagedKey(ctx, t, store, "tenant-a", "id-1")

	otherClient := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer otherClient.Close()
	var triggered atomic.Bool
	store.client.AddHook(conflictHook{
		key:     keyRecordKey("id-1"),
		client:  otherClient,
		trigger: &triggered,
	})

	if err := store.Revoke(ctx, "id-1", "tenant-a"); err != nil {
		t.Fatalf("expected revoke to succeed after retry, got %v", err)
	}
	if !triggered.Load() {
		t.Fatalf("expected conflict hook to trigger")
	}
}

func TestRevoke_MaxRetries(t *testing.T) {
	store, srv := newTestKeyStore(t)
	ctx := context.Background()
	seedManagedKey(ctx, t, store, "tenant-a", "id-1")

	otherClient := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer otherClient.Close()
	store.client.AddHook(conflictHook{
		key:    keyRecordKey("id-1"),
		client: otherClient,
		always: true,
	})

	if err := store.Revoke(ctx, "id-1", "tenant-a"); err == nil || !strings.Contains(err.Error(), "too many retries") {
		t.Fatalf("expected max retries error, got %v", err)
	}
}

func TestValidateKey_Success(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()

	rawKey, err := GenerateRawKey()
	if err != nil {
		t.Fatalf("generate raw key: %v", err)
	}

	mk := &ManagedKey{
		Name:      "test-key",
		Tenant:    "tenant-a",
		Scopes:    []string{"admin"},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, mk, rawKey); err != nil {
		t.Fatalf("create key: %v", err)
	}

	got, err := store.ValidateKey(ctx, rawKey)
	if err != nil {
		t.Fatalf("validate key: %v", err)
	}
	if got.ID != mk.ID || got.Tenant != mk.Tenant {
		t.Fatalf("unexpected key: %#v", got)
	}
}

func TestValidateKey_NotFound(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()

	rawKey, err := GenerateRawKey()
	if err != nil {
		t.Fatalf("generate raw key: %v", err)
	}
	if _, err := store.ValidateKey(ctx, rawKey); err == nil {
		t.Fatalf("expected key not found error")
	}
}

func TestRecordUsageConcurrentNoLostIncrements(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()

	keyID := "key-concurrent"
	seedManagedKey(ctx, t, store, "tenant-a", keyID)

	const goroutines = 10
	const increments = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				if err := store.RecordUsage(ctx, keyID); err != nil {
					t.Errorf("record usage: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Read back and verify usage_count == goroutines * increments.
	raw, err := store.client.Get(ctx, keyRecordKey(keyID)).Result()
	if err != nil {
		t.Fatalf("get key: %v", err)
	}
	var mk ManagedKey
	if err := json.Unmarshal([]byte(raw), &mk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	expected := int64(goroutines * increments)
	if mk.UsageCount != expected {
		t.Fatalf("expected usage_count=%d, got %d (lost %d increments)",
			expected, mk.UsageCount, expected-mk.UsageCount)
	}
}
