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
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/mcp"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func init() {
	registerCrossTenantFamily("PR1-02-MCP-AUDIT",
		crossTenantAuditRead("audit-export", "/api/v1/audit/export"),
		crossTenantAuditRead("audit-verify", "/api/v1/audit/verify"),
		crossTenantAuditRead("audit-events", "/api/v1/audit/events"),
		crossTenantCase{"GET /api/v1/mcp/approvals/{id}", "mcp-approval-detail", crossTenantMCPApprovalDetail},
		crossTenantMCPAuditRead("outbound", audit.EventMCPToolOutboundInvocation),
		crossTenantMCPAuditRead("usage", audit.EventMCPToolInvocation),
		crossTenantCase{"GET /api/v1/mcp/tools", "mcp-tools", crossTenantMCPTools},
		crossTenantCase{"GET /api/v1/mcp/gateway/config", "mcp-gateway-config", crossTenantMCPGatewayConfig},
		crossTenantCase{"POST /api/v1/mcp/gateway/clients/connect", "mcp-client-connect", crossTenantMCPClientConnect},
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
