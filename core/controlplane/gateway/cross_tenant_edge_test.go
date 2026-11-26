package gateway

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
)

func init() {
	for _, tc := range []struct{ name, path, body string }{
		{"session-create", "/sessions", `{"tenant_id":"{tenant}","agent_product":"claude-code","agent_version":"1.2.3","mode":"local-dev","policy_mode":"observe"}`},
		{"session-heartbeat", "/sessions/{session_id}/heartbeat", `{}`},
		{"session-end", "/sessions/{session_id}/end", `{}`},
		{"execution-create", "/executions", `{"session_id":"{session_id}","adapter":"claude-code-hook","mode":"local-dev"}`},
		{"execution-end", "/executions/{execution_id}/end", `{}`},
		{"approval-approve", "/approvals/{approval_ref}/approve", `{"reason":"reviewed"}`},
		{"approval-reject", "/approvals/{approval_ref}/reject", `{"reason":"declined"}`},
		{"approval-wait", "/approvals/{approval_ref}/wait", `{"timeout_ms":1}`},
		{"evaluate", "/evaluate", `{"session_id":"{session_id}","execution_id":"{execution_id}","agent_product":"claude-code","layer":"hook","kind":"hook.pre_tool_use","tool_name":"Bash","input_redacted":{"command":"echo sweep"}}`},
	} {
		registerCrossTenantFamily("PR1-02-EDGE-MCP", crossTenantEdgeWrite(tc.name, "/api/v1/edge"+tc.path, tc.body))
	}
}

func TestCrossTenantSweepEdge(t *testing.T) {
	runCrossTenantFamily(t, "PR1-02-EDGE-MCP")
}

type crossTenantEdgeSeed struct {
	sessionID, executionID, approvalRef, marker string
	audit                                       *testAuditSender
}

func seedCrossTenantEdge(t *testing.T, sw *crossTenantSweep, approval bool) crossTenantEdgeSeed {
	t.Helper()
	seed := crossTenantEdgeSeed{marker: sw.tenantB.principalID, audit: &testAuditSender{}}
	sw.server.auditExporter = seed.audit
	sw.server.safetyClient = &edgeEvaluateStubSafetyClient{}
	if approval {
		seed.marker = "private-edge-sweep-requester"
		a := seedGatewayEdgeApproval(t, sw.server, sw.tenantB.tenantID, seed.marker, "private-label")
		seed.sessionID, seed.executionID, seed.approvalRef = a.SessionID, a.ExecutionID, a.ApprovalRef
	} else {
		session := createEdgeRouteSessionForTenant(t, sw.handler, sw.tenantB.apiKey, sw.tenantB.tenantID)
		execution := createCrossTenantExecution(t, sw.handler, sw.tenantB.apiKey, sw.tenantB.tenantID, session.SessionID)
		seed.sessionID, seed.executionID = session.SessionID, execution.ExecutionID
	}
	return seed
}

func (seed crossTenantEdgeSeed) expand(sw *crossTenantSweep, value string) string {
	return strings.NewReplacer("{tenant}", sw.tenantB.tenantID, "{session_id}", seed.sessionID,
		"{execution_id}", seed.executionID, "{approval_ref}", seed.approvalRef).Replace(value)
}

type crossTenantEdgeState struct {
	Views          map[string]string
	HeartbeatAlive bool
	Executions     int64
	Events         edgecore.EventPage
	Approval       *edgecore.EdgeApproval
	Audit          []audit.SIEMEvent
}

func crossTenantEdgeStateOf(t *testing.T, sw *crossTenantSweep, seed crossTenantEdgeSeed) crossTenantEdgeState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	state := crossTenantEdgeState{Views: map[string]string{}}
	for _, path := range []string{"/sessions", "/sessions/{session_id}", "/executions?session_id={session_id}", "/executions/{execution_id}", "/approvals"} {
		rr := sw.as(t, sw.tenantB, http.MethodGet, seed.expand(sw, "/api/v1/edge"+path), "")
		assertCrossTenantAbsent(t, rr, "B state snapshot")
		state.Views[path] = rr.Body.String()
	}
	var err error
	state.HeartbeatAlive, err = sw.server.edgeStore.HeartbeatAlive(ctx, sw.tenantB.tenantID, seed.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	state.Executions, err = sw.server.edgeStore.CountSessionExecutions(ctx, sw.tenantB.tenantID, seed.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	state.Events, err = sw.server.edgeStore.ListEvents(ctx, edgecore.ListEventsQuery{TenantID: sw.tenantB.tenantID, SessionID: seed.sessionID, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if seed.approvalRef != "" {
		var found bool
		state.Approval, found, err = sw.server.edgeStore.GetApproval(ctx, sw.tenantB.tenantID, seed.approvalRef)
		if err != nil || !found || state.Approval == nil {
			t.Fatalf("B approval missing: %v", err)
		}
	}
	for i := 0; i < seed.audit.Len(); i++ {
		if event := seed.audit.Get(i); event.TenantID == sw.tenantB.tenantID {
			state.Audit = append(state.Audit, event)
		}
	}
	return state
}

func crossTenantEdgeWrite(name, path, body string) crossTenantCase {
	return crossTenantCase{"POST " + path, name, func(t *testing.T, sw *crossTenantSweep) {
		seed := seedCrossTenantEdge(t, sw, strings.Contains(path, "/approvals/"))
		// An absent heartbeat makes any unauthorized refresh observable, not just a TTL extension.
		if name == "session-heartbeat" {
			if err := sw.server.jobStore.Client().Del(context.Background(), "edge:session:heartbeat:"+seed.sessionID).Err(); err != nil {
				t.Fatal(err)
			}
		}
		before := crossTenantEdgeStateOf(t, sw, seed)
		if !strings.Contains(before.Views["/sessions/{session_id}"], seed.marker) {
			t.Fatal("owner session missing private marker")
		}
		if before.Approval != nil && before.Approval.Status != edgecore.ApprovalStatusPending {
			t.Fatal("approval seed is not pending")
		}
		mark := sw.publishMark()
		target, payload := seed.expand(sw, path), seed.expand(sw, body)
		rr := sw.as(t, sw.tenantA, http.MethodPost, target, payload)
		after, published := crossTenantEdgeStateOf(t, sw, seed), sw.publishedSince(mark)
		owner := sw.as(t, sw.tenantB, http.MethodPost, target, payload)
		assertCrossTenantAbsent(t, owner, "owner "+name)
		if name != "approval-wait" && reflect.DeepEqual(after, crossTenantEdgeStateOf(t, sw, seed)) {
			t.Error("owner write had no observable effect")
		}
		if !reflect.DeepEqual(before, after) {
			t.Error("cross-tenant write changed B state")
		}
		if len(published) != 0 {
			t.Error("cross-tenant refusal published a packet")
		}
		assertCrossTenantRefused(t, rr, fmt.Sprintf("A %s", name), seed.marker)
	}}
}
