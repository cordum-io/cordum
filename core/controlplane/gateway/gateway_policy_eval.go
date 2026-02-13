package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func (s *server) handlePolicyEvaluate(w http.ResponseWriter, r *http.Request) {
	s.handlePolicyCheck(w, r, "evaluate")
}

func (s *server) handlePolicySimulate(w http.ResponseWriter, r *http.Request) {
	s.handlePolicyCheck(w, r, "simulate")
}

func (s *server) handlePolicyExplain(w http.ResponseWriter, r *http.Request) {
	s.handlePolicyCheck(w, r, "explain")
}

func (s *server) handlePolicySnapshots(w http.ResponseWriter, r *http.Request) {
	if s.safetyClient == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "safety kernel unavailable")
		return
	}
	resp, err := s.safetyClient.ListSnapshots(r.Context(), &pb.ListSnapshotsRequest{})
	if err != nil {
		slog.Error("safety kernel list snapshots failed", "error", err)
		writeErrorJSON(w, http.StatusBadGateway, "upstream service error")
		return
	}
	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec -- JSON response; content-type is set to application/json.
	_, _ = w.Write(data)
}

func (s *server) handlePolicyCheck(w http.ResponseWriter, r *http.Request, mode string) {
	if s.safetyClient == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "safety kernel unavailable")
		return
	}
	var req policyCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	tenant, err := s.resolveTenant(r, req.Tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	req.Tenant = tenant
	req.OrgId = tenant
	principalID, err := s.resolvePrincipal(r, req.PrincipalId)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	req.PrincipalId = principalID
	if req.Meta != nil {
		req.Meta.TenantId = tenant
	}
	checkReq, err := buildPolicyCheckRequest(r.Context(), &req, s.configSvc, s.tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	var resp *pb.PolicyCheckResponse
	switch mode {
	case "simulate":
		resp, err = s.safetyClient.Simulate(r.Context(), checkReq)
	case "explain":
		resp, err = s.safetyClient.Explain(r.Context(), checkReq)
	default:
		resp, err = s.safetyClient.Evaluate(r.Context(), checkReq)
	}
	if err != nil {
		slog.Error("safety kernel policy check failed", "error", err, "mode", mode)
		writeErrorJSON(w, http.StatusBadGateway, "upstream service error")
		return
	}

	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec -- JSON response; content-type is set to application/json.
	_, _ = w.Write(data)
}
