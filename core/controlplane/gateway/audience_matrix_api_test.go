package gateway

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/mcp"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const matrixMCPAgent = "agent-matrix-mcp"

// TestAudienceMatrixSCIMAPI presents every credential to POST
// /api/v1/scim/v2/Users. The protected effect is a stored user record
// (user:id:*).
func TestAudienceMatrixSCIMAPI(t *testing.T) {
	m := newAudienceMatrix(t)
	setTestEntitlements(t, m.s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.SCIM = true })
	users := auth.NewRedisUserStoreFromClient(m.s.jobStore.Client().(*redis.Client))
	scim, err := auth.NewSCIMService(users, matrixTenant, m.s.entitlements)
	if err != nil {
		t.Fatalf("scim service: %v", err)
	}
	m.keys, m.jwt = m.matrixWithProvider(t, m.keys, scim), m.matrixWithProvider(t, m.jwt, scim)
	var n atomic.Int64
	identity.Run(t, identity.SCIMAPI, func(t *testing.T, c identity.Credential) identity.Result {
		newReq := func() *http.Request {
			body := fmt.Sprintf(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"matrix-user-%d"}`, n.Add(1))
			return matrixRequest(auth.SCIMUsersPath, body)
		}
		return m.presentHTTP(t, identity.SCIMAPI, c, newReq, m.matrixCount("user:id:*"), "user:id records")
	})
}

// TestAudienceMatrixMCPGateway presents every credential to a session-less
// POST /mcp/message calling cordum_submit_job. The protected effect is a
// stored job plus its sys.job.submit publish.
func TestAudienceMatrixMCPGateway(t *testing.T) {
	m := newAudienceMatrix(t)
	m.matrixStartMCP(t)
	const call = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + mcp.ToolSubmitJob +
		`","arguments":{"prompt":"audience matrix","topic":"job.test"}}}`
	identity.Run(t, identity.MCPGateway, func(t *testing.T, c identity.Credential) identity.Result {
		newReq := func() *http.Request { return matrixRequest("/mcp/message", call) }
		return m.presentHTTP(t, identity.MCPGateway, c, newReq, m.jobEffects, "job:state keys + sys.job.submit publishes")
	})
}

// TestAudienceMatrixGRPCAPI presents every credential to CordumApi SubmitJob
// through the production interceptor. The protected effect is a stored job
// plus its sys.job.submit publish.
func TestAudienceMatrixGRPCAPI(t *testing.T) {
	m := newAudienceMatrix(t)
	identity.Run(t, identity.GRPCAPI, m.matrixPresentGRPC)
}

// matrixWithProvider adds provider to deployment d's composite, as RunWithAuth
// appends the SCIM service after Basic and the OIDC adapter, and serves the
// routes it registers.
func (m *audienceMatrix) matrixWithProvider(t *testing.T, d matrixDeployment, provider auth.AuthProvider) matrixDeployment {
	t.Helper()
	composite, err := auth.NewCompositeAuthProvider(d.auth, provider)
	if err != nil {
		t.Fatalf("composite: %v", err)
	}
	m.s.auth = composite
	handler, err := newHTTPHandler(m.s)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	return matrixDeployment{name: d.name, auth: composite, handler: handler}
}

// matrixCount counts the job store's Redis keys matching pattern.
func (m *audienceMatrix) matrixCount(pattern string) func(t *testing.T) int {
	return func(t *testing.T) int {
		t.Helper()
		keys, err := m.s.jobStore.Client().Keys(context.Background(), pattern).Result()
		if err != nil {
			t.Fatalf("%s keys: %v", pattern, err)
		}
		return len(keys)
	}
}

// matrixStartMCP enables the HTTP MCP runtime and binds one agent identity,
// allowed cordum_submit_job, to the principal of every credential meant for
// mcp_gateway, so that an MCP client holding one acts as that agent.
func (m *audienceMatrix) matrixStartMCP(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	m.s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(m.s.shutdownCh) })
	if err := m.s.configSvc.Set(ctx, &configsvc.Document{
		Scope: configsvc.ScopeSystem, ScopeID: "default",
		Data: map[string]any{"mcp": map[string]any{"enabled": true, "transport": "http"}},
	}); err != nil {
		t.Fatalf("set mcp config: %v", err)
	}
	if err := m.s.startMCPRuntimeFromConfig(m.s.loadMCPConfig(ctx)); err != nil {
		t.Fatalf("start mcp runtime: %v", err)
	}
	if _, err := m.s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID: matrixMCPAgent, TenantID: matrixTenant, Name: matrixMCPAgent, Owner: "matrix@example.com",
		RiskTier: "high", AllowedTools: []string{mcp.ToolSubmitJob}, Status: "active",
	}); err != nil {
		t.Fatalf("agent identity: %v", err)
	}
	for _, c := range identity.Credentials {
		if !identity.Allowed(c, identity.MCPGateway) {
			continue
		}
		d, carrier := m.matrixNatural(c)
		req := matrixRequest("/api/v1/jobs", "")
		carrier.set(req, m.tokens[c])
		authCtx, err := d.auth.AuthenticateHTTP(req)
		if err != nil || authCtx.AuthenticatedPrincipal() == "" {
			t.Fatalf("principal of %s: %v", c, err)
		}
		if err := m.s.agentIdentityStore.LinkWorker(ctx, matrixMCPAgent, authCtx.AuthenticatedPrincipal()); err != nil {
			t.Fatalf("link %s: %v", c, err)
		}
	}
}

type matrixGRPCSend struct {
	d       matrixDeployment
	carrier string
	md      metadata.MD
}

// matrixGRPCSends lists where credential c is presented: an allowed one once,
// in its natural deployment and carrier; any other in every carrier
// AuthenticateGRPC reads (x-api-key, api-key, authorization) in both.
func (m *audienceMatrix) matrixGRPCSends(c identity.Credential) []matrixGRPCSend {
	md := func(carrier string) metadata.MD {
		value := m.tokens[c]
		if carrier == "authorization" {
			value = "Bearer " + value
		}
		return metadata.Pairs(carrier, value)
	}
	if identity.Allowed(c, identity.GRPCAPI) {
		d, natural := m.matrixNatural(c)
		carrier := "authorization"
		if natural.name == "x-api-key" {
			carrier = "x-api-key"
		}
		return []matrixGRPCSend{{d, carrier, md(carrier)}}
	}
	var sends []matrixGRPCSend
	for _, d := range []matrixDeployment{m.keys, m.jwt} {
		switch c {
		case identity.Anonymous:
			sends = append(sends, matrixGRPCSend{d, "none", metadata.MD{}})
		case identity.AgentIDHeader:
			sends = append(sends, matrixGRPCSend{d, "x-agent-id", metadata.Pairs("x-agent-id", m.tokens[c])})
		default:
			for _, carrier := range []string{"x-api-key", "api-key", "authorization"} {
				sends = append(sends, matrixGRPCSend{d, carrier, md(carrier)})
			}
		}
	}
	return sends
}

// matrixPresentGRPC calls SubmitJob through apiKeyUnaryInterceptor for each
// send. Accepted is a nil error; only Unauthenticated and PermissionDenied
// count as a refusal, and any other error fails the test.
func (m *audienceMatrix) matrixPresentGRPC(t *testing.T, c identity.Credential) identity.Result {
	t.Helper()
	before := m.jobEffects(t)
	res := identity.Result{}
	var trail []string
	for _, sd := range m.matrixGRPCSends(c) {
		m.s.auth = sd.d.auth
		ctx := metadata.NewIncomingContext(withGRPCMethod(pb.CordumApi_SubmitJob_FullMethodName), sd.md)
		info := &grpc.UnaryServerInfo{FullMethod: pb.CordumApi_SubmitJob_FullMethodName}
		_, err := apiKeyUnaryInterceptor(sd.d.auth)(ctx, &pb.SubmitJobRequest{Prompt: "audience matrix", Topic: "job.test", OrgId: matrixTenant}, info,
			func(ctx context.Context, req any) (any, error) { return m.s.SubmitJob(ctx, req.(*pb.SubmitJobRequest)) })
		if code := status.Code(err); err != nil && code != codes.Unauthenticated && code != codes.PermissionDenied {
			t.Fatalf("SubmitJob %s/%s: unexpected error %v", sd.d.name, sd.carrier, err)
		}
		res.Accepted = res.Accepted || err == nil
		trail = append(trail, fmt.Sprintf("%s/%s=%s", sd.d.name, sd.carrier, status.Code(err)))
	}
	after := m.jobEffects(t)
	res.Effect = after != before
	res.Evidence = fmt.Sprintf("job:state keys + sys.job.submit publishes %d->%d; %v", before, after, trail)
	return res
}
