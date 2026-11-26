package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/model"
)

func init() {
	registerCrossTenantFamily("PR1-02-PLATFORM",
		crossTenantCase{"GET /api/v1/license/usage", "license-usage", crossTenantPlatformUsage},
		crossTenantCase{"GET /api/v1/copilot/sessions/{sessionId}", "copilot-session", crossTenantPlatformCopilot},
		crossTenantPlatformGovernance("/api/v1/governance/health", "governance-health"),
		crossTenantPlatformGovernance("/api/v1/governance/decisions", "governance-decisions"),
		crossTenantPlatformGovernance("/api/v1/governance/approvals/analytics", "approval-analytics"),
	)
}

func TestCrossTenantSweepPlatform(t *testing.T) {
	runCrossTenantFamily(t, "PR1-02-PLATFORM")
}

func crossTenantPlatformUsage(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sw.server.jobStore.SetStateWithContext(ctx, id, model.JobStateScheduled, nil); err != nil {
		t.Fatal(err)
	}
	before := crossTenantJobMeta(t, sw, id)
	path := "/api/v1/license/usage?tenant=" + sw.tenantB.tenantID
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, rr, "A usage", marker, sw.tenantB.tenantID)
	assertCrossTenantPlatformUsage(t, rr, sw.tenantA.tenantID, 0)
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Error("usage read changed B job")
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "B usage control")
	assertCrossTenantPlatformUsage(t, owner, sw.tenantB.tenantID, 1)
}

func assertCrossTenantPlatformUsage(t *testing.T, rr *httptest.ResponseRecorder, tenant string, want int) {
	t.Helper()
	var body struct {
		TenantID string `json:"tenant_id"`
		Usage    struct {
			Jobs struct {
				Current *int `json:"current"`
			} `json:"concurrent_jobs"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TenantID != tenant || body.Usage.Jobs.Current == nil || *body.Usage.Jobs.Current != want {
		t.Errorf("usage must describe tenant %s with %d active jobs: %s", tenant, want, rr.Body.String())
	}
}

func crossTenantPlatformCopilot(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	before := crossTenantJobMeta(t, sw, id)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := copilot.NewRedisStore(sw.server.jobStore.Client())
	sw.server.copilotStore = store
	const sessionID = "sess-platform-b"
	msg := copilot.CopilotMessage{ID: "message-b", Role: "user", Content: marker, JobIDs: []string{id}}
	if err := store.AppendMessage(ctx, sw.tenantB.tenantID, sw.tenantB.principalID, sessionID, msg); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetSession(ctx, sw.tenantB.tenantID, sessionID, sw.tenantB.principalID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/copilot/sessions/" + sessionID
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantRefused(t, rr, "A copilot", marker, sw.tenantB.tenantID)
	after, err := store.GetSession(ctx, sw.tenantB.tenantID, sessionID, sw.tenantB.principalID)
	if err != nil || !reflect.DeepEqual(saved, after) || !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Errorf("copilot read changed B records: %v", err)
	}
	owner := sw.as(t, sw.tenantB, http.MethodGet, path, "")
	assertCrossTenantAbsent(t, owner, "B copilot control")
	if !strings.Contains(owner.Body.String(), marker) {
		t.Error("B copilot control missing seeded session")
	}
}

func crossTenantPlatformGovernance(path, name string) crossTenantCase {
	return crossTenantCase{"GET " + path, name, func(t *testing.T, sw *crossTenantSweep) {
		id, marker := seedCrossTenantJob(t, sw)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		record := model.DecisionLogRecord{
			JobID: id, Tenant: sw.tenantB.tenantID, Topic: "job.platform", AgentID: "platform-b-agent",
			Verdict: model.SafetyRequireApproval, RuleID: marker, Reason: marker, Timestamp: time.Now().Add(-time.Second).UnixMilli(),
		}
		if err := sw.server.decisionLogStore.AppendDecision(ctx, record); err != nil {
			t.Fatal(err)
		}
		before, meta := crossTenantPlatformDecisions(t, sw), crossTenantJobMeta(t, sw, id)
		query := path + "?tenant=" + sw.tenantB.tenantID
		if name == "governance-decisions" {
			query += "&agent_id=platform-b-agent"
		} else if name == "approval-analytics" {
			query += "&group_by=rule"
		}
		rr := sw.as(t, sw.tenantA, http.MethodGet, query, "")
		if name == "governance-health" {
			assertCrossTenantRefused(t, rr, name, marker)
		} else {
			assertCrossTenantAbsent(t, rr, name, marker, id, sw.tenantB.tenantID)
		}
		if !reflect.DeepEqual(before, crossTenantPlatformDecisions(t, sw)) || !reflect.DeepEqual(meta, crossTenantJobMeta(t, sw, id)) {
			t.Error("governance read changed B records")
		}
		owner := sw.as(t, sw.tenantB, http.MethodGet, query, "")
		assertCrossTenantAbsent(t, owner, "B governance control")
		if name != "governance-health" && !strings.Contains(owner.Body.String(), marker) {
			t.Error("B governance control missing seeded decision")
		}
	}}
}

func crossTenantPlatformDecisions(t *testing.T, sw *crossTenantSweep) model.DecisionPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	page, err := sw.server.decisionLogStore.QueryDecisions(ctx, model.DecisionQuery{Tenant: sw.tenantB.tenantID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("B decision snapshot missing: %v (%d items)", err, len(page.Items))
	}
	return page
}
