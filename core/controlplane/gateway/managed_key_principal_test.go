package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/capprofile"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
)

// These tests pin that a managed (ck_) API key authenticates as its own
// principal, its key id. In the production profile, with header principals
// disabled, it submits a job that records that principal. The other carriers
// keep their checks: a body or header principal never names another identity,
// and an unbound static key is still refused.

const managedKeyPrincipalUnboundKey = "managedkeyprincipal-unbound-static-key-001"

const managedKeyPrincipalMismatch = "authenticated job identity mismatch"

type managedKeyPrincipalFixture struct {
	s        *server
	bus      *stubBus
	safety   *stubSafetyClient
	provider *auth.BasicAuthProvider
	key      *auth.ManagedKey
	secret   string
}

// managedKeyPrincipalSubmit is one POST /api/v1/jobs. Empty fields are left
// out of the request.
type managedKeyPrincipalSubmit struct {
	apiKey, header, bodyPrincipal, label, idem string
}

// newManagedKeyPrincipalFixture serves the production profile with
// CORDUM_ALLOW_HEADER_PRINCIPAL unset. A real BasicAuthProvider holds one
// unbound static key and a Redis key store with one managed key, created the
// way POST /api/v1/auth/keys creates it. agent-a and agent-b are linked to
// worker-a and worker-b.
func newManagedKeyPrincipalFixture(t *testing.T) managedKeyPrincipalFixture {
	t.Helper()
	t.Setenv("CORDUM_BCRYPT_COST", "4")
	s, bus, safety := newAgentBindingGateway(t)
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_ENV":      "production",
		"CORDUM_API_KEYS": `[{"key":"` + managedKeyPrincipalUnboundKey + `","tenant":"default","role":"admin"}]`,
	})
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", s.jobStore.Client())
	}
	store := auth.NewRedisKeyStoreFromClient(client)
	provider.SetKeyStore(store)
	t.Cleanup(provider.DrainUsage)
	s.auth = provider
	s.capProfile = capprofile.Production
	secret, err := auth.GenerateRawKey()
	if err != nil {
		t.Fatalf("GenerateRawKey() error = %v, want nil", err)
	}
	key := &auth.ManagedKey{Name: "ci", Tenant: "default", Scopes: []string{"jobs:write"}, CreatedAt: time.Now().UTC()}
	if err := store.Create(context.Background(), key, secret); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	return managedKeyPrincipalFixture{s: s, bus: bus, safety: safety, provider: provider, key: key, secret: secret}
}

// submit authenticates the request with the real provider, as apiKeyMiddleware
// does, then runs the submit handler with that context.
func (f managedKeyPrincipalFixture) submit(t *testing.T, in managedKeyPrincipalSubmit) *httptest.ResponseRecorder {
	t.Helper()
	body := submitJobRequest{Prompt: "hello", Topic: "job.test", PrincipalId: in.bodyPrincipal, IdempotencyKey: in.idem}
	if in.label != "" {
		body.Labels = map[string]string{"agent_id": in.label}
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", in.apiKey)
	if in.header != "" {
		req.Header.Set("X-Principal-Id", in.header)
	}
	ac, err := f.provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP() error = %v, want nil", err)
	}
	f.provider.DrainUsage()
	rec := httptest.NewRecorder()
	f.s.handleSubmitJobHTTP(rec, withAuth(req, ac))
	return rec
}

// effects counts what a submit leaves behind: job state records and
// sys.job.submit publishes. The key store writes apikey:* records on every
// use, so this never counts the whole database.
func (f managedKeyPrincipalFixture) effects(t *testing.T) (states, publishes int) {
	t.Helper()
	keys, err := f.s.jobStore.Client().Keys(context.Background(), "job:state:*").Result()
	if err != nil {
		t.Fatalf("KEYS job:state:*: %v", err)
	}
	f.bus.mu.Lock()
	defer f.bus.mu.Unlock()
	for _, m := range f.bus.published {
		if m.subject == capsdk.SubjectSubmit {
			publishes++
		}
	}
	return len(keys), publishes
}

// assertRefusedWithoutSubmit checks a 403 that left no job state, no publish,
// no idempotency record and no policy check behind.
func (f managedKeyPrincipalFixture) assertRefusedWithoutSubmit(t *testing.T, rec *httptest.ResponseRecorder, want, idem string, states, publishes int) {
	t.Helper()
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("status = %d body=%s, want 403 containing %q", rec.Code, rec.Body.String(), want)
	}
	if gotStates, gotPublishes := f.effects(t); gotStates != states || gotPublishes != publishes {
		t.Fatalf("job states = %d and publishes = %d after the refusal, want %d and %d", gotStates, gotPublishes, states, publishes)
	}
	if id, _ := f.s.jobStore.GetJobByIdempotencyKeyScoped(context.Background(), "default", idem); id != "" {
		t.Fatalf("idempotency record %q = %q, want none", idem, id)
	}
	if f.safety.lastReq != nil {
		t.Fatal("refused submit reached the policy check")
	}
}

// assertJobIsKeyPrincipal checks that the stored job and its sys.job.submit
// publish carry the managed key's own principal as principal and actor.
func (f managedKeyPrincipalFixture) assertJobIsKeyPrincipal(t *testing.T, jobID string) {
	t.Helper()
	want := f.key.ID
	req, err := f.s.jobStore.GetJobRequest(context.Background(), jobID)
	if err != nil || req == nil {
		t.Fatalf("GetJobRequest(%s) = (%v, %v), want the stored job", jobID, req, err)
	}
	id := req.GetIdentity()
	if req.GetPrincipalId() != want || id.GetPrincipalId() != want || id.GetActorId() != want ||
		id.GetTenantId() != "default" || req.GetMeta().GetActorId() != want {
		t.Fatalf("stored job principal=%q identity=%+v meta.actor=%q, want principal and actor %q in tenant default",
			req.GetPrincipalId(), id, req.GetMeta().GetActorId(), want)
	}
	f.bus.mu.Lock()
	defer f.bus.mu.Unlock()
	for _, m := range f.bus.published {
		if m.subject == capsdk.SubjectSubmit && m.packet.GetJobRequest().GetJobId() == jobID {
			if got := m.packet.GetIdentity().GetPrincipalId(); got != want {
				t.Fatalf("published identity principal = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatalf("no %s publish for job %s", capsdk.SubjectSubmit, jobID)
}

// assertAcceptedAsKeyPrincipal checks a 200 whose job is the key's own.
func (f managedKeyPrincipalFixture) assertAcceptedAsKeyPrincipal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var resp map[string]string
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp["job_id"] == "" {
		t.Fatalf("status = %d body=%s, want 200 with a job_id", rec.Code, rec.Body.String())
	}
	f.assertJobIsKeyPrincipal(t, resp["job_id"])
}

func TestManagedKeyPrincipal_ProductionSubmitRecordsKeyPrincipal(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	f.assertAcceptedAsKeyPrincipal(t, f.submit(t, managedKeyPrincipalSubmit{apiKey: f.secret, idem: "idem-managed-own"}))
}

func TestManagedKeyPrincipal_HeaderPrincipalIgnoredInProduction(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	// The header names a linked worker. Production drops it, so the job is
	// still the key's own.
	f.assertAcceptedAsKeyPrincipal(t, f.submit(t, managedKeyPrincipalSubmit{apiKey: f.secret, header: "worker-b", idem: "idem-managed-header"}))
}

func TestManagedKeyPrincipal_GRPCSubmitRecordsKeyPrincipal(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", f.secret, "x-principal-id", "worker-b"))
	ac, err := f.provider.AuthenticateGRPC(ctx)
	if err != nil {
		t.Fatalf("AuthenticateGRPC() error = %v, want nil", err)
	}
	f.provider.DrainUsage()
	resp, err := f.s.SubmitJob(context.WithValue(ctx, auth.ContextKey{}, ac), &pb.SubmitJobRequest{
		Prompt: "hello", Topic: "job.test", OrgId: "default", IdempotencyKey: "idem-managed-grpc",
	})
	if err != nil || resp.GetJobId() == "" {
		t.Fatalf("SubmitJob() = (%v, %v), want a job id and nil error", resp, err)
	}
	f.assertJobIsKeyPrincipal(t, resp.GetJobId())
}

func TestManagedKeyPrincipal_BodyPrincipalRefusedWithoutSubmit(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	states, publishes := f.effects(t)
	rec := f.submit(t, managedKeyPrincipalSubmit{apiKey: f.secret, bodyPrincipal: "worker-b", idem: "idem-managed-body"})
	f.assertRefusedWithoutSubmit(t, rec, managedKeyPrincipalMismatch, "idem-managed-body", states, publishes)
}

func TestManagedKeyPrincipal_LabelForLinkedWorkerRefusedWithoutSubmit(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	states, publishes := f.effects(t)
	// The key's own principal is linked to no agent, and the header cannot
	// select worker-b's agent for it.
	rec := f.submit(t, managedKeyPrincipalSubmit{apiKey: f.secret, header: "worker-b", label: "agent-b", idem: "idem-managed-label"})
	f.assertRefusedWithoutSubmit(t, rec, "client-supplied agent_id", "idem-managed-label", states, publishes)
}

func TestManagedKeyPrincipal_UnboundStaticKeyStillRefused(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	states, publishes := f.effects(t)
	rec := f.submit(t, managedKeyPrincipalSubmit{apiKey: managedKeyPrincipalUnboundKey, header: "worker-b", idem: "idem-static-unbound"})
	f.assertRefusedWithoutSubmit(t, rec, managedKeyPrincipalMismatch, "idem-static-unbound", states, publishes)
}

func TestManagedKeyPrincipal_AuditKeepsKeyIdentity(t *testing.T) {
	f := newManagedKeyPrincipalFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req.Header.Set("X-API-Key", f.secret)
	ac, err := f.provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP() error = %v, want nil", err)
	}
	f.provider.DrainUsage()
	if got := ac.AuthenticatedPrincipal(); got != f.key.ID {
		t.Fatalf("AuthenticatedPrincipal() = %q, want the key id %q", got, f.key.ID)
	}
	id, source, label := policybundles.ActorIdentity(ac)
	if id != f.key.ID || source != "api_key:"+f.key.ID || label != "ci" {
		t.Fatalf("ActorIdentity() = (%q, %q, %q), want (%q, %q, %q)", id, source, label, f.key.ID, "api_key:"+f.key.ID, "ci")
	}
}
