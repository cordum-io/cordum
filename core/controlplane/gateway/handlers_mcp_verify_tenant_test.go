package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/mcp/outbound"
	"github.com/redis/go-redis/v9"
)

func TestMCPVerifySignatureAuditTenant(t *testing.T) {
	for _, name := range []string{"other-tenant-claim", "own-tenant", "unresolved-tenant", "platform-caller"} {
		t.Run(name, func(t *testing.T) {
			sw := mcpVerifyTenantFixture(t)
			claimed, target := sw.tenantB.tenantID, sw.tenantA.tenantID
			if name == "own-tenant" {
				claimed = sw.tenantA.tenantID
			}
			before := mcpVerifyTenantState(t, sw)
			body := fmt.Sprintf(`{"method":"tools/call","params":{},"headers":{"%s":%q}}`, outbound.HeaderTenant, claimed)
			var rr *httptest.ResponseRecorder
			switch name {
			case "unresolved-tenant", "platform-caller":
				target = ""
				if name == "platform-caller" {
					target = sw.server.tenant
				}
				rr = mcpVerifyTenantDirect(t, sw, body, name == "platform-caller")
			default:
				rr = sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/mcp/verify-signature", body)
			}
			if name == "unresolved-tenant" {
				if rr.Code != http.StatusForbidden {
					t.Errorf("status=%d, want 403", rr.Code)
				}
			} else {
				var result mcpVerifySignatureResponse
				want := mcpVerifySignatureResponse{SubReason: "missing", Tenant: claimed}
				if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || rr.Code != http.StatusOK || result != want {
					t.Errorf("verify response: status=%d result=%+v error=%v", rr.Code, result, err)
				}
			}
			after := mcpVerifyTenantState(t, sw)
			mcpVerifyTenantAssertState(t, before, after, target, claimed, name != "own-tenant")
		})
	}
}

func mcpVerifyTenantFixture(t *testing.T) *crossTenantSweep {
	t.Helper()
	sw := newCrossTenantSweep(t)
	sw.server.tenant = "mcp-verify-platform"
	setTestEntitlements(t, sw.server, licensing.PlanCommunity, func(ent *licensing.Entitlements) {
		ent.AuditExport = true
		ent.LegalHold = true
	})
	client, ok := sw.server.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatal("legal-hold fixture requires a Redis client")
	}
	sw.server.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
	seedAuditEvents(t, sw.server, sw.tenantB.tenantID, []audit.SIEMEvent{{
		EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo,
		Action: "tenant-fixture", Reason: "tenant-b-audit-record",
	}})
	sw.server.auditExporter = newAuditChainSender(sw.server.auditChainer, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := sw.server.legalHoldStore.CreateHold(ctx, sw.tenantB.tenantID, "tenant-b-hold", "fixture"); err != nil {
		t.Fatal(err)
	}
	clearMCPInboundTrustEnv(t)
	_, publicKey := genTestKey(t)
	t.Setenv(outbound.EnvTrustedKeyPrefix+"TENANT", publicKey)
	sw.server.reloadMCPVerifier()
	return sw
}

func mcpVerifyTenantDirect(t *testing.T, sw *crossTenantSweep, body string, platform bool) *httptest.ResponseRecorder {
	t.Helper()
	reader := strings.NewReader(body)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/verify-signature", reader).WithContext(ctx)
	req = withAuth(req, &auth.AuthContext{Role: "admin", AllowCrossTenant: platform})
	rr := httptest.NewRecorder()
	sw.server.handleMCPVerifySignature(rr, req)
	if !platform && reader.Len() != len(body) {
		t.Error("unresolved tenant request body was read")
	}
	return rr
}

type mcpVerifyTenantSnapshot struct {
	events map[string][]redis.XMessage
	holds  map[string]audit.LegalHold
}

func mcpVerifyTenantState(t *testing.T, sw *crossTenantSweep) mcpVerifyTenantSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	state := mcpVerifyTenantSnapshot{
		events: make(map[string][]redis.XMessage),
		holds:  make(map[string]audit.LegalHold),
	}
	for _, tenant := range []string{sw.tenantA.tenantID, sw.tenantB.tenantID, sw.server.tenant} {
		events, err := sw.server.redisClient().XRange(ctx, sw.server.auditChainer.StreamKey(tenant), "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		state.events[tenant] = events
	}
	holds, err := sw.server.legalHoldStore.ListHolds(ctx, sw.tenantB.tenantID)
	if err != nil || len(holds) != 1 || len(state.events[sw.tenantB.tenantID]) == 0 {
		t.Fatalf("B fixture unavailable: holds=%d error=%v", len(holds), err)
	}
	for _, hold := range holds {
		state.holds[hold.ID] = *hold
	}
	return state
}

func mcpVerifyTenantAssertState(t *testing.T, before, after mcpVerifyTenantSnapshot, target, claimed string, checkClaim bool) {
	t.Helper()
	if !reflect.DeepEqual(before.holds, after.holds) {
		t.Error("B legal holds changed")
	}
	for tenant, events := range before.events {
		got := after.events[tenant]
		if tenant != target {
			if !reflect.DeepEqual(events, got) {
				t.Errorf("audit stream for %q changed", tenant)
			}
			continue
		}
		if len(got) != len(events)+1 {
			t.Errorf("audit stream for %q gained %d events, want 1", tenant, len(got)-len(events))
			continue
		}
		if !reflect.DeepEqual(events, got[:len(events)]) {
			t.Errorf("existing audit stream for %q changed", tenant)
		}
		payload, ok := got[len(events)].Values["event"].(string)
		if !ok {
			t.Fatal("audit event payload is not a string")
		}
		var event audit.SIEMEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatal(err)
		}
		if event.EventType != audit.EventMCPSignatureInvalid || event.TenantID != target || event.Reason != "missing" {
			t.Errorf("unexpected audit event: %+v", event)
		}
		if checkClaim && event.Extra["claimed_tenant"] != claimed {
			t.Errorf("claimed_tenant=%q, want %q", event.Extra["claimed_tenant"], claimed)
		}
	}
}
