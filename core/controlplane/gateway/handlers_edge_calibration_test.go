package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/calibration"
)

const edgeCalibrationPath = "/api/v1/edge/calibration-reports"

const edgeCalibrationBundle = `rules:
  - id: deny-rm
    match:
      topics:
        - job.*
    decision: deny
  - id: approve-push
    match:
      topics:
        - job.*
    decision: deny
  - id: quiet-rule
    match:
      topics:
        - job.*
    decision: deny
`

// newEdgeCalibrationFixture seeds one policy bundle and one observe-mode
// session of edgeRouteTenant with two DENY events of deny-rm, one
// REQUIRE_APPROVAL event of approve-push and one ALLOW event.
func newEdgeCalibrationFixture(t *testing.T) (*server, http.Handler, time.Time) {
	t.Helper()
	s, h := newEdgeRouteTestServer(t)
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID,
		Data: map[string]any{packs.PolicyConfigKey: map[string]any{
			"calibration/test": map[string]any{"content": edgeCalibrationBundle, "enabled": true},
		}},
	}); err != nil {
		t.Fatalf("seed policy bundle: %v", err)
	}
	sess := createEdgeRouteSession(t, h)
	base := time.Now().UTC().Truncate(time.Second)
	for i, ev := range []struct {
		decision edgecore.EdgeDecision
		rule     string
	}{
		{edgecore.DecisionDeny, "deny-rm"},
		{edgecore.DecisionDeny, "deny-rm"},
		{edgecore.DecisionRequireApproval, "approve-push"},
		{edgecore.DecisionAllow, ""},
	} {
		if _, err := s.edgeStore.AppendEvent(ctx, edgecore.AgentActionEvent{
			EventID: fmt.Sprintf("evt-cal-%d", i), SessionID: sess.SessionID, ExecutionID: sess.ExecutionID,
			TenantID: edgeRouteTenant, PrincipalID: "principal-edge-a", Timestamp: base.Add(time.Duration(i+1) * time.Second),
			Layer: edgecore.LayerHook, Kind: edgecore.EventKindHookPolicyDecision, AgentProduct: "claude-code",
			ToolName: "Bash", ActionName: "bash", Decision: ev.decision, RuleID: ev.rule, Status: edgecore.ActionStatusOK,
		}); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}
	return s, h, base
}

func edgeCalibrationWindow(since, until time.Time) string {
	return fmt.Sprintf(`{"since":%q,"until":%q}`, since.Format(time.RFC3339), until.Format(time.RFC3339))
}

func postEdgeCalibration(t *testing.T, h http.Handler, apiKey, tenantID, body string) (int, string) {
	t.Helper()
	rr := edgeRoutePOSTAsTenantWithIdempotencyKey(t, h, apiKey, tenantID, edgeCalibrationPath, body, "")
	return rr.Code, rr.Body.String()
}

func decodeEdgeCalibrationReport(t *testing.T, body string) calibration.Report {
	t.Helper()
	var rep calibration.Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("decode calibration report %q: %v", body, err)
	}
	return rep
}

func TestEdgeCalibrationReportCreateAndGet(t *testing.T) {
	_, h, base := newEdgeCalibrationFixture(t)
	code, body := postEdgeCalibration(t, h, edgeRouteTestAPIKey, edgeRouteTenant, edgeCalibrationWindow(base.Add(-time.Hour), base.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 body=%s", code, body)
	}
	rep := decodeEdgeCalibrationReport(t, body)
	if !regexp.MustCompile(`^cal-[0-9a-f]{32}$`).MatchString(rep.ID) {
		t.Fatalf("report id = %q, want cal-<32 hex>", rep.ID)
	}
	wantRules := []calibration.RuleCount{{RuleID: "approve-push", RequireApproval: 1}, {RuleID: "deny-rm", Deny: 2}}
	if rep.TenantID != edgeRouteTenant || rep.SessionsObserved != 1 || rep.EventsScanned != 4 ||
		!reflect.DeepEqual(rep.Rules, wantRules) || !reflect.DeepEqual(rep.NeverFired, []string{"quiet-rule"}) {
		t.Fatalf("report = tenant %q sessions %d events %d rules %+v never_fired %v, want %q 1 4 %+v [quiet-rule]",
			rep.TenantID, rep.SessionsObserved, rep.EventsScanned, rep.Rules, rep.NeverFired, edgeRouteTenant, wantRules)
	}
	get := edgeApprovalRouteGETAs(t, h, edgeRouteViewerAPIKey, edgeRouteTenant, edgeCalibrationPath+"/"+rep.ID)
	if get.Code != http.StatusOK || get.Body.String() != body {
		t.Fatalf("viewer GET = %d %s, want 200 with the POST body %s", get.Code, get.Body.String(), body)
	}
}

func TestEdgeCalibrationReportOtherTenantNotFound(t *testing.T) {
	_, h, base := newEdgeCalibrationFixture(t)
	window := edgeCalibrationWindow(base.Add(-time.Hour), base.Add(time.Hour))
	code, body := postEdgeCalibration(t, h, edgeRouteTestAPIKey, edgeRouteTenant, window)
	if code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 body=%s", code, body)
	}
	rep := decodeEdgeCalibrationReport(t, body)
	get := edgeApprovalRouteGETAs(t, h, edgeRouteOtherAPIKey, edgeRouteOtherTenant, edgeCalibrationPath+"/"+rep.ID)
	if get.Code != http.StatusNotFound {
		t.Fatalf("other-tenant GET status = %d, want 404 body=%s", get.Code, get.Body.String())
	}
	assertBodyOmits(t, get.Body.String(), "deny-rm", "approve-push", "quiet-rule")

	code, body = postEdgeCalibration(t, h, edgeRouteOtherAPIKey, edgeRouteOtherTenant, window)
	if code != http.StatusCreated {
		t.Fatalf("other-tenant POST status = %d, want 201 body=%s", code, body)
	}
	other := decodeEdgeCalibrationReport(t, body)
	if other.SessionsObserved != 0 || other.EventsScanned != 0 || len(other.Rules) != 0 {
		t.Fatalf("other-tenant report counted tenant A data: sessions %d events %d rules %+v",
			other.SessionsObserved, other.EventsScanned, other.Rules)
	}
}

func TestEdgeCalibrationReportRejectsWithoutWrite(t *testing.T) {
	s, h, base := newEdgeCalibrationFixture(t)
	for _, tc := range []struct {
		name, apiKey, body string
		want               int
	}{
		{"viewer", edgeRouteViewerAPIKey, edgeCalibrationWindow(base.Add(-time.Hour), base.Add(time.Hour)), http.StatusForbidden},
		{"inverted window", edgeRouteTestAPIKey, edgeCalibrationWindow(base.Add(time.Hour), base.Add(-time.Hour)), http.StatusBadRequest},
		{"91-day window", edgeRouteTestAPIKey, edgeCalibrationWindow(base.Add(-91*24*time.Hour), base), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := postEdgeCalibration(t, h, tc.apiKey, edgeRouteTenant, tc.body)
			if code != tc.want {
				t.Fatalf("POST status = %d, want %d body=%s", code, tc.want, body)
			}
			keys, err := s.redisClient().Keys(context.Background(), edgeCalibrationReportKeyPrefix+"*").Result()
			if err != nil || len(keys) != 0 {
				t.Fatalf("stored report keys = %v (err %v), want none", keys, err)
			}
		})
	}
}
