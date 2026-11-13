package configsvc

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

// setAllCountHook counts every command and pipeline a client sends.
type setAllCountHook struct{ n *atomic.Int64 }

func (h setAllCountHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h setAllCountHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}

func (h setAllCountHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

// setAllRewriteHook stores value at key through another client right after
// the first GET of key, so a transaction watching key must abort.
type setAllRewriteHook struct {
	key, value string
	other      *redis.Client
	fired      *atomic.Bool
}

func (h setAllRewriteHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h setAllRewriteHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if strings.EqualFold(cmd.Name(), "get") && len(args) > 1 && args[1] == h.key && h.fired.CompareAndSwap(false, true) {
			_ = h.other.Set(ctx, h.key, h.value, 0).Err()
		}
		return err
	}
}

func (h setAllRewriteHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func setAllDoc(id, value string, revision int64) *Document {
	return &Document{Scope: ScopeSystem, ScopeID: id, Data: map[string]any{"v": value}, Revision: revision}
}

func setAllRequireStored(t *testing.T, svc *Service, id, value string, revision int64) {
	t.Helper()
	doc, err := svc.Get(context.Background(), ScopeSystem, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if doc.Revision != revision || doc.Data["v"] != value {
		t.Fatalf("%s: got revision %d value %v, want %d %q", id, doc.Revision, doc.Data["v"], revision, value)
	}
}

func setAllRaw(t *testing.T, get func(string) (string, error), keys ...string) []string {
	t.Helper()
	out := make([]string, len(keys))
	for i, key := range keys {
		raw, err := get(key)
		if err != nil {
			t.Fatalf("raw %s: %v", key, err)
		}
		out[i] = raw
	}
	return out
}

func TestSetAll_CommitsEveryDocumentInOneTransaction(t *testing.T) {
	svc, srv := newSvcWithServer(t)
	t.Cleanup(func() { _ = svc.Close(); srv.Close() })
	ctx := context.Background()

	a, b := setAllDoc("pair_a{t}", "a1", 0), setAllDoc("pair_b{t}", "b1", 0)
	if err := svc.SetAll(ctx, a, b); err != nil {
		t.Fatalf("first SetAll: %v", err)
	}
	setAllRequireStored(t, svc, "pair_a{t}", "a1", 1)
	setAllRequireStored(t, svc, "pair_b{t}", "b1", 1)

	a.Data["v"], b.Data["v"] = "a2", "b2"
	if err := svc.SetAll(ctx, a, b); err != nil {
		t.Fatalf("second SetAll: %v", err)
	}
	if a.Revision != 2 || b.Revision != 2 {
		t.Fatalf("in-memory revisions %d %d, want 2 2", a.Revision, b.Revision)
	}
	setAllRequireStored(t, svc, "pair_a{t}", "a2", 2)
	setAllRequireStored(t, svc, "pair_b{t}", "b2", 2)
}

func TestSetAll_RevisionConflictLeavesEveryDocumentUnchanged(t *testing.T) {
	svc, srv := newSvcWithServer(t)
	t.Cleanup(func() { _ = svc.Close(); srv.Close() })
	ctx := context.Background()
	keys := []string{"cfg:system:pair_a{t}", "cfg:system:pair_b{t}"}
	if err := svc.SetAll(ctx, setAllDoc("pair_a{t}", "a1", 0), setAllDoc("pair_b{t}", "b1", 0)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := setAllRaw(t, srv.Get, keys...)

	a, b := setAllDoc("pair_a{t}", "a2", 1), setAllDoc("pair_b{t}", "b2", 0)
	if err := svc.SetAll(ctx, a, b); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale second document: got %v, want ErrRevisionConflict", err)
	}
	if after := setAllRaw(t, srv.Get, keys...); after[0] != before[0] || after[1] != before[1] {
		t.Fatalf("stored documents changed:\nbefore %q\nafter  %q", before, after)
	}
	if a.Revision != 1 || b.Revision != 0 {
		t.Fatalf("in-memory revisions %d %d, want 1 0", a.Revision, b.Revision)
	}

	t.Run("watched document changes before the commit", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
		other := redis.NewClient(&redis.Options{Addr: srv.Addr()})
		t.Cleanup(func() { _ = client.Close(); _ = other.Close() })
		var fired atomic.Bool
		rewritten := `{"scope":"system","scope_id":"pair_b{t}","data":{"v":"other"},"revision":2}`
		client.AddHook(setAllRewriteHook{key: keys[1], value: rewritten, other: other, fired: &fired})

		a, b := setAllDoc("pair_a{t}", "a3", 1), setAllDoc("pair_b{t}", "b3", 1)
		if err := NewFromClient(client).SetAll(ctx, a, b); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("got %v, want ErrRevisionConflict", err)
		}
		if !fired.Load() {
			t.Fatal("rewrite hook never fired")
		}
		after := setAllRaw(t, srv.Get, keys...)
		if after[0] != before[0] || after[1] != rewritten {
			t.Fatalf("got %q, want first unchanged and second rewritten", after)
		}
		if a.Revision != 1 || b.Revision != 1 {
			t.Fatalf("in-memory revisions %d %d, want 1 1", a.Revision, b.Revision)
		}
	})
}

func TestSetAll_RefusesDocumentsInDifferentClusterSlotsBeforeAnyWrite(t *testing.T) {
	cases := []struct {
		name     string
		docs     []*Document
		crossErr bool
	}{
		{"policy and policy_audit", []*Document{setAllDoc("policy", "p", 0), setAllDoc("policy_audit", "q", 0)}, true},
		{"same key twice", []*Document{setAllDoc("pair_a{t}", "a", 0), setAllDoc("pair_a{t}", "b", 0)}, false},
		{"no documents", nil, false},
		{"document without scope", []*Document{setAllDoc("pair_a{t}", "a", 0), {ScopeID: "pair_b{t}"}}, false},
		{"org document without scope_id", []*Document{{Scope: ScopeOrg}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, srv := newSvcWithServer(t)
			t.Cleanup(func() { _ = svc.Close(); srv.Close() })
			var calls atomic.Int64
			svc.client.AddHook(setAllCountHook{n: &calls})

			err := svc.SetAll(context.Background(), tc.docs...)
			if err == nil || errors.Is(err, ErrCrossSlot) != tc.crossErr {
				t.Fatalf("got %v, want an error (ErrCrossSlot: %v)", err, tc.crossErr)
			}
			if n := calls.Load(); n != 0 {
				t.Fatalf("SetAll sent %d Redis calls before refusing, want 0", n)
			}
			if keys := srv.Keys(); len(keys) != 0 {
				t.Fatalf("refused SetAll wrote %v", keys)
			}
			for _, doc := range tc.docs {
				if doc != nil && doc.Revision != 0 {
					t.Fatalf("refused SetAll changed a revision to %d", doc.Revision)
				}
			}
		})
	}
}

func TestClusterKeySlot_MatchesRedisReferenceSlots(t *testing.T) {
	for key, want := range map[string]int{
		"foo":                  12182,
		"bar":                  5061,
		"somekey":              11058,
		"{user1000}.following": 3443,
		"{user1000}.followers": 3443,
		"foo{}{bar}":           8363,
		"cfg:system:policy":    4150,
		"cfg:system:policy_write_audit{cfg:system:policy}": 4150,
		"cfg:system:policy_audit":                          11215,
	} {
		if got := clusterKeySlot(key); got != want {
			t.Errorf("clusterKeySlot(%q) = %d, want %d", key, got, want)
		}
	}
}
