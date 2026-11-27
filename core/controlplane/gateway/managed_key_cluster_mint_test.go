package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

// mintClusterHook models the cross-slot transaction refusal that miniredis
// does not implement, and can revoke after CreateChild's first parent check.
type mintClusterHook struct {
	key   string
	after func(context.Context)
	once  sync.Once
}

func (*mintClusterHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *mintClusterHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "watch" && len(cmd.Args()) > 2 {
			return fmt.Errorf("watch of %d keys", len(cmd.Args())-1)
		}
		err := next(ctx, cmd)
		if err == nil && cmd.Name() == "get" && len(cmd.Args()) == 2 && cmd.Args()[1] == h.key && h.after != nil {
			h.once.Do(func() { h.after(ctx) })
		}
		return err
	}
}

func (*mintClusterHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) > 0 && cmds[0].Name() == "multi" {
			keys := make(map[string]bool)
			for _, cmd := range cmds {
				if cmd.Name() != "multi" && cmd.Name() != "exec" && len(cmd.Args()) > 1 {
					key, ok := cmd.Args()[1].(string)
					if !ok {
						return fmt.Errorf("non-string key in %s", cmd.Name())
					}
					keys[key] = true
				}
			}
			if len(keys) > 1 {
				return fmt.Errorf("multi over %d keys", len(keys))
			}
		}
		return next(ctx, cmds)
	}
}

func TestManagedKeyMint_CrossSlotMultiRefused(t *testing.T) {
	t.Setenv("CORDUM_BCRYPT_COST", "4")
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "revoked during create"}[revoke], func(t *testing.T) {
			f := newEdgeScopeFixture(t)
			parent, req := managedRoleAuthenticatedMint(t, f)
			client := replicaRedisClient(t, f)
			revoked := false
			hook := &mintClusterHook{key: "apikey:" + parent.ID}
			if revoke {
				hook.after = func(ctx context.Context) {
					if err := f.keys.Revoke(ctx, parent.ID, "default"); err != nil {
						t.Errorf("replica Revoke: %v", err)
					}
					revoked = true
				}
			}
			client.AddHook(hook)
			f.s.keyStore = auth.NewRedisKeyStoreFromClient(client)
			rec := httptest.NewRecorder()
			f.s.handleCreateKey(rec, req)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if revoke {
				if rec.Code != http.StatusForbidden || !revoked || body["secret"] != nil {
					t.Fatalf("status=%d revoked=%v body=%s; want 403, no secret", rec.Code, revoked, rec.Body)
				}
				assertOnlyKey(t, f, parent.ID)
				return
			}
			var secret string
			if rec.Code != http.StatusCreated || json.Unmarshal(body["secret"], &secret) != nil || secret == "" {
				t.Fatalf("status=%d body=%s; want 201 and secret", rec.Code, rec.Body)
			}
			if got, err := f.keys.ValidateKey(context.Background(), secret); err != nil || got == nil || got.ID == parent.ID {
				t.Fatalf("ValidateKey(child) = %v, %v", got, err)
			}
		})
	}
}
