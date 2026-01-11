package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const policyContent = `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`

func TestPolicyBundleHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	body, _ := json.Marshal(map[string]any{
		"content": policyContent,
		"enabled": true,
		"author":  "tester",
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", bytes.NewReader(body))
	putReq.SetPathValue("id", "secops/test")
	putReq.Header.Set("X-Principal-Role", "admin")
	putRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put policy bundle: %d %s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/secops/test", nil)
	getReq.SetPathValue("id", "secops/test")
	getReq.Header.Set("X-Principal-Role", "admin")
	getRec := httptest.NewRecorder()
	s.handleGetPolicyBundle(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get policy bundle: %d %s", getRec.Code, getRec.Body.String())
	}
	var detail map[string]any
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode bundle detail: %v", err)
	}
	if detail["id"] != "secops/test" {
		t.Fatalf("unexpected bundle id")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles", nil)
	listReq.Header.Set("X-Principal-Role", "admin")
	listRec := httptest.NewRecorder()
	s.handlePolicyBundles(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list policy bundles: %d %s", listRec.Code, listRec.Body.String())
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/rules", nil)
	rulesReq.Header.Set("X-Principal-Role", "admin")
	rulesRec := httptest.NewRecorder()
	s.handlePolicyRules(rulesRec, rulesReq)
	if rulesRec.Code != http.StatusOK {
		t.Fatalf("policy rules: %d %s", rulesRec.Code, rulesRec.Body.String())
	}

	simBody, _ := json.Marshal(map[string]any{
		"request": map[string]any{
			"topic":  "job.test",
			"tenant": "default",
		},
	})
	simReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/secops/test/simulate", bytes.NewReader(simBody))
	simReq.SetPathValue("id", "secops/test")
	simReq.Header.Set("X-Principal-Role", "admin")
	simRec := httptest.NewRecorder()
	s.handleSimulatePolicyBundle(simRec, simReq)
	if simRec.Code != http.StatusOK {
		t.Fatalf("simulate bundle: %d %s", simRec.Code, simRec.Body.String())
	}
	var resp pb.PolicyCheckResponse
	if err := protojson.Unmarshal(simRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode simulate response: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("unexpected decision: %v", resp.GetDecision())
	}
}

func TestPolicyBundlePublishRollbackAndAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)

	seed, _ := json.Marshal(map[string]any{
		"content": policyContent,
		"enabled": false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", bytes.NewReader(seed))
	putReq.SetPathValue("id", "secops/test")
	putReq.Header.Set("X-Principal-Role", "admin")
	putReq.Header.Set("X-Principal-Id", "user1")
	putRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("seed bundle: %d %s", putRec.Code, putRec.Body.String())
	}

	pubReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/publish", bytes.NewReader([]byte(`{}`)))
	pubReq.Header.Set("X-Principal-Role", "admin")
	pubReq.Header.Set("X-Principal-Id", "user1")
	pubRec := httptest.NewRecorder()
	s.handlePublishPolicyBundles(pubRec, pubReq)
	if pubRec.Code != http.StatusOK {
		t.Fatalf("publish bundles: %d %s", pubRec.Code, pubRec.Body.String())
	}
	var publishResp map[string]any
	if err := json.NewDecoder(pubRec.Body).Decode(&publishResp); err != nil {
		t.Fatalf("decode publish: %v", err)
	}
	rollbackID, _ := publishResp["snapshot_after"].(string)
	if rollbackID == "" {
		t.Fatalf("expected snapshot_after id")
	}

	snapReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/snapshots", nil)
	snapReq.Header.Set("X-Principal-Role", "admin")
	snapRec := httptest.NewRecorder()
	s.handleListPolicyBundleSnapshots(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("list snapshots: %d %s", snapRec.Code, snapRec.Body.String())
	}

	rbBody, _ := json.Marshal(map[string]any{"snapshot_id": rollbackID})
	rbReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/rollback", bytes.NewReader(rbBody))
	rbReq.Header.Set("X-Principal-Role", "admin")
	rbReq.Header.Set("X-Principal-Id", "user1")
	rbRec := httptest.NewRecorder()
	s.handleRollbackPolicyBundles(rbRec, rbReq)
	if rbRec.Code != http.StatusOK {
		t.Fatalf("rollback bundles: %d %s", rbRec.Code, rbRec.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit", nil)
	auditReq.Header.Set("X-Principal-Role", "admin")
	auditRec := httptest.NewRecorder()
	s.handleListPolicyAudit(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit list: %d %s", auditRec.Code, auditRec.Body.String())
	}
	var auditResp map[string]any
	if err := json.NewDecoder(auditRec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	items, _ := auditResp["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected audit entries")
	}
}
