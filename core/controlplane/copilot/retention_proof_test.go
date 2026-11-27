package copilot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// Retention proofs cited by docs/contract/deletion-proofs.md. Each proof writes
// a marker through the production writer with its retention shortened, requires
// tracescan to find it in every key family it names, shows it survives until
// just before the shortened TTL, moves miniredis just past it, then requires no
// trace in the writer client's keyspace.
// Exports: this writer reaches none; it writes only the Redis keys named here.

// TestRetentionProofDIC47 covers the session record and the per-user session
// index. NewRedisStore reads COPILOT_SESSION_TTL_HOURS once, so the env is set
// before the store is built.
func TestRetentionProofDIC47(t *testing.T) {
	const ttl = time.Hour
	t.Setenv("COPILOT_SESSION_TTL_HOURS", "1")
	mr, client, marker := newProofRedis(t)
	s := NewRedisStore(client)
	msg := CopilotMessage{Role: "user", Content: "prompt " + marker, JobIDs: []string{marker + "-job"}}
	if err := s.AppendMessage(context.Background(), marker+"-tenant", marker+"-user", marker+"-session", msg); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "copilot:session:"+marker, "copilot:sessions:"+marker)
	requireNoTrace(t, client, marker)
}

func newProofRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
}

// expireJustPast requires the marker in each family, then again one second
// before ttl, then moves miniredis one second past ttl.
func expireJustPast(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient, ttl time.Duration, marker string, families ...string) {
	t.Helper()
	requireTrace(t, client, marker, families...)
	mr.FastForward(ttl - time.Second)
	requireTrace(t, client, marker, families...)
	mr.FastForward(2 * time.Second)
}

func requireTrace(t *testing.T, client redis.UniversalClient, marker string, families ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	for _, family := range families {
		if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("no %s* trace of %q; hits=%v", family, marker, hits)
		}
	}
}

func requireNoTrace(t *testing.T, client redis.UniversalClient, marker string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("trace of %q remains after cleanup: %v", marker, hits)
	}
}

// TestRetentionProofDIC47MessageCap drives the COPILOT_SESSION_MAX_MESSAGES cap:
// once the marker message is trimmed, no trace of it may remain while the
// session key itself still exists.
func TestRetentionProofDIC47MessageCap(t *testing.T) {
	t.Setenv("COPILOT_SESSION_MAX_MESSAGES", "2")
	_, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisStore(client)
	const tenant, user, session = "tenant-cap", "user-cap", "session-cap"
	key := "copilot:session:" + tenant + ":" + session
	if err := s.AppendMessage(ctx, tenant, user, session, CopilotMessage{Role: "user", Content: marker + " first prompt"}); err != nil {
		t.Fatalf("AppendMessage(first): %v", err)
	}
	requireTrace(t, client, marker, key)
	for _, content := range []string{"second prompt", "third prompt"} {
		if err := s.AppendMessage(ctx, tenant, user, session, CopilotMessage{Role: "user", Content: content}); err != nil {
			t.Fatalf("AppendMessage(%s): %v", content, err)
		}
	}
	got, err := s.GetSession(ctx, tenant, session, user)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 2 || got.Messages[0].Content != "second prompt" {
		t.Fatalf("cap kept %d messages (%+v); want the 2 newest", len(got.Messages), got.Messages)
	}
	if n, err := client.Exists(ctx, key).Result(); err != nil || n != 1 {
		t.Fatalf("Exists(%s) = %d, %v; want the session still stored", key, n, err)
	}
	requireNoTrace(t, client, marker)
}
