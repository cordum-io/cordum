package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestPolicyHandlers(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-alpha"})
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:         "ok",
		PolicySnapshot: "snap-alpha",
	})

	payload := map[string]any{
		"topic":  "job.default",
		"tenant": "default",
	}
	body, _ := json.Marshal(payload)
	evalReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", bytes.NewReader(body))
	evalRR := httptest.NewRecorder()
	s.handlePolicyEvaluate(evalRR, evalReq)
	if evalRR.Code != http.StatusOK {
		t.Fatalf("policy evaluate: %d %s", evalRR.Code, evalRR.Body.String())
	}

	simReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/simulate", bytes.NewReader(body))
	simRR := httptest.NewRecorder()
	s.handlePolicySimulate(simRR, simReq)
	if simRR.Code != http.StatusOK {
		t.Fatalf("policy simulate: %d %s", simRR.Code, simRR.Body.String())
	}

	expReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/explain", bytes.NewReader(body))
	expRR := httptest.NewRecorder()
	s.handlePolicyExplain(expRR, expReq)
	if expRR.Code != http.StatusOK {
		t.Fatalf("policy explain: %d %s", expRR.Code, expRR.Body.String())
	}

	snapReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/snapshots", nil)
	snapRR := httptest.NewRecorder()
	s.handlePolicySnapshots(snapRR, snapReq)
	if snapRR.Code != http.StatusOK {
		t.Fatalf("policy snapshots: %d %s", snapRR.Code, snapRR.Body.String())
	}
}
