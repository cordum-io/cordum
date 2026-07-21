//go:build capproduction

package capproduction

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

type staticTrustResolver struct {
	identity *scheduler.HandshakeTrustIdentity
}

func (r *staticTrustResolver) Resolve(
	_ context.Context, workerID, keyID string,
) (*scheduler.HandshakeTrustIdentity, error) {
	if r == nil || r.identity == nil || workerID != r.identity.WorkerID || keyID != r.identity.ProofKeyID {
		return nil, errors.New("worker proof authority unavailable")
	}
	copy := *r.identity
	copy.AllowedTopics = append([]string(nil), r.identity.AllowedTopics...)
	return &copy, nil
}

type productionAudit struct{}

func (productionAudit) Emit(context.Context, audit.SIEMEvent) {}

type switchableReplay struct {
	delegate capsdk.ReplayStore
	failNext atomic.Bool
}

func (s *switchableReplay) Admit(
	tenant, audience, sender string, messageID, digest []byte, expiry time.Time,
) (capsdk.ReplayOutcome, error) {
	if s == nil || s.delegate == nil || s.failNext.CompareAndSwap(true, false) {
		return 0, capsdk.ErrReplayStoreUnavailable
	}
	return s.delegate.Admit(tenant, audience, sender, messageID, digest, expiry)
}

type directStrategy struct{ workerID string }

func (s directStrategy) PickSubject(
	_ *pb.JobRequest, workers map[string]*pb.Heartbeat, _ map[string]scheduler.WorkerReadiness,
) (string, error) {
	if workers[s.workerID] == nil {
		return "", errors.New("production worker unavailable")
	}
	return bus.DirectSubject(s.workerID), nil
}

type recordingSafety struct {
	mu     sync.Mutex
	tenant string
	calls  int
	err    error
}

func (s *recordingSafety) Check(
	_ context.Context, request *pb.JobRequest,
) (scheduler.SafetyDecisionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	identity, meta := request.GetIdentity(), request.GetMeta()
	if identity == nil || meta == nil || request.GetTenantId() != s.tenant ||
		request.GetPrincipalId() != identity.GetPrincipalId() || meta.GetTenantId() != s.tenant ||
		meta.GetActorId() != identity.GetActorId() {
		s.err = errors.New("safety received noncanonical identity")
		return scheduler.SafetyDecisionRecord{Decision: scheduler.SafetyDeny}, s.err
	}
	return scheduler.SafetyDecisionRecord{
		Decision: scheduler.SafetyAllow, PolicySnapshot: "snapshot-production-1",
	}, nil
}

func (s *recordingSafety) snapshot() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.err
}

func connectProductionRedis(t *testing.T, rawURL string) redis.UniversalClient {
	t.Helper()
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("parse CAP_PRODUCTION_REDIS_URL: %v", err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("external Redis is unreachable: %v", err)
	}
	return client
}

func newProductionIssuer(
	t *testing.T, client redis.UniversalClient,
) *scheduler.SessionTokenIssuer {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate token key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("production-token-key", publicKey); err != nil {
		t.Fatalf("add token key: %v", err)
	}
	issuer, err := scheduler.NewSessionTokenIssuer(
		privateKey, "production-token-key", trust, client,
		scheduler.SessionTokenIssuerOptions{Lifetime: 5 * time.Minute, Skew: 5 * time.Second},
	)
	if err != nil {
		t.Fatalf("new token issuer: %v", err)
	}
	return issuer
}

func requiredEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the declared CAP-PRODUCTION gate", name)
	}
	return value
}

func (h *productionHarness) awaitDurableResult(t *testing.T, jobID string, eventCount int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		count := h.runtimeEventCount(t, jobID)
		pendingID := h.pendingEffectID(t, jobID)
		if count == eventCount && pendingID == "" {
			return
		}
		if count > eventCount || time.Now().After(deadline) {
			t.Fatalf("job %s durable result = events %d, pending %q; want events %d, no pending effect",
				jobID, count, pendingID, eventCount)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *productionHarness) pendingEffectID(t *testing.T, jobID string) string {
	t.Helper()
	effects, err := h.store.PendingJobEffects(context.Background(), 100)
	if err != nil {
		t.Fatalf("pending durable effects: %v", err)
	}
	for _, effect := range effects {
		if effect.JobID == jobID {
			return effect.EventID
		}
	}
	return ""
}

func generateP256(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	return key
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("random value: %v", err)
	}
	return hex.EncodeToString(value)
}
