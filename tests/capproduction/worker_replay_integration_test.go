//go:build capproduction

package capproduction

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	capworker "github.com/cordum-io/cap/v2/sdk/go/worker"
	"github.com/redis/go-redis/v9"
)

var beginWorkerReplay = redis.NewScript(`
local digest = redis.call('HGET', KEYS[1], 'digest')
if digest and digest ~= ARGV[1] then return {'conflict'} end
local state = redis.call('HGET', KEYS[1], 'state')
if state == 'complete' then
  return {'complete', redis.call('HGET', KEYS[1], 'trace'), redis.call('HGET', KEYS[1], 'result')}
end
local leaseUntil = tonumber(redis.call('HGET', KEYS[1], 'lease_until') or '0')
if state == 'processing' and leaseUntil > tonumber(ARGV[2]) then return {'pending'} end
redis.call('HSET', KEYS[1], 'digest', ARGV[1], 'state', 'processing', 'lease', ARGV[3], 'lease_until', ARGV[4])
redis.call('PEXPIREAT', KEYS[1], ARGV[5])
return {'process', ARGV[3]}
`)

var completeWorkerReplay = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'state') ~= 'processing' then return 0 end
if redis.call('HGET', KEYS[1], 'lease') ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[1], 'state', 'complete', 'trace', ARGV[2], 'result', ARGV[3])
redis.call('HDEL', KEYS[1], 'lease', 'lease_until')
return 1
`)

var renewWorkerReplay = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'state') ~= 'processing' then return 0 end
if redis.call('HGET', KEYS[1], 'lease') ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[1], 'lease_until', ARGV[2])
return 1
`)

var abortWorkerReplay = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'state') ~= 'processing' then return 0 end
if redis.call('HGET', KEYS[1], 'lease') ~= ARGV[1] then return 0 end
return redis.call('DEL', KEYS[1])
`)

type redisWorkerReplay struct {
	client redis.UniversalClient
	prefix string
}

func newRedisWorkerReplay(client redis.UniversalClient, prefix string) *redisWorkerReplay {
	return &redisWorkerReplay{client: client, prefix: prefix}
}

func (s *redisWorkerReplay) Durable() bool { return s != nil && s.client != nil }

func (s *redisWorkerReplay) Begin(
	ctx context.Context, entry capworker.ManagedReplayEntry,
) (capworker.ManagedReplayClaim, error) {
	if !s.Durable() || ctx == nil || ctx.Err() != nil {
		return capworker.ManagedReplayClaim{}, errors.New("worker replay unavailable")
	}
	lease, err := randomReplayLease()
	if err != nil {
		return capworker.ManagedReplayClaim{}, err
	}
	result, err := beginWorkerReplay.Run(ctx, s.client, []string{s.key(entry)},
		hex.EncodeToString(entry.Digest), time.Now().UnixMilli(), lease,
		entry.LeaseUntil.UnixMilli(), entry.ExpiresAt.UnixMilli()).StringSlice()
	if err != nil || len(result) == 0 {
		return capworker.ManagedReplayClaim{}, fmt.Errorf("worker replay begin: %w", err)
	}
	switch result[0] {
	case "process":
		return capworker.ManagedReplayClaim{State: capworker.ManagedReplayProcess, LeaseID: result[1]}, nil
	case "pending":
		return capworker.ManagedReplayClaim{State: capworker.ManagedReplayPending}, nil
	case "complete":
		wire, decodeErr := base64.RawStdEncoding.DecodeString(result[2])
		return capworker.ManagedReplayClaim{State: capworker.ManagedReplayCompleted,
			Outcome: capworker.ManagedReplayOutcome{TraceID: result[1], Result: wire}}, decodeErr
	case "conflict":
		return capworker.ManagedReplayClaim{}, capsdk.ErrReplayConflict
	default:
		return capworker.ManagedReplayClaim{}, errors.New("worker replay invalid state")
	}
}

func (s *redisWorkerReplay) Renew(
	ctx context.Context, entry capworker.ManagedReplayEntry, leaseID string, until time.Time,
) error {
	result, err := renewWorkerReplay.Run(
		ctx, s.client, []string{s.key(entry)}, leaseID, until.UnixMilli(),
	).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("worker replay lease lost")
	}
	return nil
}

func (s *redisWorkerReplay) Complete(
	ctx context.Context, entry capworker.ManagedReplayEntry, leaseID string, outcome capworker.ManagedReplayOutcome,
) error {
	result, err := completeWorkerReplay.Run(ctx, s.client, []string{s.key(entry)}, leaseID,
		outcome.TraceID, base64.RawStdEncoding.EncodeToString(outcome.Result)).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("worker replay lease lost")
	}
	return nil
}

func (s *redisWorkerReplay) Abort(
	ctx context.Context, entry capworker.ManagedReplayEntry, leaseID string,
) error {
	result, err := abortWorkerReplay.Run(ctx, s.client, []string{s.key(entry)}, leaseID).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("worker replay lease lost")
	}
	return nil
}

func (s *redisWorkerReplay) key(entry capworker.ManagedReplayEntry) string {
	hash := sha256.New()
	for _, part := range [][]byte{[]byte(entry.Tenant), []byte(entry.Audience), []byte(entry.Sender), entry.MessageID} {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(part)
	}
	return s.prefix + hex.EncodeToString(hash.Sum(nil))
}

func randomReplayLease() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func TestRedisWorkerReplayLifecycle(t *testing.T) {
	store := newRedisWorkerReplayFixture(t)
	now := time.Now()
	entry := capworker.ManagedReplayEntry{
		Tenant: "tenant-a", Audience: "worker.a.jobs", Sender: "cordum-scheduler",
		MessageID: randomBytes(t, 16), Digest: bytes.Repeat([]byte{0x42}, 32),
		LeaseUntil: now.Add(time.Second), ExpiresAt: now.Add(time.Minute),
	}
	first, err := store.Begin(context.Background(), entry)
	if err != nil || first.State != capworker.ManagedReplayProcess || first.LeaseID == "" {
		t.Fatalf("first replay claim = (%+v, %v), want process", first, err)
	}
	assertRedisReplayLifecycle(t, store, entry, first, now)
}

func newRedisWorkerReplayFixture(t *testing.T) *redisWorkerReplay {
	t.Helper()
	client := connectProductionRedis(t, requiredEnvironment(t, "CAP_PRODUCTION_REDIS_URL"))
	t.Cleanup(func() { _ = client.Close() })
	return newRedisWorkerReplay(client, "cap:worker-replay-test:"+randomHex(t, 6)+":")
}

func assertRedisReplayLifecycle(
	t *testing.T, store *redisWorkerReplay, entry capworker.ManagedReplayEntry,
	first capworker.ManagedReplayClaim, now time.Time,
) {
	t.Helper()
	pending, err := store.Begin(context.Background(), entry)
	if err != nil || pending.State != capworker.ManagedReplayPending {
		t.Fatalf("pending replay claim = (%+v, %v), want pending", pending, err)
	}
	if err := store.Renew(context.Background(), entry, "wrong-lease", now.Add(2*time.Second)); err == nil {
		t.Fatal("renew accepted the wrong lease")
	}
	if err := store.Renew(context.Background(), entry, first.LeaseID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("renew active lease: %v", err)
	}
	want := capworker.ManagedReplayOutcome{TraceID: "trace-a", Result: []byte("result-a")}
	if err := store.Complete(context.Background(), entry, first.LeaseID, want); err != nil {
		t.Fatalf("complete replay: %v", err)
	}
	complete, err := store.Begin(context.Background(), entry)
	if err != nil || complete.State != capworker.ManagedReplayCompleted ||
		complete.Outcome.TraceID != want.TraceID || !bytes.Equal(complete.Outcome.Result, want.Result) {
		t.Fatalf("completed replay claim = (%+v, %v)", complete, err)
	}
	conflict := entry
	conflict.Digest = bytes.Repeat([]byte{0x24}, 32)
	if _, err := store.Begin(context.Background(), conflict); !errors.Is(err, capsdk.ErrReplayConflict) {
		t.Fatalf("digest conflict error = %v, want replay conflict", err)
	}
	aborted := entry
	aborted.MessageID = randomBytes(t, 16)
	claim, err := store.Begin(context.Background(), aborted)
	if err != nil {
		t.Fatalf("begin abort fixture: %v", err)
	}
	if err := store.Abort(context.Background(), aborted, claim.LeaseID); err != nil {
		t.Fatalf("abort replay: %v", err)
	}
	reclaimed, err := store.Begin(context.Background(), aborted)
	if err != nil || reclaimed.State != capworker.ManagedReplayProcess {
		t.Fatalf("reclaimed replay = (%+v, %v), want process", reclaimed, err)
	}
}
