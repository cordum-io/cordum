package gateway

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
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
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyRead, "admin", "operator") {
		return
	}
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
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	if s.safetyClient == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "safety kernel unavailable")
		return
	}
	var req policyCheckRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
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
		writeForbidden(w, r, err)
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

	// Action-layer gates run BEFORE the safety-kernel call. The pipeline
	// short-circuits with an HTTP error envelope (or a 200 informational
	// shape for simulate REQUIRE_HUMAN) so the caller learns the exact
	// gate Code via HTTP status. Callers that send no Action skip this
	// path; legacy behavior is unchanged.
	if s.actionGatePipeline != nil && req.Action != nil {
		input := &config.PolicyInput{
			Tenant: tenant,
			Topic:  req.Topic,
			Labels: req.Labels,
			Meta:   config.PolicyMeta{ActorID: req.PrincipalId},
			Action: req.Action,
		}
		if gateDec, fired := s.actionGatePipeline.Run(r.Context(), input); fired {
			s.emitPolicyActionGateAudit(r.Context(), tenant, principalID, mode, &req, gateDec)
			writeActionGatePolicyError(w, r, mode, gateDec)
			return
		}
		checkReq = stripGatewayForwardedActionDescriptor(checkReq)
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

	// Audit: log every policy dry-run call for enumeration detection.
	s.appendAuditEntryNamed(r.Context(), "policy."+mode, "policy", req.Topic,
		req.Topic, policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		"policy "+mode+" by "+policybundles.PolicyActorID(r))

	// Defense in depth: redact rule internals when configured.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CORDUM_POLICY_EVALUATE_REDACT")), "true") && resp != nil {
		resp.RuleId = "matched"
		resp.Reason = resp.GetDecision().String()
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
