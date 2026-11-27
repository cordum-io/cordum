package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/mcp/outbound"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func init() {
	registerCrossTenantFamily("PR1-02-MCP-AUDIT",
		crossTenantAuditRead("audit-export", "/api/v1/audit/export"),
		crossTenantAuditRead("audit-verify", "/api/v1/audit/verify"),
		crossTenantAuditRead("audit-events", "/api/v1/audit/events"),
		crossTenantCase{"POST /api/v1/audit/export/test", "audit-export-test", crossTenantAuditExportTest},
		crossTenantCase{"POST /api/v1/audit/legal-hold", "legal-hold-create", crossTenantLegalHoldCreate},
		crossTenantCase{"GET /api/v1/audit/legal-holds", "legal-holds-list", crossTenantLegalHoldList},
		crossTenantCase{"DELETE /api/v1/audit/legal-hold/{id}", "legal-hold-release", crossTenantLegalHoldRelease},
		crossTenantCase{"GET /api/v1/mcp/approvals/{id}", "mcp-approval-detail", crossTenantMCPApprovalDetail},
		crossTenantCase{"POST /api/v1/mcp/verify-signature", "mcp-verify-signature", crossTenantMCPVerifySignature},
		crossTenantMCPAuditRead("outbound", audit.EventMCPToolOutboundInvocation),
		crossTenantMCPAuditRead("usage", audit.EventMCPToolInvocation),
		crossTenantCase{"GET /api/v1/mcp/tools", "mcp-tools", crossTenantMCPTools},
		crossTenantCase{"GET /api/v1/mcp/gateway/config", "mcp-gateway-config", crossTenantMCPGatewayConfig},
		crossTenantCase{"POST /api/v1/mcp/gateway/clients/connect", "mcp-client-connect", crossTenantMCPClientConnect},
		crossTenantCase{"POST /mcp/message", "mcp-message", crossTenantMCPMessage},
		crossTenantCase{"GET /mcp/sse", "mcp-sse", crossTenantMCPSSE},
	)
}

func TestCrossTenantSweepMCPAudit(t *testing.T) {
	runCrossTenantFamily(t, "PR1-02-MCP-AUDIT")
}

func seedCrossTenantAudit(t *testing.T, sw *crossTenantSweep) string {
	t.Helper()
	setTestEntitlements(t, sw.server, licensing.PlanCommunity, func(ent *licensing.Entitlements) {
		ent.AuditExport = true
		ent.LegalHold = true
	})
	client, ok := sw.server.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatal("legal-hold fixture requires a Redis client")
	}
	sw.server.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
	marker := "private-audit-" + uuid.NewString()
	seedAuditEvents(t, sw.server, sw.tenantB.tenantID, []audit.SIEMEvent{{
		EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo,
		Action: "sweep-seed", Decision: "allow", AgentID: marker, Reason: marker,
	}})
	sw.server.auditExporter = newAuditChainSender(sw.server.auditChainer, nil)
	return marker
}

type crossTenantAuditSnapshot struct {
	Events []redis.XMessage
	Holds  map[string]audit.LegalHold
}

func crossTenantAuditState(t *testing.T, sw *crossTenantSweep) crossTenantAuditSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	key := sw.server.auditChainer.StreamKey(sw.tenantB.tenantID)
	events, err := sw.server.redisClient().XRange(ctx, key, "-", "+").Result()
	if err != nil || len(events) == 0 {
		t.Fatalf("read B audit stream: %v (events=%d)", err, len(events))
	}
	holds, err := sw.server.legalHoldStore.ListHolds(ctx, sw.tenantB.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	state := crossTenantAuditSnapshot{Events: events, Holds: map[string]audit.LegalHold{}}
	for _, hold := range holds {
		state.Holds[hold.ID] = *hold
	}
	return state
}

func assertCrossTenantAuditUnchanged(t *testing.T, sw *crossTenantSweep, before crossTenantAuditSnapshot, mark int) {
	t.Helper()
	if !reflect.DeepEqual(before, crossTenantAuditState(t, sw)) {
		t.Error("cross-tenant request changed B audit records or legal holds")
	}
	if len(sw.publishedSince(mark)) != 0 {
		t.Error("cross-tenant audit request published a packet")
	}
}

func crossTenantAuditRead(name, route string) crossTenantCase {
	return crossTenantCase{"GET " + route, name, func(t *testing.T, sw *crossTenantSweep) {
		marker := seedCrossTenantAudit(t, sw)
		seedCrossTenantLegalHold(t, sw, marker)
		path := route + "?tenant=" + url.QueryEscape(sw.tenantB.tenantID)
		if route == "/api/v1/audit/export" {
			path += "&format=json" + rangeQS()
		}
		before, mark := crossTenantAuditState(t, sw), sw.publishMark()
		rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
		assertCrossTenantAuditUnchanged(t, sw, before, mark)
		if route == "/api/v1/audit/verify" {
			assertCrossTenantRefused(t, rr, name, marker)
		} else {
			assertCrossTenantAuditResponse(t, rr, name, marker)
		}
		owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
		assertCrossTenantAbsent(t, owner, "owner "+name)
		if route == "/api/v1/audit/verify" {
			var result audit.VerifyResult
			if err := json.Unmarshal(owner.Body.Bytes(), &result); err != nil || result.TotalEvents != len(before.Events) || result.VerifiedEvents != len(before.Events) {
				t.Fatalf("owner verification did not scan B's chain: %v; %s", err, owner.Body.String())
			}
		} else if !strings.Contains(owner.Body.String(), marker) {
			t.Fatal("owner audit read omitted B's private marker")
		}
	}}
}

func assertCrossTenantAuditResponse(t *testing.T, rr *httptest.ResponseRecorder, label string, leaks ...string) {
	t.Helper()
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusNotFound {
		assertCrossTenantRefused(t, rr, label, leaks...)
		return
	}
	assertCrossTenantAbsent(t, rr, label, leaks...)
}

func seedCrossTenantLegalHold(t *testing.T, sw *crossTenantSweep, reason string) audit.LegalHold {
	t.Helper()
	body := fmt.Sprintf(`{"tenant_id":%q,"reason":%q}`, sw.tenantB.tenantID, reason)
	rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/audit/legal-hold", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("owner hold create: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result struct {
		Hold audit.LegalHold `json:"hold"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || result.Hold.ID == "" || result.Hold.TenantID != sw.tenantB.tenantID || result.Hold.Reason != reason {
		t.Fatalf("decode owner hold: %v; %s", err, rr.Body.String())
	}
	return result.Hold
}

func crossTenantAuditExportTest(t *testing.T, sw *crossTenantSweep) {
	marker := seedCrossTenantAudit(t, sw)
	seedCrossTenantLegalHold(t, sw, marker)
	sw.server.tenant = sw.tenantB.tenantID
	path := "/api/v1/audit/export/test?tenant=" + url.QueryEscape(sw.tenantB.tenantID)
	body := fmt.Sprintf(`{"tenant_id":%q}`, sw.tenantB.tenantID)
	initial := crossTenantAuditState(t, sw)
	owner := sw.as(t, sw.tenantB, http.MethodPost, path, body)
	assertCrossTenantAbsent(t, owner, "owner export test")
	before := crossTenantAuditState(t, sw)
	if len(before.Events) != len(initial.Events)+1 {
		t.Fatal("owner export test did not append to B's audit stream")
	}
	mark := sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, body)
	assertCrossTenantAuditUnchanged(t, sw, before, mark)
	assertCrossTenantAuditResponse(t, rr, "export test", marker)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, path, body), "owner export retest")
}

func crossTenantLegalHoldCreate(t *testing.T, sw *crossTenantSweep) {
	marker := seedCrossTenantAudit(t, sw)
	hold := seedCrossTenantLegalHold(t, sw, marker)
	// Keep B's historical record without an active hold masking authorization.
	release := sw.as(t, sw.tenantB, http.MethodDelete, "/api/v1/audit/legal-hold/"+hold.ID, "")
	assertCrossTenantAbsent(t, release, "owner release before create")
	before, mark := crossTenantAuditState(t, sw), sw.publishMark()
	body := fmt.Sprintf(`{"tenant_id":%q,"reason":"new hold"}`, sw.tenantB.tenantID)
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/audit/legal-hold", body)
	assertCrossTenantAuditUnchanged(t, sw, before, mark)
	assertCrossTenantAuditResponse(t, rr, "hold create", marker, hold.ID)
	seedCrossTenantLegalHold(t, sw, "owner control")
}

func crossTenantLegalHoldList(t *testing.T, sw *crossTenantSweep) {
	marker := seedCrossTenantAudit(t, sw)
	hold := seedCrossTenantLegalHold(t, sw, marker)
	for _, query := range []string{"?tenant=" + url.QueryEscape(sw.tenantB.tenantID), ""} {
		before, mark := crossTenantAuditState(t, sw), sw.publishMark()
		rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/audit/legal-holds"+query, "")
		assertCrossTenantAuditUnchanged(t, sw, before, mark)
		assertCrossTenantAuditResponse(t, rr, "holds list", marker, hold.ID)
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/audit/legal-holds?tenant="+url.QueryEscape(sw.tenantB.tenantID), "")
	assertCrossTenantAbsent(t, owner, "owner holds list")
	if !strings.Contains(owner.Body.String(), marker) || !strings.Contains(owner.Body.String(), hold.ID) {
		t.Fatal("owner list omitted B's hold")
	}
}

func crossTenantLegalHoldRelease(t *testing.T, sw *crossTenantSweep) {
	marker := seedCrossTenantAudit(t, sw)
	hold := seedCrossTenantLegalHold(t, sw, marker)
	control := sw.as(t, sw.tenantB, http.MethodDelete, "/api/v1/audit/legal-hold/"+hold.ID, "")
	assertCrossTenantAbsent(t, control, "owner release control")
	hold = seedCrossTenantLegalHold(t, sw, marker)
	path := "/api/v1/audit/legal-hold/" + hold.ID
	before, mark := crossTenantAuditState(t, sw), sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodDelete, path, "")
	assertCrossTenantAuditUnchanged(t, sw, before, mark)
	assertCrossTenantRefused(t, rr, "hold release", marker)
	owner := sw.as(t, sw.tenantB, http.MethodDelete, path, "")
	assertCrossTenantAbsent(t, owner, "owner hold release")
	after := crossTenantAuditState(t, sw)
	released, ok := after.Holds[hold.ID]
	if !ok || released.ReleasedAt == nil || released.ReleasedBy != sw.tenantB.principalID {
		t.Fatal("owner release did not update B's hold")
	}
}

func crossTenantMCPApprovalDetail(t *testing.T, sw *crossTenantSweep) {
	store := NewMCPApprovalStore(sw.server.jobStore.Client())
	sw.server.setMCPRuntime(&mcpRuntimeState{approvalStore: store, approvalHandler: newMCPApprovalHandler(store)})
	t.Cleanup(func() { sw.server.clearMCPRuntime() })
	marker := "private-requester-" + uuid.NewString()
	approval := enqueueTestApproval(t, store, marker, sw.tenantB.tenantID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	before, err := store.Get(ctx, approval.ID)
	if err != nil || before == nil || before.Status != "pending" {
		t.Fatalf("read B pending approval: %v; %+v", err, before)
	}
	path, mark := "/api/v1/mcp/approvals/"+approval.ID, sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	after, err := store.Get(ctx, approval.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B approval changed: %v", err)
	}
	if len(sw.publishedSince(mark)) != 0 {
		t.Error("cross-tenant approval read published a packet")
	}
	assertCrossTenantRefused(t, rr, "approval detail", marker)
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "owner approval detail")
	if !strings.Contains(owner.Body.String(), marker) {
		t.Fatal("owner approval detail omitted B's requester")
	}
}

func crossTenantMCPVerifySignature(t *testing.T, sw *crossTenantSweep) {
	marker := seedCrossTenantAudit(t, sw)
	seedCrossTenantLegalHold(t, sw, marker)
	clearMCPInboundTrustEnv(t)
	_, publicKey := genTestKey(t)
	t.Setenv(outbound.EnvTrustedKeyPrefix+"SWEEP", publicKey)
	sw.server.reloadMCPVerifier()
	if verifier, err := sw.server.mcpVerifier(); err != nil || verifier == nil {
		t.Fatalf("trusted verifier fixture unavailable: %v", err)
	}
	body := fmt.Sprintf(`{"method":"tools/call","params":{},"headers":{"%s":%q}}`, outbound.HeaderTenant, sw.tenantB.tenantID)
	path := "/api/v1/mcp/verify-signature"
	initial := crossTenantAuditState(t, sw)
	owner := sw.as(t, sw.tenantB, http.MethodPost, path, body)
	assertCrossTenantAbsent(t, owner, "owner signature check")
	var result mcpVerifySignatureResponse
	if err := json.Unmarshal(owner.Body.Bytes(), &result); err != nil || result.OK || result.SubReason != "missing" {
		t.Fatalf("owner did not evaluate the incomplete signature: %v; %s", err, owner.Body.String())
	}
	before, mark := crossTenantAuditState(t, sw), sw.publishMark()
	if len(before.Events) != len(initial.Events)+1 {
		t.Fatal("owner verification did not append its audit record")
	}
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, body)
	assertCrossTenantAuditUnchanged(t, sw, before, mark)
	assertCrossTenantAuditResponse(t, rr, "signature verification", marker)
}

func crossTenantMCPAuditRead(name, eventType string) crossTenantCase {
	route := "/api/v1/mcp/" + name
	return crossTenantCase{"GET " + route, "mcp-" + name, func(t *testing.T, sw *crossTenantSweep) {
		marker := seedCrossTenantAudit(t, sw)
		seedAuditEvents(t, sw.server, sw.tenantB.tenantID, []audit.SIEMEvent{{
			EventType: eventType, AgentID: marker, Decision: "allow",
			Extra: map[string]string{"target_server": "private-upstream-b", "tool_name": "private-tool-b"},
		}})
		path := route + "?tenant=" + url.QueryEscape(sw.tenantB.tenantID)
		before, mark := crossTenantAuditState(t, sw), sw.publishMark()
		rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
		assertCrossTenantAuditUnchanged(t, sw, before, mark)
		assertCrossTenantAuditResponse(t, rr, name, marker, "private-tool-b")
		owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
		assertCrossTenantAbsent(t, owner, "owner "+name)
		if !strings.Contains(owner.Body.String(), marker) || !strings.Contains(owner.Body.String(), "private-tool-b") {
			t.Fatal("owner MCP audit read omitted B's event")
		}
	}}
}

func crossTenantMCPTools(t *testing.T, sw *crossTenantSweep) {
	enableAgentIdentityEntitlement(t, sw.server)
	agent := seedFleetAgent(t, sw, fleetAgentBody("private-mcp-agent-b", ""))
	registry := mcp.NewToolRegistry()
	tool := mcp.Tool{Name: "fleet.read", Description: "private-mcp-tool-description-b", RiskTier: "low"}
	if err := registry.Register(tool, func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
		t.Error("listing tools must not execute a tool")
		return &mcp.ToolCallResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	sw.server.setMCPRuntime(&mcpRuntimeState{toolRegistry: registry})
	t.Cleanup(func() { sw.server.clearMCPRuntime() })
	path, mark := "/api/v1/mcp/tools?agent_id="+url.QueryEscape(agent.ID), sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantRefused(t, rr, "MCP tools", tool.Description)
	if len(sw.publishedSince(mark)) != 0 {
		t.Error("tool list published a packet")
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "owner MCP tools")
	if !strings.Contains(owner.Body.String(), tool.Description) {
		t.Fatal("owner tools omitted the allowed tool")
	}
}

func crossTenantMCPGatewayConfig(t *testing.T, sw *crossTenantSweep) {
	upstream := frontingReadyUpstream(sw.tenantB.tenantID, "private-b-upstream")
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, "POST", "/api/v1/edge/mcp/upstreams", mustJSON(upstream)), "owner upstream seed")
	before, mark := crossTenantUpstream(t, sw), sw.publishMark()
	path := "/api/v1/mcp/gateway/config?tenant=" + url.QueryEscape(sw.tenantB.tenantID)
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "MCP gateway config", upstream.Name, upstream.Endpoint)
	var config struct {
		Count int `json:"upstream_count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &config); err != nil || config.Count != 0 {
		t.Fatalf("A upstream count: %+v, %v", config, err)
	}
	if !reflect.DeepEqual(before, crossTenantUpstream(t, sw)) || len(sw.publishedSince(mark)) != 0 {
		t.Error("config read changed B upstream or published a packet")
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "owner MCP gateway config")
	if err := json.Unmarshal(owner.Body.Bytes(), &config); err != nil || config.Count != 1 {
		t.Fatalf("B upstream count: %+v, %v", config, err)
	}
}

func crossTenantMCPClientConnect(t *testing.T, sw *crossTenantSweep) {
	seed := seedCrossTenantEdge(t, sw, true)
	marker := seedCrossTenantAudit(t, sw)
	seedCrossTenantLegalHold(t, sw, marker)
	store := NewMCPApprovalStore(sw.server.jobStore.Client())
	approval := enqueueTestApproval(t, store, "private-mcp-requester-b", sw.tenantB.tenantID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	before, auditBefore, mark := crossTenantEdgeStateOf(t, sw, seed), crossTenantAuditState(t, sw), sw.publishMark()
	body := seed.expand(sw, `{"tenant_id":"{tenant}","session_id":"{session_id}","execution_id":"{execution_id}"}`)
	path := "/api/v1/mcp/gateway/clients/connect"
	rr := sw.as(t, sw.tenantA, http.MethodPost, path, body)
	assertCrossTenantAuditUnchanged(t, sw, auditBefore, mark)
	if !reflect.DeepEqual(before, crossTenantEdgeStateOf(t, sw, seed)) {
		t.Error("client connect changed B sessions, executions, approvals or events")
	}
	after, err := store.Get(ctx, approval.ID)
	if err != nil || !reflect.DeepEqual(approval, after) {
		t.Errorf("client connect changed B MCP approval: %v", err)
	}
	assertCrossTenantAbsent(t, rr, "client connect", sw.tenantB.tenantID, seed.sessionID, seed.executionID, marker)
	owner := sw.as(t, sw.tenantB, http.MethodPost, path, body)
	assertCrossTenantAbsent(t, owner, "owner client connect")
	if !strings.Contains(owner.Body.String(), sw.tenantB.tenantID) || reflect.DeepEqual(before, crossTenantEdgeStateOf(t, sw, seed)) {
		t.Fatal("owner connect did not create B evidence")
	}
}

const crossTenantMCPTool = "sweep_count"

type crossTenantMCPHarness struct {
	sw    *crossTenantSweep
	seed  crossTenantEdgeSeed
	mu    sync.Mutex
	calls []string
}

func crossTenantMCPAgent(who crossTenantSweepTenant) string {
	return "sweep-mcp-agent-" + who.principalID
}

// startCrossTenantMCP boots the HTTP MCP runtime with one identity per tenant
// and a tool that records the tenant each call runs for.
func startCrossTenantMCP(t *testing.T, sw *crossTenantSweep) *crossTenantMCPHarness {
	t.Helper()
	h := &crossTenantMCPHarness{sw: sw, seed: seedCrossTenantEdge(t, sw, false)}
	s := sw.server
	s.shutdownCh = make(chan struct{})
	setMCPConfigForBoot(t, s, true, false)
	s.reloadMCPConfig(context.Background())
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.httpTransport == nil || runtime.toolRegistry == nil {
		t.Fatal("MCP runtime did not start")
	}
	t.Cleanup(func() { s.stopMCPRuntime(runtime); close(s.shutdownCh) })
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		if _, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{ID: crossTenantMCPAgent(who), TenantID: who.tenantID,
			Name: crossTenantMCPAgent(who), Owner: who.principalID, RiskTier: "high", AllowedTools: []string{crossTenantMCPTool}}); err != nil {
			t.Fatalf("create identity for %s: %v", who.tenantID, err)
		}
	}
	tool := mcp.Tool{Name: crossTenantMCPTool, InputSchema: map[string]any{"type": "object"}}
	if err := runtime.toolRegistry.Register(tool, func(ctx context.Context, _ json.RawMessage) (*mcp.ToolCallResult, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.calls = append(h.calls, mcp.TenantFromContext(ctx))
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "sweep-tool-ran"}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *crossTenantMCPHarness) do(t *testing.T, who crossTenantSweepTenant, method, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body := ""
	if method == http.MethodPost {
		body = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + crossTenantMCPTool + `","arguments":{}}}`
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-API-Key", who.apiKey)
	req.Header.Set("X-Tenant-ID", who.tenantID)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	h.sw.handler.ServeHTTP(rr, req)
	return rr
}

func (h *crossTenantMCPHarness) ranFor() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// refuseFronted fronts the runtime for B only; A must then be refused.
func (h *crossTenantMCPHarness) refuseFronted(t *testing.T, method, path string) {
	t.Helper()
	runtime := h.sw.server.getMCPRuntime()
	runtime.frontedUpstream, runtime.frontedTenant = "private-b-upstream", h.sw.tenantB.tenantID
	assertCrossTenantRefused(t, h.do(t, h.sw.tenantA, method, path, mcpAgentIDHeader, crossTenantMCPAgent(h.sw.tenantA)), "MCP fronted for B", h.seed.marker)
}

func (h *crossTenantMCPHarness) assertBUntouched(t *testing.T, before crossTenantEdgeState, mark int) {
	t.Helper()
	for _, tenant := range h.ranFor() {
		if tenant == h.sw.tenantB.tenantID {
			t.Error("tenant A's MCP request ran a tool for tenant B")
		}
	}
	if !reflect.DeepEqual(before, crossTenantEdgeStateOf(t, h.sw, h.seed)) {
		t.Error("tenant A's MCP request changed B's sessions, executions or events")
	}
	for _, m := range h.sw.publishedSince(mark) {
		if strings.Contains(m.subject+fmt.Sprint(m.packet), h.sw.tenantB.tenantID) {
			t.Error("tenant A's MCP request published a packet carrying tenant B")
		}
	}
}

func crossTenantMCPMessage(t *testing.T, sw *crossTenantSweep) {
	h := startCrossTenantMCP(t, sw)
	agentA, agentB := crossTenantMCPAgent(sw.tenantA), crossTenantMCPAgent(sw.tenantB)
	spoofed := sw.tenantA
	spoofed.tenantID = sw.tenantB.tenantID
	before, mark := crossTenantEdgeStateOf(t, sw, h.seed), sw.publishMark()
	assertCrossTenantRefused(t, h.do(t, spoofed, http.MethodPost, "/mcp/message", mcpAgentIDHeader, agentA), "MCP message tenant header", h.seed.marker)
	assertCrossTenantAuditResponse(t, h.do(t, sw.tenantA, http.MethodPost, "/mcp/message", mcpAgentIDHeader, agentB), "MCP message B agent", h.seed.marker)
	path := "/mcp/message?session_id=" + url.QueryEscape(h.seed.sessionID)
	assertCrossTenantAuditResponse(t, h.do(t, sw.tenantA, http.MethodPost, path, "X-MCP-Session-ID", h.seed.sessionID, mcpAgentIDHeader, agentA), "MCP message B session", h.seed.marker)
	assertCrossTenantAbsent(t, h.do(t, sw.tenantA, http.MethodPost, "/mcp/message", mcpAgentIDHeader, agentA), "A control MCP message", h.seed.marker)
	h.refuseFronted(t, http.MethodPost, "/mcp/message")
	h.assertBUntouched(t, before, mark)
	if got := h.ranFor(); !reflect.DeepEqual(got, []string{sw.tenantA.tenantID}) {
		t.Fatalf("A control: tool ran for %v, want only tenant A", got)
	}
	owner := h.do(t, sw.tenantB, http.MethodPost, "/mcp/message", mcpAgentIDHeader, agentB)
	assertCrossTenantAbsent(t, owner, "owner MCP message")
	if got := h.ranFor(); !strings.Contains(owner.Body.String(), "sweep-tool-ran") || len(got) == 0 || got[len(got)-1] != sw.tenantB.tenantID {
		t.Fatalf("owner control: tool did not run for B: %v", got)
	}
}

func crossTenantMCPSSE(t *testing.T, sw *crossTenantSweep) {
	h := startCrossTenantMCP(t, sw)
	spoofed := sw.tenantA
	spoofed.tenantID = sw.tenantB.tenantID
	before, mark := crossTenantEdgeStateOf(t, sw, h.seed), sw.publishMark()
	assertCrossTenantRefused(t, h.do(t, spoofed, http.MethodGet, "/mcp/sse"), "MCP SSE tenant header", h.seed.marker)
	path := "/mcp/sse?session_id=" + url.QueryEscape(h.seed.sessionID)
	for _, header := range [][]string{{"X-MCP-Session-ID", h.seed.sessionID}, {mcpAgentIDHeader, crossTenantMCPAgent(sw.tenantB)}} {
		rr := h.do(t, sw.tenantA, http.MethodGet, path, header...)
		assertCrossTenantAuditResponse(t, rr, "MCP SSE "+header[0], h.seed.marker)
		if rr.Header().Get("X-MCP-Session-ID") == h.seed.sessionID {
			t.Error("A's SSE stream took B's session id")
		}
	}
	h.refuseFronted(t, http.MethodGet, "/mcp/sse")
	h.assertBUntouched(t, before, mark)
	owner := h.do(t, sw.tenantB, http.MethodGet, "/mcp/sse")
	assertCrossTenantAbsent(t, owner, "owner MCP SSE")
	if !strings.Contains(owner.Body.String(), "event: session") {
		t.Fatalf("owner control: B's SSE stream sent no session event: %q", owner.Body.String())
	}
}

// TestCrossTenantSweepMCPAuditGlobalReads backs this row's global routes in the
// manifest: with B's audit, hold, upstream and MCP session seeded, tenant A's
// replies carry none of B's data and B's state is unchanged.
func TestCrossTenantSweepMCPAuditGlobalReads(t *testing.T) {
	sw := newCrossTenantSweep(t)
	marker := seedCrossTenantAudit(t, sw)
	hold := seedCrossTenantLegalHold(t, sw, marker)
	upstream := frontingReadyUpstream(sw.tenantB.tenantID, "private-b-upstream")
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/edge/mcp/upstreams", mustJSON(upstream)), "owner upstream seed")
	h := startCrossTenantMCP(t, sw)
	leaks := []string{marker, hold.ID, upstream.Endpoint, h.seed.sessionID, crossTenantMCPAgent(sw.tenantB)}
	registry, auditBefore := crossTenantUpstream(t, sw), crossTenantAuditState(t, sw)
	before, mark := crossTenantEdgeStateOf(t, sw, h.seed), sw.publishMark()
	query := "?tenant=" + url.QueryEscape(sw.tenantB.tenantID)
	for _, path := range []string{"/api/v1/audit/export/health", "/api/v1/audit/export/config", "/api/v1/mcp/gateway/health", "/mcp/status"} {
		assertCrossTenantAbsent(t, sw.as(t, sw.tenantA, http.MethodGet, path+query, ""), path, append(leaks, upstream.Name)...)
	}
	body := fmt.Sprintf(`{"tenant_id":%q}`, sw.tenantB.tenantID)
	for _, path := range []string{"/api/v1/mcp/gateway/upstream", "/api/v1/mcp/gateway/upstream/" + upstream.Name} {
		rr := sw.as(t, sw.tenantA, http.MethodPost, path+query, body)
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "gateway_disabled") {
			t.Errorf("%s: status=%d, want 503 gateway_disabled; body=%s", path, rr.Code, rr.Body.String())
		}
		assertCrossTenantNoLeaks(t, rr, path, leaks...)
	}
	if !reflect.DeepEqual(registry, crossTenantUpstream(t, sw)) || !reflect.DeepEqual(auditBefore, crossTenantAuditState(t, sw)) {
		t.Error("a global route changed B's upstream, audit records or legal holds")
	}
	h.assertBUntouched(t, before, mark)
}
