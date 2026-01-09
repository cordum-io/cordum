package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHandleStatusAndWorkers(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.workerMu.Lock()
	s.workers["w1"] = &pb.Heartbeat{WorkerId: "w1"}
	s.workerMu.Unlock()

	workersReq := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)
	workersRec := httptest.NewRecorder()
	s.handleGetWorkers(workersRec, workersReq)
	if workersRec.Code != http.StatusOK {
		t.Fatalf("unexpected workers status: %d", workersRec.Code)
	}
	var workers []*pb.Heartbeat
	if err := json.NewDecoder(workersRec.Body).Decode(&workers); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(workers) != 1 || workers[0].WorkerId != "w1" {
		t.Fatalf("unexpected workers list")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	statusRec := httptest.NewRecorder()
	s.handleStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusRec.Code)
	}
	var status map[string]any
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	workersInfo, ok := status["workers"].(map[string]any)
	if !ok || workersInfo["count"].(float64) != 1 {
		t.Fatalf("unexpected workers count in status")
	}
}
