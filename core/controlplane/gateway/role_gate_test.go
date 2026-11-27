package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	roleGateSecret   = "roleGate-hmac-secret-0123456789abcdef"
	roleGatePassword = "RoleGatePass1!xy"
	roleGateChat     = "/api/v1/workflow-runs/roleGate-chat/chat"
	roleGateLock     = `{"resource":"roleGate-%s","owner":"roleGate-owner","mode":"exclusive","ttl_ms":60000}`
)

var (
	roleGateRoles = []string{"admin", "operator", "secops", "user"}
	roleGateKinds = []string{"key", "session", "jwt"}

	// roleGateStates are forced license states. The first keeps the test gateway's own
	// Community license with SCIM entitled. Gated states may refuse at break-glass (503).
	roleGateStates = []struct {
		name, status string
		plan         licensing.Plan
		gated        bool
	}{
		{name: "default"},
		{"valid", "active", licensing.PlanEnterprise, false},
		{"grace", "grace", licensing.PlanEnterprise, false},
		{"degraded", "degraded", licensing.PlanCommunity, true},
		{"invalid", "fallback", licensing.PlanCommunity, true},
		{"missing", "active", licensing.PlanCommunity, false},
	}

	// roleGateRoutes are the admin-only routes with the status admin gets past the gate.
	// License reload comes last because it drops the forced license.
	roleGateRoutes = []struct {
		method, path, body string
		admin              int
	}{
		{http.MethodGet, "/api/v1/admin/locks", "", http.StatusOK},
		{http.MethodPost, "/api/v1/evals/datasets/roleGate-missing/run", "{}", http.StatusNotFound},
		{http.MethodDelete, "/api/v1/evals/runs/roleGate-run?force=true", "", http.StatusNoContent},
		{http.MethodGet, "/api/v1/governance/health", "", http.StatusOK},
		{http.MethodGet, "/api/v1/stream", "", http.StatusBadRequest},
		{http.MethodPost, "/api/v1/locks/acquire", fmt.Sprintf(roleGateLock, "free"), http.StatusOK},
		{http.MethodPost, "/api/v1/locks/renew", fmt.Sprintf(roleGateLock, "held"), http.StatusOK},
		{http.MethodPost, "/api/v1/locks/release", fmt.Sprintf(roleGateLock, "held"), http.StatusOK},
		{http.MethodPost, "/api/v1/policy/bundles/roleGate-bundle/resign", "", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/v1/scim/settings", "", http.StatusOK},
		{http.MethodPost, "/api/v1/scim/settings/token", "", http.StatusOK},
		{http.MethodPost, "/api/v1/license/reload", "", http.StatusOK},
	}
)

type roleGateCred struct{ header, value string }

// roleGatePrincipals holds one credential per "<role>/<kind>" and the user store behind
// the sessions.
type roleGatePrincipals struct {
	users *auth.RedisUserStore
	mr    *miniredis.Miniredis
	creds map[string]roleGateCred
}

type roleGateFixture struct {
	*roleGatePrincipals
	s   *server
	bus *stubBus
	h   http.Handler
}

// TestAdminRoleGatesAdmitOnlyAdmin pins every admin-only route and branch to the admin role
// for static keys, sessions and JWTs, and checks that a refusal stores nothing.
func TestAdminRoleGatesAdmitOnlyAdmin(t *testing.T) {
	t.Cleanup(evalRunRateLimiter.reset)
	p := roleGateSignIn(t)
	for _, st := range roleGateStates {
		t.Run("refused/"+st.name, func(t *testing.T) {
			f := roleGateSetup(t, p)
			if st.plan != "" {
				f.s.entitlements.ForceStateWithStatus(st.plan, licensing.DefaultEntitlements(st.plan), nil, st.status)
			}
			roleGateEach(t, []string{"operator", "secops"}, func(t *testing.T, cred string) {
				for _, rt := range roleGateRoutes {
					want := []int{http.StatusForbidden}
					// Operator passes the evals routes' admin list, which a permission check
					// follows; in gated states break-glass answers before that check.
					if st.gated && strings.HasPrefix(cred, "operator/") && strings.Contains(rt.path, "/evals/") {
						want = append(want, http.StatusServiceUnavailable)
					}
					f.refused(t, rt.method, rt.path, rt.body, cred, want...)
				}
			})
		})
	}
	for _, kind := range roleGateKinds {
		t.Run("admin/"+kind, func(t *testing.T) {
			f := roleGateSetup(t, p)
			for _, rt := range roleGateRoutes {
				if rec := f.do(rt.method, rt.path, rt.body, "admin/"+kind); rec.Code != rt.admin {
					t.Errorf("admin %s %s = %d %s, want %d", rt.method, rt.path, rec.Code, rec.Body.String(), rt.admin)
				}
			}
		})
	}
	t.Run("branches", func(t *testing.T) { roleGateBranches(t, roleGateSetup(t, p)) })
	t.Run("permission_checks", func(t *testing.T) { roleGatePermissionChecks(t, roleGateSetup(t, p)) })
	t.Run("grpc_submit", func(t *testing.T) { roleGateSubmit(t, roleGateSetup(t, p)) })
}

// roleGateEach runs fn once per credential kind of each role.
func roleGateEach(t *testing.T, roles []string, fn func(t *testing.T, cred string)) {
	for _, role := range roles {
		for _, kind := range roleGateKinds {
			cred := role + "/" + kind
			t.Run(cred, func(t *testing.T) { fn(t, cred) })
		}
	}
}

// roleGateBranches: only admin gets the admin-only status fields and posts chat as agent.
func roleGateBranches(t *testing.T, f *roleGateFixture) {
	roleGateEach(t, []string{"admin", "operator", "secops"}, func(t *testing.T, cred string) {
		admin := strings.HasPrefix(cred, "admin/")
		body := roleGateBody(t, f.do(http.MethodGet, "/api/v1/status", "", cred))
		_, instance := body["instance_id"]
		_, limiter := body["rate_limiter"]
		if instance != admin || limiter != admin {
			t.Errorf("status instance_id=%t rate_limiter=%t, want %t", instance, limiter, admin)
		}
		post := `{"role":"agent","content":"roleGate"}`
		if strings.HasPrefix(cred, "secops/") {
			f.refused(t, http.MethodPost, roleGateChat, post, cred, http.StatusForbidden)
			return
		}
		want := map[bool]string{true: "agent", false: "user"}[admin]
		if got := roleGateBody(t, f.do(http.MethodPost, roleGateChat, post, cred))["role"]; got != want {
			t.Errorf("chat posted as agent stored role %v, want %s", got, want)
		}
	})
}

// roleGatePermissionChecks: a legacy admin list admits operator only to the permissions
// operator holds, and only while the permission checker is set.
func roleGatePermissionChecks(t *testing.T, f *roleGateFixture) {
	approve := func(t *testing.T) string { return "/api/v1/approvals/" + seedApprovalJob(t, f.s, "") + "/approve" }
	roleGateEach(t, []string{"operator"}, func(t *testing.T, cred string) {
		target := approve(t)
		if rec := f.do(http.MethodPost, target, `{"reason":"roleGate"}`, cred); rec.Code != http.StatusOK {
			t.Errorf("operator approve = %d %s, want 200", rec.Code, rec.Body.String())
		}
		jobID := strings.TrimSuffix(strings.TrimPrefix(target, "/api/v1/approvals/"), "/approve")
		if state, err := f.s.jobStore.GetState(context.Background(), jobID); err != nil || state == model.JobStateApproval {
			t.Errorf("approved job state = %s (err %v), want it out of %s", state, err, model.JobStateApproval)
		}
		f.refused(t, http.MethodPut, "/api/v1/config", `{"roleGate":"on"}`, cred, http.StatusForbidden)
	})
	roleGateEach(t, []string{"secops"}, func(t *testing.T, cred string) {
		f.refused(t, http.MethodPost, approve(t), `{"reason":"roleGate"}`, cred, http.StatusForbidden)
	})
	t.Run("without_checker", func(t *testing.T) {
		f.s.permChecker = nil
		roleGateEach(t, []string{"operator"}, func(t *testing.T, cred string) {
			f.refused(t, http.MethodPost, approve(t), `{"reason":"roleGate"}`, cred, http.StatusForbidden)
		})
	})
}

// roleGateSubmit sends SubmitJob through the gRPC auth interceptor: admin, user and
// operator keys and JWTs submit; secops is refused and publishes nothing. Every
// session token is refused as Unauthenticated before the role gate, since the gRPC
// API takes no user session, and publishes nothing.
func roleGateSubmit(t *testing.T, f *roleGateFixture) {
	intercept := apiKeyUnaryInterceptor(f.s.auth)
	submit := func(ctx context.Context, req any) (any, error) {
		return f.s.SubmitJob(ctx, req.(*pb.SubmitJobRequest))
	}
	roleGateEach(t, []string{"admin", "user", "operator", "secops"}, func(t *testing.T, cred string) {
		c := f.creds[cred]
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(c.header, c.value))
		published := f.published()
		_, err := intercept(ctx, &pb.SubmitJobRequest{Prompt: "roleGate", Topic: "job.default", OrgId: "default"}, &grpc.UnaryServerInfo{}, submit)
		session := strings.HasSuffix(cred, "/session")
		if !session && !strings.HasPrefix(cred, "secops/") {
			if err != nil {
				t.Errorf("SubmitJob = %v, want success", err)
			}
			return
		}
		want := map[bool]codes.Code{true: codes.Unauthenticated, false: codes.PermissionDenied}[session]
		if status.Code(err) != want || f.published() != published {
			t.Errorf("SubmitJob = %v with %d published, want %s with none", err, f.published()-published, want)
		}
	})
}

// roleGateSignIn stores a user per role and signs it in, and signs a JWT per role.
func roleGateSignIn(t *testing.T) *roleGatePrincipals {
	t.Helper()
	users, mr := newTestUserStore(t)
	basic := newBasicAuthForTest(t, roleGateEnv())
	basic.SetUserStore(users)
	login := &server{auth: basic, tenant: "default"}
	p := &roleGatePrincipals{users: users, mr: mr, creds: map[string]roleGateCred{}}
	for _, role := range roleGateRoles {
		name := "roleGate-" + role
		if err := users.Create(context.Background(), &auth.User{Username: name, Tenant: "default", Role: role}, roleGatePassword); err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"`+name+`","password":"`+roleGatePassword+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		login.handleLogin(rec, req)
		var resp AuthLoginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !strings.HasPrefix(resp.Token, "session-") {
			t.Fatalf("login %s = %d %s", name, rec.Code, rec.Body.String())
		}
		p.creds[role+"/key"] = roleGateCred{"X-API-Key", name + "-key"}
		p.creds[role+"/session"] = roleGateCred{"X-API-Key", resp.Token}
		p.creds[role+"/jwt"] = roleGateCred{"Authorization", "Bearer " + roleGateJWT(t, role)}
	}
	return p
}

func roleGateEnv() map[string]string {
	keys := make([]string, 0, len(roleGateRoles))
	for _, role := range roleGateRoles {
		keys = append(keys, fmt.Sprintf(`{"key":"roleGate-%[1]s-key","role":"%[1]s","tenant":"default","principal_id":"key-%[1]s"}`, role))
	}
	return map[string]string{
		"CORDUM_API_KEYS":        "[" + strings.Join(keys, ",") + "]",
		"CORDUM_JWT_HMAC_SECRET": roleGateSecret,
		"CORDUM_JWT_ISSUER":      "roleGate-issuer",
	}
}

// roleGateJWT signs an HS256 token whose role claim is role.
func roleGateJWT(t *testing.T, role string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"iss": "roleGate-issuer", "sub": "jwt-" + role, "role": role, "tenant": "default",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	enc := base64.RawURLEncoding
	input := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(roleGateSecret))
	_, _ = mac.Write([]byte(input))
	return input + "." + enc.EncodeToString(mac.Sum(nil))
}

// roleGateSetup builds a gateway on the full HTTP handler, with the principals' keys,
// sessions, JWTs and SCIM, a held lock, an eval run, a chat run and a fresh eval-run limit.
func roleGateSetup(t *testing.T, p *roleGatePrincipals) *roleGateFixture {
	t.Helper()
	evalRunRateLimiter.reset()
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	basic := newBasicAuthForTest(t, roleGateEnv())
	basic.SetUserStore(p.users)
	scim, err := auth.NewSCIMService(p.users, "default", s.entitlements)
	if err != nil {
		t.Fatalf("scim service: %v", err)
	}
	if s.auth, err = auth.NewCompositeAuthProvider(basic, scim); err != nil {
		t.Fatalf("composite auth: %v", err)
	}
	setTestEntitlements(t, s, licensing.PlanCommunity, func(e *licensing.Entitlements) {
		e.MaxPolicyBundles = licensing.Unlimited
		e.SCIM = true
	})
	ctx := context.Background()
	if _, ok, err := s.lockStore.Acquire(ctx, tenantLockResource("default", "roleGate-held"), "roleGate-owner", locks.ModeExclusive, time.Minute); err != nil || !ok {
		t.Fatalf("seed lock = %t %v", ok, err)
	}
	if err := s.evalRunStore.CreateRun(ctx, runner.RunResult{RunID: "roleGate-run", DatasetID: "roleGate-dataset", Tenant: "default"}); err != nil {
		t.Fatalf("seed eval run: %v", err)
	}
	seedChatRun(t, s, "roleGate-chat", "roleGate-workflow", "default")
	h, err := newHTTPHandler(s)
	if err != nil {
		t.Fatalf("http handler: %v", err)
	}
	return &roleGateFixture{roleGatePrincipals: p, s: s, bus: bus, h: h}
}

func (f *roleGateFixture) do(method, target, body, cred string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set(f.creds[cred].header, f.creds[cred].value)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

// refused requires a refusal with one of the wanted statuses that stores nothing.
func (f *roleGateFixture) refused(t *testing.T, method, target, body, cred string, want ...int) {
	t.Helper()
	before := f.snapshot(t)
	if rec := f.do(method, target, body, cred); !slices.Contains(want, rec.Code) {
		t.Errorf("%s %s = %d %s, want %v", method, target, rec.Code, rec.Body.String(), want)
	}
	if f.snapshot(t) != before {
		t.Errorf("%s %s changed stored state on refusal", method, target)
	}
}

// snapshot renders every gateway Redis key with its value and TTL, the user store, and
// the license state.
func (f *roleGateFixture) snapshot(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c := f.s.redisClient()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		var v any
		switch c.Type(ctx, k).Val() {
		case "string":
			v = c.Get(ctx, k).Val()
		case "hash":
			v = c.HGetAll(ctx, k).Val()
		case "list":
			v = c.LRange(ctx, k, 0, -1).Val()
		case "set":
			members := c.SMembers(ctx, k).Val()
			sort.Strings(members)
			v = members
		case "zset":
			v = c.ZRangeWithScores(ctx, k, 0, -1).Val()
		default:
			v = c.XRange(ctx, k, "-", "+").Val()
		}
		fmt.Fprintf(&b, "%s %v %v\n", k, v, c.PTTL(ctx, k).Val())
	}
	license, err := json.Marshal([]any{f.s.currentEntitlements(), f.s.currentLicenseInfo()})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	return b.String() + f.mr.Dump() + string(license)
}

func (f *roleGateFixture) published() int {
	f.bus.mu.Lock()
	defer f.bus.mu.Unlock()
	return len(f.bus.published)
}

// roleGateBody decodes a 200 JSON response.
func roleGateBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("response = %d %s, want 200 JSON", rec.Code, rec.Body.String())
	}
	return body
}
