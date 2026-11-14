package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const linkInjectionUnboundKey = "link-injection-unbound-static-key-000000"

const linkInjectionWorkerAKey = "link-injection-key-bound-to-worker-a-0000"

const linkInjectionWorkerBKey = "link-injection-key-bound-to-worker-b-0000"

const linkInjectionUserUnboundKey = "link-injection-user-role-unbound-key-0000"

const linkInjectionUserWorkerBKey = "link-injection-user-role-worker-b-key-000"

const linkInjectionAudienceDenied = "submit_delegation_impersonation_denied"

// linkInjectionFixture is one agent-binding gateway (agent-a and agent-b
// linked to worker-a and worker-b) authenticating through a BasicAuthProvider
// built from the pinned environment.
type linkInjectionFixture struct {
	s        *server
	bus      *stubBus
	safety   *stubSafetyClient
	provider *auth.BasicAuthProvider
}

// linkInjectionEachProfile runs fn in a "production" and a "development"
// subtest. Production sets CORDUM_ALLOW_HEADER_PRINCIPAL=true and the
// production capability profile; development allows header principals by
// default. The static keys are admin keys (unbound, bound to worker-a, bound
// to worker-b) and "user" role keys (unbound, bound to worker-b).
func linkInjectionEachProfile(t *testing.T, fn func(t *testing.T, f linkInjectionFixture)) {
	for _, profile := range []string{"production", "development"} {
		t.Run(profile, func(t *testing.T) {
			flag := ""
			if profile == "production" {
				flag = "true"
			}
			keys := `[{"key":"` + linkInjectionUnboundKey + `"},` +
				`{"key":"` + linkInjectionWorkerAKey + `","principal_id":"worker-a"},` +
				`{"key":"` + linkInjectionWorkerBKey + `","principal_id":"worker-b"},` +
				`{"key":"` + linkInjectionUserUnboundKey + `","role":"user"},` +
				`{"key":"` + linkInjectionUserWorkerBKey + `","principal_id":"worker-b","role":"user"}]`
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
			fn(t, linkInjectionFixture{s: s, bus: bus, safety: safety, provider: provider})
		})
	}
}

// submitHTTP authenticates a POST /api/v1/jobs with key and, when non-empty,
// X-Principal-Id as the gateway middleware does, then runs
// handleSubmitJobHTTP. body never carries labels.agent_id.
func (f linkInjectionFixture) submitHTTP(t *testing.T, key, header string, body submitJobRequest) *httptest.ResponseRecorder {
	t.Helper()
	body.Prompt, body.Topic = "hello", "job.test"
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	if header != "" {
		req.Header.Set("X-Principal-Id", header)
	}
	ac, err := f.provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("AuthenticateHTTP() error = %v, want nil", err)
	}
	f.safety.recordReq(nil)
	rec := httptest.NewRecorder()
	f.s.handleSubmitJobHTTP(rec, withAuth(req, ac))
	return rec
}

// assertPolicy checks an accepted submit reached the policy check attributed
// to principal and carrying agent as labels.agent_id ("" for none). detail is
// the HTTP body or the gRPC error.
func (f linkInjectionFixture) assertPolicy(t *testing.T, accepted bool, detail, principal, agent string) {
	t.Helper()
	if !accepted || f.safety.lastReq == nil {
		t.Fatalf("accepted = %v, policy captured = %v; want both; detail=%s", accepted, f.safety.lastReq != nil, detail)
	}
	if got, ok := f.safety.lastReq.GetLabels()["agent_id"]; got != agent || (agent == "" && ok) {
		t.Errorf("policy agent_id = %q (present %v), want %q", got, ok, agent)
	}
	if got := f.safety.lastReq.GetPrincipalId(); got != principal {
		t.Errorf("policy principal = %q, want %q (attribution unchanged)", got, principal)
	}
}

// submitGRPC authenticates x-api-key and, when non-empty, x-principal-id
// metadata as the gateway interceptor does, then runs SubmitJob with
// principal as the request's PrincipalId and no labels.agent_id.
func (f linkInjectionFixture) submitGRPC(t *testing.T, key, header, principal string) error {
	t.Helper()
	pairs := []string{"x-api-key", key}
	if header != "" {
		pairs = append(pairs, "x-principal-id", header)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
	ac, err := f.provider.AuthenticateGRPC(ctx)
	if err != nil {
		t.Fatalf("AuthenticateGRPC() error = %v, want nil", err)
	}
	f.safety.recordReq(nil)
	req := &pb.SubmitJobRequest{Prompt: "hello", Topic: "job.test", OrgId: "default", PrincipalId: principal}
	_, err = f.s.SubmitJob(context.WithValue(ctx, auth.ContextKey{}, ac), req)
	return err
}

// audienceDenials counts the delegation-audience gate's audit entries.
func (f linkInjectionFixture) audienceDenials(t *testing.T) int {
	t.Helper()
	entries, err := f.s.loadPolicyAudit(context.Background())
	if err != nil {
		t.Fatalf("loadPolicyAudit() error = %v", err)
	}
	n := 0
	for _, entry := range entries {
		if entry.Action == linkInjectionAudienceDenied {
			n++
		}
	}
	return n
}

func TestSubmitLinkInjection_HTTPFollowsAuthenticatedPrincipal(t *testing.T) {
	linkInjectionEachProfile(t, func(t *testing.T, f linkInjectionFixture) {
		for _, tc := range []struct {
			name, key, header, body, principal, agent string
			// prodRefused: production refuses a body principal_id that differs
			// from the credential's before any policy check.
			prodRefused bool
		}{
			// A worker credential still gets its own agent (DoD 3).
			{"bound_worker_credential", linkInjectionWorkerBKey, "", "", "worker-b", "agent-b", false},
			{"bound_worker_credential_padded_body", linkInjectionWorkerBKey, "", " worker-b ", "worker-b", "agent-b", false},
			// The caller's own link is not put on a job attributed to another principal.
			{"bound_key_names_other_principal", linkInjectionWorkerAKey, "", "worker-b", "worker-b", "", true},
			{"bound_key_case_mismatched_body", linkInjectionWorkerBKey, "", "WORKER-B", "WORKER-B", "", true},
			{"unbound_key_header_principal", linkInjectionUnboundKey, "worker-b", "", "worker-b", "", false},
			{"unbound_key_header_equals_body", linkInjectionUnboundKey, "worker-b", "worker-b", "worker-b", "", false},
			{"unbound_key_body_principal", linkInjectionUnboundKey, "", "worker-b", "worker-b", "", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				published := len(f.bus.published)
				rec := f.submitHTTP(t, tc.key, tc.header, submitJobRequest{PrincipalId: tc.body})
				if tc.prodRefused && f.s.capProfile.IsProduction() {
					if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "authenticated job identity mismatch") {
						t.Fatalf("status = %d, want 403 identity mismatch; body=%s", rec.Code, rec.Body.String())
					}
					if f.safety.lastReq != nil || len(f.bus.published) != published {
						t.Errorf("refused submit reached policy = %v, publishes %d -> %d", f.safety.lastReq != nil, published, len(f.bus.published))
					}
					return
				}
				f.assertPolicy(t, rec.Code == http.StatusOK, rec.Body.String(), tc.principal, tc.agent)
			})
		}
	})
}

func TestSubmitLinkInjection_HTTPDelegationAudienceGateSeesNoRequestPrincipal(t *testing.T) {
	linkInjectionEachProfile(t, func(t *testing.T, f linkInjectionFixture) {
		setTestEntitlements(t, f.s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
			entitlements.RBAC = true
			entitlements.AgentIdentity = true
		})
		// "user" passes the submit role gate (admin, user) but, holding only
		// jobs.write, lacks delegation.impersonate for the audience gate.
		putTestRole(t, f.s, "user", auth.PermJobsWrite)
		for _, tc := range []struct {
			name, key, header, body string
			allowed                 bool
		}{
			{"unbound_key_body_principal", linkInjectionUserUnboundKey, "", "worker-b", false},
			{"unbound_key_header_principal", linkInjectionUserUnboundKey, "worker-b", "", false},
			{"bound_worker_credential", linkInjectionUserWorkerBKey, "", "", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				published, denials := len(f.bus.published), f.audienceDenials(t)
				rec := f.submitHTTP(t, tc.key, tc.header, submitJobRequest{PrincipalId: tc.body, DelegationAudienceAgentID: "agent-b"})
				if tc.allowed {
					f.assertPolicy(t, rec.Code == http.StatusOK, rec.Body.String(), "worker-b", "agent-b")
					if got := f.audienceDenials(t); got != denials {
						t.Errorf("audience gate denials = %d, want %d", got, denials)
					}
					return
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 from the audience gate; body=%s", rec.Code, rec.Body.String())
				}
				if got := f.audienceDenials(t); got != denials+1 {
					t.Errorf("audience gate denials = %d, want %d", got, denials+1)
				}
				if f.safety.lastReq != nil || len(f.bus.published) != published {
					t.Errorf("refused submit reached policy = %v, publishes %d -> %d", f.safety.lastReq != nil, published, len(f.bus.published))
				}
			})
		}
	})
}

func TestSubmitLinkInjection_GRPCFollowsAuthenticatedPrincipal(t *testing.T) {
	linkInjectionEachProfile(t, func(t *testing.T, f linkInjectionFixture) {
		for _, tc := range []struct {
			name, key, header, request, principal, agent string
			// prodRefused: production refuses a request PrincipalId that
			// differs from the credential's before any policy check.
			prodRefused bool
		}{
			// A worker credential still gets its own agent (DoD 3).
			{"bound_worker_credential", linkInjectionWorkerBKey, "", "", "worker-b", "agent-b", false},
			// The credential's principal overrides the request field.
			{"bound_key_overrides_request_principal", linkInjectionWorkerAKey, "", "worker-b", "worker-a", "agent-a", false},
			{"unbound_key_metadata_principal", linkInjectionUnboundKey, "worker-b", "", "worker-b", "", false},
			{"unbound_key_request_principal", linkInjectionUnboundKey, "", "worker-b", "worker-b", "", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				published := len(f.bus.published)
				err := f.submitGRPC(t, tc.key, tc.header, tc.request)
				if tc.prodRefused && f.s.capProfile.IsProduction() {
					if status.Code(err) != codes.PermissionDenied || !strings.Contains(status.Convert(err).Message(), "authenticated job identity mismatch") {
						t.Fatalf("SubmitJob() error = %v, want PermissionDenied identity mismatch", err)
					}
					if f.safety.lastReq != nil || len(f.bus.published) != published {
						t.Errorf("refused submit reached policy = %v, publishes %d -> %d", f.safety.lastReq != nil, published, len(f.bus.published))
					}
					return
				}
				f.assertPolicy(t, err == nil, fmt.Sprint(err), tc.principal, tc.agent)
			})
		}
	})
}
