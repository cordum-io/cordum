package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/capprofile"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const headerPrincipalUnboundKey = "submit-unbound-static-key-000000000000000"

const headerPrincipalBoundKey = "submit-static-key-bound-to-worker-b-00000"

const headerPrincipalRefusal = "client-supplied agent_id requires an authenticated principal"

// headerPrincipalFixture is one agent-binding gateway (agent-a and agent-b
// linked to worker-a and worker-b) authenticating through a BasicAuthProvider
// built from the pinned environment.
type headerPrincipalFixture struct {
	s        *server
	bus      *stubBus
	safety   *stubSafetyClient
	provider *auth.BasicAuthProvider
}

// headerPrincipalEachProfile runs fn in a "production" and a "development"
// subtest. Production sets CORDUM_ALLOW_HEADER_PRINCIPAL=true and the
// production capability profile; development leaves the flag unset, since it
// allows header principals by default. The static keys are one unbound key
// and one key bound to worker-b.
func headerPrincipalEachProfile(t *testing.T, fn func(t *testing.T, f headerPrincipalFixture)) {
	for _, profile := range []string{"production", "development"} {
		t.Run(profile, func(t *testing.T) {
			flag := ""
			if profile == "production" {
				flag = "true"
			}
			keys := `[{"key":"` + headerPrincipalUnboundKey + `"},` +
				`{"key":"` + headerPrincipalBoundKey + `","principal_id":"worker-b"}]`
			for name, value := range map[string]string{
				"CORDUM_ENV": profile, "CORDUM_PRODUCTION": "", "CORDUM_ALLOW_HEADER_PRINCIPAL": flag,
				"CORDUM_API_KEYS": keys, "CORDUM_API_KEY": "", "CORDUM_API_KEYS_PATH": "",
				"CORDUM_ALLOW_INSECURE_NO_AUTH": "", "CORDUM_JWT_HMAC_SECRET": "", "CORDUM_JWT_PUBLIC_KEY": "",
				"CORDUM_JWT_PUBLIC_KEY_PATH": "", "CORDUM_JWT_REQUIRED": "",
			} {
				t.Setenv(name, value)
			}
			provider, err := auth.NewBasicAuthProvider("default")
			if err != nil {
				t.Fatalf("NewBasicAuthProvider() error = %v, want nil", err)
			}
			s, bus, safety := newAgentBindingGateway(t)
			s.auth = provider
			if profile == "production" {
				s.capProfile = capprofile.Production
			}
			fn(t, headerPrincipalFixture{s: s, bus: bus, safety: safety, provider: provider})
		})
	}
}

// submitHTTP authenticates a POST /api/v1/jobs carrying key and X-Principal-Id
// as the gateway middleware does, then runs handleSubmitJobHTTP. An empty
// label sends no labels.agent_id.
func (f headerPrincipalFixture) submitHTTP(t *testing.T, key, principal, label, idem string) *httptest.ResponseRecorder {
	t.Helper()
	body := submitJobRequest{Prompt: "hello", Topic: "job.test", IdempotencyKey: idem}
	if label != "" {
		body.Labels = map[string]string{"agent_id": label}
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Principal-Id", principal)
	ac, err := f.provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP() error = %v, want nil", err)
	}
	rec := httptest.NewRecorder()
	f.s.handleSubmitJobHTTP(rec, withAuth(req, ac))
	return rec
}

// submitGRPC is submitHTTP over x-api-key and x-principal-id metadata and
// SubmitJob.
func (f headerPrincipalFixture) submitGRPC(t *testing.T, key, principal, label, idem string) error {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", key, "x-principal-id", principal))
	ac, err := f.provider.AuthenticateGRPC(ctx)
	if err != nil {
		t.Fatalf("AuthenticateGRPC() error = %v, want nil", err)
	}
	req := &pb.SubmitJobRequest{Prompt: "hello", Topic: "job.test", OrgId: "default", IdempotencyKey: idem}
	if label != "" {
		req.Labels = map[string]string{"agent_id": label}
	}
	_, err = f.s.SubmitJob(context.WithValue(ctx, auth.ContextKey{}, ac), req)
	return err
}

// redisKeys counts the keys in the gateway's Redis database, which the job,
// memory and agent stores share.
func (f headerPrincipalFixture) redisKeys(t *testing.T) int64 {
	t.Helper()
	n, err := f.s.jobStore.Client().DBSize(context.Background()).Result()
	if err != nil {
		t.Fatalf("DBSIZE: %v", err)
	}
	return n
}

// assertNoTrace checks a refused submit stored nothing: no idempotency record
// and no new Redis key.
func (f headerPrincipalFixture) assertNoTrace(t *testing.T, idem string, keysBefore int64) {
	t.Helper()
	if id, _ := f.s.jobStore.GetJobByIdempotencyKeyScoped(context.Background(), "default", idem); id != "" {
		t.Errorf("idempotency record %q = %q, want none", idem, id)
	}
	if got := f.redisKeys(t); got != keysBefore {
		t.Errorf("Redis keys = %d after the refused submit, want %d", got, keysBefore)
	}
}

// assertBound checks an accepted submit carried agent-b to the policy check,
// was published, and stored its idempotency record, so the refused cases'
// empty-record checks look where a record would be.
func (f headerPrincipalFixture) assertBound(t *testing.T, publishedBefore int, idem string) {
	t.Helper()
	if f.safety.lastReq == nil {
		t.Fatal("accepted submit reached no policy check")
	}
	if got := f.safety.lastReq.GetLabels()["agent_id"]; got != "agent-b" {
		t.Errorf("policy agent_id = %q, want agent-b", got)
	}
	if got := len(f.bus.published); got <= publishedBefore {
		t.Errorf("published = %d, want more than %d", got, publishedBefore)
	}
	if id, err := f.s.jobStore.GetJobByIdempotencyKeyScoped(context.Background(), "default", idem); err != nil || id == "" {
		t.Errorf("idempotency record %q = %q (err %v), want a job id", idem, id, err)
	}
}

func TestSubmitAssertedPrincipal_HTTPLabelRefusedWithoutSubmit(t *testing.T) {
	headerPrincipalEachProfile(t, func(t *testing.T, f headerPrincipalFixture) {
		before := f.redisKeys(t)
		rec := f.submitHTTP(t, headerPrincipalUnboundKey, "worker-b", "agent-b", "idem-http-asserted")
		assertAgentBindingRefused(t, rec, f.bus, f.safety, headerPrincipalRefusal)
		f.assertNoTrace(t, "idem-http-asserted", before)
	})
}

func TestSubmitAssertedPrincipal_GRPCLabelRefusedWithoutSubmit(t *testing.T) {
	headerPrincipalEachProfile(t, func(t *testing.T, f headerPrincipalFixture) {
		before := f.redisKeys(t)
		err := f.submitGRPC(t, headerPrincipalUnboundKey, "worker-b", "agent-b", "idem-grpc-asserted")
		if status.Code(err) != codes.PermissionDenied || !strings.Contains(status.Convert(err).Message(), headerPrincipalRefusal) {
			t.Fatalf("SubmitJob() error = %v, want PermissionDenied %q", err, headerPrincipalRefusal)
		}
		if f.safety.lastReq != nil || len(f.bus.published) != 0 {
			t.Fatalf("refused submit reached policy=%v publishes=%d", f.safety.lastReq != nil, len(f.bus.published))
		}
		f.assertNoTrace(t, "idem-grpc-asserted", before)
		// The same caller without the label is still served, attributed but
		// with no agent inferred from the asserted principal.
		if err := f.submitGRPC(t, headerPrincipalUnboundKey, "worker-b", "", "idem-grpc-unlabeled"); err != nil {
			t.Fatalf("SubmitJob() without a label error = %v, want nil", err)
		}
		if f.safety.lastReq == nil {
			t.Fatal("submit without a label reached no policy check")
		}
		if got := f.safety.lastReq.GetLabels()["agent_id"]; got != "" {
			t.Errorf("policy agent_id = %q, want none for an asserted principal", got)
		}
	})
}

func TestSubmitAssertedPrincipal_BoundCredentialStillBinds(t *testing.T) {
	headerPrincipalEachProfile(t, func(t *testing.T, f headerPrincipalFixture) {
		// The key binds worker-b, so the header's worker-a is ignored.
		rec := f.submitHTTP(t, headerPrincipalBoundKey, "worker-a", "agent-b", "idem-http-bound")
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		f.assertBound(t, 0, "idem-http-bound")
		published := len(f.bus.published)
		f.safety.recordReq(nil)
		if err := f.submitGRPC(t, headerPrincipalBoundKey, "worker-a", "agent-b", "idem-grpc-bound"); err != nil {
			t.Fatalf("SubmitJob() error = %v, want nil", err)
		}
		f.assertBound(t, published, "idem-grpc-bound")
	})
}
