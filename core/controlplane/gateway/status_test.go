package gateway

import (
	"context"
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

	s.auth = stubLicenseAuth{info: &LicenseInfo{Mode: "enterprise", Status: "active", Plan: "Enterprise"}}

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

	licenseInfo, ok := status["license"].(map[string]any)
	if !ok {
		t.Fatalf("expected license info in status")
	}
	if mode, ok := licenseInfo["mode"].(string); !ok || mode != "enterprise" {
		t.Fatalf("unexpected license mode: %#v", licenseInfo["mode"])
	}
}

type stubLicenseAuth struct {
	info *LicenseInfo
}

func (s stubLicenseAuth) AuthenticateHTTP(*http.Request) (*AuthContext, error) { return &AuthContext{}, nil }

func (s stubLicenseAuth) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return &AuthContext{}, nil
}

func (s stubLicenseAuth) RequireRole(*http.Request, ...string) error { return nil }

func (s stubLicenseAuth) ResolveTenant(_ *http.Request, requested, _ string) (string, error) {
	return requested, nil
}

func (s stubLicenseAuth) RequireTenantAccess(*http.Request, string) error { return nil }

func (s stubLicenseAuth) ResolvePrincipal(_ *http.Request, requested string) (string, error) {
	return requested, nil
}

func (s stubLicenseAuth) LicenseInfo() *LicenseInfo { return s.info }
