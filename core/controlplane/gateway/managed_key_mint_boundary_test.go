package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

// replicaRevokeStore revokes the calling key through a second store on its
// own Redis client, as another gateway replica would, right before each write.
type replicaRevokeStore struct {
	*auth.RedisKeyStore
	replica  *auth.RedisKeyStore
	parentID string
}

func (s replicaRevokeStore) Create(ctx context.Context, key *auth.ManagedKey, secret string) error {
	if err := s.replica.Revoke(ctx, s.parentID, "default"); err != nil {
		return err
	}
	return s.RedisKeyStore.Create(ctx, key, secret)
}

func (s replicaRevokeStore) CreateChild(ctx context.Context, parentID string, key *auth.ManagedKey, secret string) error {
	if err := s.replica.Revoke(ctx, s.parentID, "default"); err != nil {
		return err
	}
	return s.RedisKeyStore.CreateChild(ctx, parentID, key, secret)
}

// failingChildStore fails CreateChild with a store error.
type failingChildStore struct{ *auth.RedisKeyStore }

func (failingChildStore) CreateChild(context.Context, string, *auth.ManagedKey, string) error {
	return errors.New("store offline")
}

// keyStoreOnly has the KeyStore methods and no CreateChild.
type keyStoreOnly struct{ auth.KeyStore }

func TestManagedKeyMintBoundary_RefusalStoresNoKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(t *testing.T, f edgeScopeFixture, parentID string) auth.KeyStore
		code  int
	}{
		{"revoked by another replica", func(t *testing.T, f edgeScopeFixture, parentID string) auth.KeyStore {
			replica := auth.NewRedisKeyStoreFromClient(replicaRedisClient(t, f))
			return replicaRevokeStore{RedisKeyStore: f.keys, replica: replica, parentID: parentID}
		}, http.StatusForbidden},
		{"create child error", func(_ *testing.T, f edgeScopeFixture, _ string) auth.KeyStore {
			return failingChildStore{f.keys}
		}, http.StatusInternalServerError},
		{"store without CreateChild", func(_ *testing.T, f edgeScopeFixture, _ string) auth.KeyStore {
			return keyStoreOnly{f.keys}
		}, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEdgeScopeFixture(t)
			parent, req := managedRoleAuthenticatedMint(t, f)
			f.s.keyStore = tc.store(t, f, parent.ID)
			rec := httptest.NewRecorder()
			f.s.handleCreateKey(rec, req)
			_, err := f.keys.ValidateKey(context.Background(), auth.FromRequest(req).APIKey)
			revoked := errors.Is(err, auth.ErrInvalidKey)
			if rec.Code != tc.code || revoked != (tc.code == http.StatusForbidden) {
				t.Fatalf("status=%d calling key revoked=%v (%v), want %d: %s", rec.Code, revoked, err, tc.code, rec.Body.String())
			}
			assertOnlyKey(t, f, parent.ID)
		})
	}
}

// replicaRedisClient opens a second client on the fixture's Redis address and DB.
func replicaRedisClient(t *testing.T, f edgeScopeFixture) *redis.Client {
	t.Helper()
	client, ok := f.s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", f.s.jobStore.Client())
	}
	opts := client.Options()
	return newSharedTestRedisClient(t, testRedisBackend{addr: opts.Addr, db: opts.DB})
}

// assertOnlyKey fails unless parentID is the only key id in a key record,
// the prefix index or the tenant index. It reads Redis directly, because
// List hides revoked keys.
func assertOnlyKey(t *testing.T, f edgeScopeFixture, parentID string) {
	t.Helper()
	ctx := context.Background()
	client := f.s.jobStore.Client()
	keys, err := client.Keys(ctx, "apikey:*").Result()
	if err != nil {
		t.Fatalf("KEYS apikey:*: %v", err)
	}
	found := false
	for _, k := range keys {
		ids := []string{strings.TrimPrefix(k, "apikey:")}
		if strings.HasPrefix(k, "apikey:prefix:") || strings.HasPrefix(k, "apikey:tenant:") {
			if ids, err = client.SMembers(ctx, k).Result(); err != nil {
				t.Fatalf("SMEMBERS %s: %v", k, err)
			}
		}
		for _, id := range ids {
			if id != parentID {
				t.Fatalf("%s holds key %q, want only the calling key %q", k, id, parentID)
			}
		}
		found = found || k == "apikey:"+parentID
	}
	if !found {
		t.Fatalf("no record of the calling key %q among %v", parentID, keys)
	}
}
