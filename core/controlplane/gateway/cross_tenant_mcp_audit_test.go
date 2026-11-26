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
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func init() {
	registerCrossTenantFamily("PR1-02-MCP-AUDIT",
		crossTenantAuditRead("audit-export", "/api/v1/audit/export"),
		crossTenantAuditRead("audit-verify", "/api/v1/audit/verify"),
		crossTenantAuditRead("audit-events", "/api/v1/audit/events"),
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
