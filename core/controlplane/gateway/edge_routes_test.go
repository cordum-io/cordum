package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	edgeRouteTestAPIKey     = "edge-route-test-key"
	edgeRouteReviewerAPIKey = "edge-route-reviewer-key"
	edgeRouteViewerAPIKey   = "edge-route-viewer-key"
	edgeRouteUserAPIKey     = "edge-route-user-key"
	edgeRouteTenant         = "tenant-edge-a"
	edgeRouteOtherAPIKey    = "edge-route-other-key"
	edgeRouteOtherTenant    = "tenant-edge-b"
)

type edgeRouteExpectation struct {
	method string
	path   string
}

// EDGE-028 backend integration coverage inventory (existing tests reused):
// A. E2E pieces: edge_routes_test.go:TestGatewayEdgeSessionLifecycleResponseContract,
//
//	edge_routes_test.go:TestGatewayEdgeExecutionLifecycleResponseContract,
//	edge_evaluate_test.go:TestGatewayEdgeEvaluateMapsSafetyDecisionsToHookResponse,
//	edge_evaluate_test.go:TestGatewayEdgeEvaluateRetryConsumesApprovedApprovalAndDeniesDuplicate,
//	edge_events_test.go:TestGatewayEdgeEventSingleWriteStreamsPersistedEdgeEnvelope,
//	handlers_edge_errors_test.go:TestGatewayEdgeExportEmitsAuditEventForSuccessAndMissing,
//	core/edge/export_test.go:TestSessionExportAssemblerHappyPathContainsAllSessionEvidence.
//	Gap filled below: one gateway sequence creates session+execution, evaluates ALLOW,
//	writes/streams an event, requires approval, approves, consumes once, then exports.
//
// B. Auth/tenant/cross-tenant: edge_routes_test.go, edge_events_test.go,
//
//	edge_events_idempotency_test.go, edge_evaluate_test.go,
//	handlers_edge_approvals_test.go, and edge_stream_test.go cover 6/7 gateway
//	edge test files; export-specific gateway auth gaps are filled later.
//
// C. Bounds/malformed: session body limit, events/evaluate malformed and oversize
//
//	inline payloads, idempotency-key limits, and store oversize prevalidation exist.
//	EdgeMaxNestingDepth is absent on HEAD, so no nested-depth assertion is added.
//
// D. Redaction/export: core redaction/model/artifact tests plus gateway session,
//
//	execution, event, evaluate, error, and export tests prove unit/path coverage;
//	full synthetic-secret round-trip is filled later.
//
// E. Safety Kernel unavailable/unknown: edge_evaluate_test.go and
//
//	core/edge/agentd/fail_modes_test.go cover current fail-mode slices; explicit
//	gateway timeout/connection/malformed/future-enum cases are filled later.
//
// F. Redis unavailable: nil-store/fake-append gateway paths exist; miniredis
//
//	Close()/SetError() store simulations are filled later.
//
// G. Stream: edge_stream_test.go plus event/evaluate stream tests cover in-memory
//
//	forwarding; subscribe/resume/heartbeat/cancel regressions are filled later.
//
// H. Approval: approval_store_redis_test.go, handlers_edge_approvals_test.go, and
//
//	edge_evaluate_test.go cover consume-once, rejected/expired/stale/self approval;
//	export-after-approval is asserted below.
func TestGatewayEdgeEndToEndEvaluateApprovalStreamAndExport(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:         "safe read-only command",
		RuleId:         "edge028.allow",
		PolicySnapshot: "snap-edge028-allow",
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}

	session := createEdgeRouteSession(t, handler)
	execution := createEdgeRouteExecution(t, handler, session.SessionID)
	if execution.SessionID != session.SessionID {
		t.Fatalf("created execution session_id = %q, want %q", execution.SessionID, session.SessionID)
	}
	if execution.TenantID != edgeRouteTenant {
		t.Fatalf("created execution tenant_id = %q, want %q", execution.TenantID, edgeRouteTenant)
	}

	allowRR := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test ./core/edge"}))
	if allowRR.Code != http.StatusOK {
		t.Fatalf("allow evaluate status = %d, want 200 body=%s", allowRR.Code, allowRR.Body.String())
	}
	var allowResp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, allowRR, &allowResp)
	if allowResp.Decision != string(edgecore.DecisionAllow) ||
		allowResp.PermissionDecision != "allow" ||
		allowResp.RuleID != "edge028.allow" ||
		allowResp.PolicySnapshot != "snap-edge028-allow" {
		t.Fatalf("allow response = decision:%q permission:%q rule:%q snapshot:%q body=%s",
			allowResp.Decision, allowResp.PermissionDecision, allowResp.RuleID, allowResp.PolicySnapshot, allowRR.Body.String())
	}
	if strings.TrimSpace(allowResp.EventID) == "" {
		t.Fatalf("allow response missing event_id body=%s", allowRR.Body.String())
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "allow evaluate event"), allowResp.EventID, edgecore.DecisionAllow, edgecore.EventKindHookPolicyDecision)

	manualEventID := "evt-edge028-e2e-tool"
	artifactURI := "artifact://edge/evt-edge028-e2e-tool/tool-input"
	manualTimestamp := time.Now().UTC().Format(time.RFC3339Nano)
	eventRR := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"`+manualEventID+`",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+execution.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"`+manualTimestamp+`",
		"layer":"hook",
		"kind":"hook.post_tool_use",
		"tool_name":"Bash",
		"input_redacted":{"summary":"test run completed"},
		"artifact_ptrs":[{
			"artifact_type":"edge.tool_input",
			"session_id":"`+session.SessionID+`",
			"execution_id":"`+execution.ExecutionID+`",
			"event_id":"`+manualEventID+`",
			"tenant_id":"`+edgeRouteTenant+`",
			"retention_class":"short",
			"redaction_level":"standard",
			"sha256":"sha256:edge028artifact",
			"uri":"`+artifactURI+`",
			"created_at":"`+manualTimestamp+`"
		}],
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if eventRR.Code != http.StatusCreated {
		t.Fatalf("manual event write status = %d, want 201 body=%s", eventRR.Code, eventRR.Body.String())
	}
	var manualEvent edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, eventRR, &manualEvent)
	if manualEvent.EventID != manualEventID || manualEvent.Seq != 2 || len(manualEvent.ArtifactPointers) != 1 || manualEvent.ArtifactPointers[0].URI != artifactURI {
		t.Fatalf("manual event = id:%q seq:%d artifacts:%#v, want %q seq=2 one artifact %q",
			manualEvent.EventID, manualEvent.Seq, manualEvent.ArtifactPointers, manualEventID, artifactURI)
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "manual event write"), manualEventID, edgecore.DecisionAllow, edgecore.EventKindHookPostToolUse)

	safety.mu.Lock()
	safety.response = &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "production command requires approval",
		RuleId:           "edge028.require-approval",
		PolicySnapshot:   session.PolicySnapshot,
		ApprovalRequired: true,
	}
	safety.mu.Unlock()
	approvalCommand := map[string]any{"command": "rm -rf /tmp/edge028-e2e"}
	approvalRR := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate",
		edgeEvaluateBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "Bash", approvalCommand))
	if approvalRR.Code != http.StatusOK {
		t.Fatalf("approval evaluate status = %d, want 200 body=%s", approvalRR.Code, approvalRR.Body.String())
	}
	var approvalResp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, approvalRR, &approvalResp)
	if approvalResp.Decision != string(edgecore.DecisionRequireApproval) ||
		approvalResp.PermissionDecision != "deny" ||
		approvalResp.RuleID != "edge028.require-approval" ||
		approvalResp.PolicySnapshot != session.PolicySnapshot ||
		!strings.HasPrefix(approvalResp.ApprovalRef, edgecore.ApprovalRefPrefix) {
		t.Fatalf("approval response = decision:%q permission:%q rule:%q snapshot:%q ref:%q body=%s",
			approvalResp.Decision, approvalResp.PermissionDecision, approvalResp.RuleID, approvalResp.PolicySnapshot, approvalResp.ApprovalRef, approvalRR.Body.String())
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "approval required event"), approvalResp.EventID, edgecore.DecisionRequireApproval, edgecore.EventKindHookPolicyDecision)

	detail := edgeApprovalRouteGETAs(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, "/api/v1/edge/approvals/"+approvalResp.ApprovalRef)
	if detail.Code != http.StatusOK {
		t.Fatalf("approval detail status = %d, want 200 body=%s", detail.Code, detail.Body.String())
	}
	var pending edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, detail, &pending)
	if pending.Status != edgecore.ApprovalStatusPending ||
		pending.EventID != approvalResp.EventID ||
		pending.ActionHash != approvalResp.ActionHash ||
		pending.PolicySnapshot != session.PolicySnapshot {
		t.Fatalf("pending approval = status:%q event:%q action:%q snapshot:%q, want pending/%q/%q/%q",
			pending.Status, pending.EventID, pending.ActionHash, pending.PolicySnapshot, approvalResp.EventID, approvalResp.ActionHash, session.PolicySnapshot)
	}

	approve := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approvalResp.ApprovalRef+"/approve", `{"reason":"approved for e2e retry"}`)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200 body=%s", approve.Code, approve.Body.String())
	}
	var approved edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, approve, &approved)
	if approved.Status != edgecore.ApprovalStatusApproved ||
		approved.Decision != edgecore.ApprovalDecisionApprove ||
		approved.ResolutionReason != "approved for e2e retry" ||
		approved.ResolvedAt == nil {
		t.Fatalf("approved record = status:%q decision:%q reason:%q resolved:%v",
			approved.Status, approved.Decision, approved.ResolutionReason, approved.ResolvedAt)
	}

	retryBody := edgeEvaluateBodyWithApprovalRef(session.SessionID, execution.ExecutionID, edgeRouteTenant, "Bash", approvalCommand, approvalResp.ApprovalRef)
	retryRR := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if retryRR.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 body=%s", retryRR.Code, retryRR.Body.String())
	}
	var retryResp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, retryRR, &retryResp)
	if retryResp.Decision != string(edgecore.DecisionAllow) ||
		retryResp.PermissionDecision != "allow" ||
		retryResp.ApprovalRef != approvalResp.ApprovalRef ||
		retryResp.WaitAfter != "" ||
		retryResp.ActionHash != approvalResp.ActionHash ||
		retryResp.InputHash != approvalResp.InputHash {
		t.Fatalf("retry response = decision:%q permission:%q ref:%q wait_after:%q action:%q input:%q, want allow consumed ref/action/input",
			retryResp.Decision, retryResp.PermissionDecision, retryResp.ApprovalRef, retryResp.WaitAfter, retryResp.ActionHash, retryResp.InputHash)
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "approval retry allow event"), retryResp.EventID, edgecore.DecisionAllow, edgecore.EventKindHookPolicyDecision)

	duplicateRR := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", retryBody)
	if duplicateRR.Code != http.StatusOK {
		t.Fatalf("duplicate retry status = %d, want 200 body=%s", duplicateRR.Code, duplicateRR.Body.String())
	}
	var duplicateResp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, duplicateRR, &duplicateResp)
	if duplicateResp.Decision != string(edgecore.DecisionDeny) ||
		duplicateResp.PermissionDecision != "deny" ||
		duplicateResp.ApprovalRef != approvalResp.ApprovalRef ||
		duplicateResp.WaitAfter != "request_new_approval" {
		t.Fatalf("duplicate retry response = decision:%q permission:%q ref:%q wait_after:%q, want deny consumed ref/new approval",
			duplicateResp.Decision, duplicateResp.PermissionDecision, duplicateResp.ApprovalRef, duplicateResp.WaitAfter)
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "duplicate retry deny event"), duplicateResp.EventID, edgecore.DecisionDeny, edgecore.EventKindHookPolicyDecision)

	eventsRR := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/events?limit=10")
	if eventsRR.Code != http.StatusOK {
		t.Fatalf("session events status = %d, want 200 body=%s", eventsRR.Code, eventsRR.Body.String())
	}
	var eventsPage edgeEventPageResponseJSON
	decodeEdgeRouteJSON(t, eventsRR, &eventsPage)
	assertEdgeEventIDs(t, eventsPage.Items, []string{allowResp.EventID, manualEventID, approvalResp.EventID, retryResp.EventID, duplicateResp.EventID})
	if eventsPage.Items[0].Decision != edgecore.DecisionAllow ||
		eventsPage.Items[2].Decision != edgecore.DecisionRequireApproval ||
		eventsPage.Items[3].Decision != edgecore.DecisionAllow ||
		eventsPage.Items[4].Decision != edgecore.DecisionDeny {
		t.Fatalf("event decisions = [%q,%q,%q,%q,%q], want ALLOW,ALLOW,REQUIRE_APPROVAL,ALLOW,DENY",
			eventsPage.Items[0].Decision, eventsPage.Items[1].Decision, eventsPage.Items[2].Decision, eventsPage.Items[3].Decision, eventsPage.Items[4].Decision)
	}

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, approvalResp.ApprovalRef)
	if err != nil || !ok || stored == nil || stored.ConsumedAt == nil {
		t.Fatalf("stored approval after retry = (%#v,%v,%v), want consumed approval", stored, ok, err)
	}
	if stored.Status != edgecore.ApprovalStatusApproved ||
		stored.Decision != edgecore.ApprovalDecisionApprove ||
		stored.EventID != approvalResp.EventID ||
		stored.ResolverID != approved.ResolverID ||
		stored.ResolvedBy != approved.ResolvedBy ||
		stored.ResolvedAt == nil ||
		stored.ResolutionReason != "approved for e2e retry" {
		t.Fatalf("stored approval evidence = status:%q decision:%q event:%q resolver:%q by:%q resolved:%v reason:%q",
			stored.Status, stored.Decision, stored.EventID, stored.ResolverID, stored.ResolvedBy, stored.ResolvedAt, stored.ResolutionReason)
	}

	// Use local/dev export behavior with no artifact-store reader: the bundle
	// must still include metadata-only missing-artifact manifests and must never
	// inline or echo tool payload literals.
	s.artifactStore = nil
	exportRR := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":10}`)
	if exportRR.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200 body=%s", exportRR.Code, exportRR.Body.String())
	}
	var bundle edgecore.SessionExportBundle
	decodeEdgeRouteJSON(t, exportRR, &bundle)
	if bundle.ManifestVersion != edgecore.ExportManifestVersion ||
		bundle.TenantID != edgeRouteTenant ||
		bundle.Session.SessionID != session.SessionID ||
		len(bundle.Events) != 5 ||
		len(bundle.Approvals) != 1 {
		t.Fatalf("export bundle = manifest:%q tenant:%q session:%q events:%d approvals:%d firstApproval:%#v",
			bundle.ManifestVersion, bundle.TenantID, bundle.Session.SessionID, len(bundle.Events), len(bundle.Approvals), bundle.Approvals)
	}
	exportedApproval := bundle.Approvals[0]
	if exportedApproval.ApprovalRef != approvalResp.ApprovalRef ||
		exportedApproval.EventID != approvalResp.EventID ||
		exportedApproval.Status != edgecore.ApprovalStatusApproved ||
		exportedApproval.Decision != edgecore.ApprovalDecisionApprove ||
		exportedApproval.PrincipalID != "principal-edge-a" ||
		exportedApproval.ResolverID != "principal-reviewer" ||
		!strings.Contains(exportedApproval.ResolvedBy, "principal:principal-reviewer") ||
		exportedApproval.ResolutionReason != "approved for e2e retry" ||
		exportedApproval.ActionHash != approvalResp.ActionHash ||
		exportedApproval.InputHash != approvalResp.InputHash ||
		exportedApproval.CreatedAt.IsZero() ||
		exportedApproval.ResolvedAt == nil ||
		exportedApproval.ConsumedAt == nil {
		t.Fatalf("exported approval = %#v, want issue+approve+consume evidence with requester, resolver, and timestamps", exportedApproval)
	}
	assertEdgeEventIDs(t, bundle.Events, []string{allowResp.EventID, manualEventID, approvalResp.EventID, retryResp.EventID, duplicateResp.EventID})
	exportApprovalEvent := findEdgeEventByID(t, bundle.Events, approvalResp.EventID)
	if exportApprovalEvent.Decision != edgecore.DecisionRequireApproval ||
		exportApprovalEvent.PolicySnapshot != session.PolicySnapshot {
		t.Fatalf("export approval event evidence = decision:%q snapshot:%q, want REQUIRE_APPROVAL/%q",
			exportApprovalEvent.Decision, exportApprovalEvent.PolicySnapshot, session.PolicySnapshot)
	}
	if len(bundle.MissingArtifacts) != 1 ||
		bundle.MissingArtifacts[0].URI != artifactURI ||
		bundle.MissingArtifacts[0].EventID != manualEventID ||
		bundle.MissingArtifacts[0].Reason != edgecore.MissingArtifactReasonNotFound {
		t.Fatalf("missing artifacts = %#v, want one not_found manifest entry for %q", bundle.MissingArtifacts, artifactURI)
	}
}

func TestGatewayEdgeRedactionRoundTripAcrossEventsApprovalsAndExport(t *testing.T) {
	syntheticSecrets := []string{
		"sk-test-fake-secret-xyz",
		"ghp_FAKETOKEN0000",
		"Bearer fake.jwt.value",
		"aws-access-key-fake/AKIAFAKEONLY",
	}
	secretCommand := strings.Join([]string{
		"curl https://example.invalid",
		"--header Authorization: " + syntheticSecrets[2],
		"--data api_key=" + syntheticSecrets[0],
		"--data github_token=" + syntheticSecrets[1],
		"AWS_ACCESS_KEY_ID=" + syntheticSecrets[3],
	}, " ")

	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "approval required for " + strings.Join(syntheticSecrets, " "),
		RuleId:           "edge028.redaction.require-approval",
		PolicySnapshot:   "",
		ApprovalRequired: true,
	}}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.mu.Lock()
	safety.response.PolicySnapshot = session.PolicySnapshot
	safety.mu.Unlock()
	execution := createEdgeRouteExecution(t, handler, session.SessionID)

	artifactEventID := "evt-edge028-redaction-artifact"
	artifactURI := "artifact://edge/redaction/tool-input"
	artifactSHA := "sha256:edge028redactionartifact"
	artifactTimestamp := time.Now().UTC().Format(time.RFC3339Nano)
	manual := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"`+artifactEventID+`",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+execution.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"`+artifactTimestamp+`",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"tool_name":"Bash",
		"input_redacted":{
			"command":"`+secretCommand+`",
			"token":"`+syntheticSecrets[1]+`",
			"aws_access_key_id":"`+syntheticSecrets[3]+`"
		},
		"artifact_ptrs":[{
			"artifact_type":"edge.tool_input",
			"session_id":"`+session.SessionID+`",
			"execution_id":"`+execution.ExecutionID+`",
			"event_id":"`+artifactEventID+`",
			"tenant_id":"`+edgeRouteTenant+`",
			"retention_class":"short",
			"redaction_level":"standard",
			"sha256":"`+artifactSHA+`",
			"uri":"`+artifactURI+`",
			"created_at":"`+artifactTimestamp+`"
		}],
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if manual.Code != http.StatusCreated {
		t.Fatalf("manual redaction event status = %d, want 201 body=%s", manual.Code, manual.Body.String())
	}
	mustNotContain(t, manual.Body.String(), syntheticSecrets...)
	if !bodyHasRedactionMarker(manual.Body.String()) || !strings.Contains(manual.Body.String(), "sha256:") {
		t.Fatalf("manual redaction event body missing marker/hash: %s", manual.Body.String())
	}

	approvalRR := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		"Bash",
		map[string]any{"command": secretCommand},
	))
	if approvalRR.Code != http.StatusOK {
		t.Fatalf("redaction approval evaluate status = %d, want 200 body=%s", approvalRR.Code, approvalRR.Body.String())
	}
	mustNotContain(t, approvalRR.Body.String(), syntheticSecrets...)
	if !bodyHasRedactionMarker(approvalRR.Body.String()) {
		t.Fatalf("approval evaluate body missing redaction marker: %s", approvalRR.Body.String())
	}
	var approvalResp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, approvalRR, &approvalResp)
	if approvalResp.Decision != string(edgecore.DecisionRequireApproval) || approvalResp.ApprovalRef == "" {
		t.Fatalf("approval response decision/ref = %q/%q, want REQUIRE_APPROVAL with ref body=%s", approvalResp.Decision, approvalResp.ApprovalRef, approvalRR.Body.String())
	}
	if approvalResp.InputHash == "" || !strings.HasPrefix(approvalResp.InputHash, "sha256:") || approvalResp.ActionHash == "" || !strings.HasPrefix(approvalResp.ActionHash, "sha256:") {
		t.Fatalf("approval hashes = input:%q action:%q, want sha256 hashes", approvalResp.InputHash, approvalResp.ActionHash)
	}

	eventsRR := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/events?limit=10")
	if eventsRR.Code != http.StatusOK {
		t.Fatalf("redaction session events status = %d, want 200 body=%s", eventsRR.Code, eventsRR.Body.String())
	}
	mustNotContain(t, eventsRR.Body.String(), syntheticSecrets...)
	if !bodyHasRedactionMarker(eventsRR.Body.String()) || !strings.Contains(eventsRR.Body.String(), "sha256:") {
		t.Fatalf("session events body missing redaction marker/hash: %s", eventsRR.Body.String())
	}
	var eventsPage edgeEventPageResponseJSON
	decodeEdgeRouteJSON(t, eventsRR, &eventsPage)
	manualEvent := findEdgeEventByID(t, eventsPage.Items, artifactEventID)
	approvalEvent := findEdgeEventByID(t, eventsPage.Items, approvalResp.EventID)
	if manualEvent.InputHash == "" || !strings.HasPrefix(manualEvent.InputHash, "sha256:") || len(manualEvent.ArtifactPointers) != 1 {
		t.Fatalf("manual event hash/artifacts = %q/%#v, want sha256 hash and one artifact pointer", manualEvent.InputHash, manualEvent.ArtifactPointers)
	}
	if manualEvent.InputRedacted["command"] != "<redacted>" || manualEvent.InputRedacted["token"] != "<redacted>" || manualEvent.InputRedacted["aws_access_key_id"] != "<redacted>" {
		t.Fatalf("manual event input_redacted = %#v, want all synthetic secret fields redacted", manualEvent.InputRedacted)
	}
	if manualEvent.ArtifactPointers[0].URI != artifactURI || manualEvent.ArtifactPointers[0].SHA256 != artifactSHA {
		t.Fatalf("manual artifact pointer = %#v, want uri %q sha %q", manualEvent.ArtifactPointers[0], artifactURI, artifactSHA)
	}
	if approvalEvent.Decision != edgecore.DecisionRequireApproval || approvalEvent.InputHash == "" || !strings.HasPrefix(approvalEvent.InputHash, "sha256:") || approvalEvent.InputRedacted["command"] != "<redacted>" {
		t.Fatalf("approval event = decision:%q input_hash:%q input:%#v, want require-approval with redacted hashed input", approvalEvent.Decision, approvalEvent.InputHash, approvalEvent.InputRedacted)
	}

	detail := edgeApprovalRouteGETAs(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, "/api/v1/edge/approvals/"+approvalResp.ApprovalRef)
	if detail.Code != http.StatusOK {
		t.Fatalf("redaction approval detail status = %d, want 200 body=%s", detail.Code, detail.Body.String())
	}
	mustNotContain(t, detail.Body.String(), syntheticSecrets...)
	if !bodyHasRedactionMarker(detail.Body.String()) || !strings.Contains(detail.Body.String(), "sha256:") {
		t.Fatalf("approval detail body missing marker/hash: %s", detail.Body.String())
	}
	var pending edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, detail, &pending)
	if pending.Reason != "<redacted>" || pending.InputHash != approvalResp.InputHash || pending.ActionHash != approvalResp.ActionHash {
		t.Fatalf("pending approval = reason:%q input:%q action:%q, want redacted reason and response hashes %q/%q",
			pending.Reason, pending.InputHash, pending.ActionHash, approvalResp.InputHash, approvalResp.ActionHash)
	}

	// Use local/dev export behavior with no artifact-store reader: the bundle
	// must still include metadata-only missing-artifact manifests and must never
	// inline or echo tool payload literals.
	s.artifactStore = nil
	exportRR := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":10}`)
	if exportRR.Code != http.StatusOK {
		t.Fatalf("redaction export status = %d, want 200 body=%s", exportRR.Code, exportRR.Body.String())
	}
	mustNotContain(t, exportRR.Body.String(), syntheticSecrets...)
	if !bodyHasRedactionMarker(exportRR.Body.String()) || !strings.Contains(exportRR.Body.String(), "sha256:") {
		t.Fatalf("export body missing redaction marker/hash: %s", exportRR.Body.String())
	}
	var bundle edgecore.SessionExportBundle
	decodeEdgeRouteJSON(t, exportRR, &bundle)
	if bundle.ManifestVersion != edgecore.ExportManifestVersion || bundle.TenantID != edgeRouteTenant || bundle.Session.SessionID != session.SessionID {
		t.Fatalf("export identity = manifest:%q tenant:%q session:%q, want %q/%q/%q",
			bundle.ManifestVersion, bundle.TenantID, bundle.Session.SessionID, edgecore.ExportManifestVersion, edgeRouteTenant, session.SessionID)
	}
	if len(bundle.Approvals) != 1 || bundle.Approvals[0].Reason != "<redacted>" || bundle.Approvals[0].InputHash != approvalResp.InputHash {
		t.Fatalf("export approvals = %#v, want one redacted approval with input hash %q", bundle.Approvals, approvalResp.InputHash)
	}
	exportedManual := findEdgeEventByID(t, bundle.Events, artifactEventID)
	exportedApproval := findEdgeEventByID(t, bundle.Events, approvalResp.EventID)
	if exportedManual.InputRedacted["command"] != "<redacted>" || exportedApproval.InputRedacted["command"] != "<redacted>" {
		t.Fatalf("exported event inputs = manual:%#v approval:%#v, want redacted commands", exportedManual.InputRedacted, exportedApproval.InputRedacted)
	}
	if len(bundle.Artifacts) != 0 {
		t.Fatalf("export artifacts = %#v, want none when artifact store is not wired", bundle.Artifacts)
	}
	if len(bundle.MissingArtifacts) != 1 || bundle.MissingArtifacts[0].URI != artifactURI || bundle.MissingArtifacts[0].SHA256 != artifactSHA || bundle.MissingArtifacts[0].Reason != edgecore.MissingArtifactReasonNotFound {
		t.Fatalf("export missing artifacts = %#v, want one metadata-only missing-artifact manifest %q/%q not_found", bundle.MissingArtifacts, artifactURI, artifactSHA)
	}
}

func TestGatewayEdgeRoutesRegisteredAndTenantScoped(t *testing.T) {
	s, _ := newEdgeRouteTestServer(t)
	routes := make(map[string]routeInfo, len(s.Routes()))
	for _, route := range s.Routes() {
		routes[route.methodPathKey()] = route
	}

	for _, want := range edgeRouteExpectations() {
		got, ok := routes[want.method+" "+want.path]
		if !ok {
			t.Fatalf("missing Edge route registration for %s %s", want.method, want.path)
		}
		if got.Auth == "public" {
			t.Fatalf("Edge route %s %s was registered as public", want.method, want.path)
		}
		if got.Auth != "tenant" {
			t.Fatalf("Edge route %s %s auth = %q, want tenant", want.method, want.path, got.Auth)
		}
	}
}

func TestGatewayEdgeRoutesRequireAuthTenantAndReachHandlers(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	missingAuth := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions", nil)
	missingAuth.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, missingAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401", rr.Code)
	}

	missingTenant := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions", nil)
	addEdgeRouteAuth(missingTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, missingTenant)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing tenant status = %d, want 400", rr.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions", nil)
	addEdgeRouteAuth(authorized)
	authorized.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authorized)
	if rr.Code == http.StatusNotFound {
		t.Fatalf("authorized Edge sessions list returned 404; route is not wired")
	}
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
		t.Fatalf("authorized Edge sessions list was rejected by auth/tenant middleware: %d", rr.Code)
	}
}

func TestGatewayEdgeExportRequiresAuthTenantAndDeniesCrossTenant(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	path := "/api/v1/edge/sessions/" + session.SessionID + "/export"

	missingAuth := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	missingAuth.Header.Set("X-Tenant-ID", edgeRouteTenant)
	missingAuth.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, missingAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("export missing auth status = %d, want 401 body=%s", rr.Code, rr.Body.String())
	}

	missingTenant := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	addEdgeRouteAuth(missingTenant)
	missingTenant.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, missingTenant)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("export missing tenant status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}

	authorized := edgeRoutePOST(t, handler, path, `{}`)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized export status = %d, want 200 body=%s", authorized.Code, authorized.Body.String())
	}

	crossTenant := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant, path, `{}`, "")
	if crossTenant.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant export status = %d, want 404 body=%s", crossTenant.Code, crossTenant.Body.String())
	}
	assertBodyOmits(t, crossTenant.Body.String(), session.SessionID, edgeRouteTenant)
}

func TestGatewayEdgeSessionCreateRejectsBadJSONAndTenantMismatch(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", strings.NewReader(`{"agent_product":`))
	addEdgeRouteAuth(badJSON)
	badJSON.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, badJSON)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400", rr.Code)
	}

	mismatchBody := []byte(`{"tenant_id":"tenant-edge-b","agent_product":"claude-code"}`)
	mismatch := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader(mismatchBody))
	addEdgeRouteAuth(mismatch)
	mismatch.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, mismatch)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("body tenant mismatch status = %d, want 403", rr.Code)
	}
}

func TestGatewayEdgeSessionCreateUsesExistingBodyLimit(t *testing.T) {
	t.Setenv(envGatewayMaxJSONBodyBytes, "32")
	_, handler := newEdgeRouteTestServer(t)

	body := bytes.Repeat([]byte("x"), 64)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader(body))
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("oversized Edge session body status = %d, want existing tier-limit 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "max_body_bytes") {
		t.Fatalf("oversized Edge session response did not use existing max_body_bytes error")
	}
}

func TestGatewayEdgeSessionLifecycleResponseContract(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"repo":"github.com/cordum/cordum",
		"git_branch":"main",
		"cwd":"D:/Cordum/cordum",
		"policy_snapshot":"snap-edge-005",
		"policy_mode":"observe",
		"enforcement_layers":{"pre_tool_use":true},
		"labels":{"purpose":"edge005"}
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertNoEdgeTokenLeak(t, create.Body.String())

	var createResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, create, &createResp)
	if createResp.SessionID == "" {
		t.Fatalf("create session response missing session_id: %#v", createResp)
	}
	if createResp.ExecutionID == "" {
		t.Fatalf("create session response missing execution_id: %#v", createResp)
	}
	if createResp.TraceID == "" {
		t.Fatalf("create session response missing trace_id: %#v", createResp)
	}
	if createResp.PolicySnapshot != "snap-edge-005" {
		t.Fatalf("create session policy_snapshot = %q, want snap-edge-005", createResp.PolicySnapshot)
	}
	if createResp.DashboardURL != "/edge/sessions/"+createResp.SessionID {
		t.Fatalf("dashboard_url = %q, want relative session URL", createResp.DashboardURL)
	}
	if createResp.Session.SessionID != createResp.SessionID {
		t.Fatalf("nested session_id = %q, want %q", createResp.Session.SessionID, createResp.SessionID)
	}
	if createResp.Session.TenantID != edgeRouteTenant {
		t.Fatalf("session tenant_id = %q, want %q", createResp.Session.TenantID, edgeRouteTenant)
	}
	if createResp.Session.PrincipalID != "principal-edge-a" {
		t.Fatalf("principal_id = %q, want auth principal fallback", createResp.Session.PrincipalID)
	}
	if createResp.Session.Status != edgecore.SessionStatusRunning {
		t.Fatalf("session status = %q, want running", createResp.Session.Status)
	}
	if createResp.Session.PolicyMode != edgecore.PolicyModeObserve {
		t.Fatalf("session policy_mode = %q, want observe", createResp.Session.PolicyMode)
	}
	if !createResp.Session.EnforcementLayers["pre_tool_use"] {
		t.Fatalf("session enforcement_layers missing pre_tool_use=true: %#v", createResp.Session.EnforcementLayers)
	}
	if createResp.Execution.ExecutionID != createResp.ExecutionID {
		t.Fatalf("nested execution_id = %q, want %q", createResp.Execution.ExecutionID, createResp.ExecutionID)
	}
	if createResp.Execution.SessionID != createResp.SessionID {
		t.Fatalf("initial execution session_id = %q, want %q", createResp.Execution.SessionID, createResp.SessionID)
	}
	if createResp.Execution.TraceID != createResp.TraceID {
		t.Fatalf("initial execution trace_id = %q, want %q", createResp.Execution.TraceID, createResp.TraceID)
	}
	if createResp.Execution.PolicySnapshot != createResp.PolicySnapshot {
		t.Fatalf("initial execution policy_snapshot = %q, want %q", createResp.Execution.PolicySnapshot, createResp.PolicySnapshot)
	}

	get := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get session status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	var gotSession edgecore.EdgeSession
	decodeEdgeRouteJSON(t, get, &gotSession)
	if gotSession.SessionID != createResp.SessionID || gotSession.TraceID != createResp.TraceID {
		t.Fatalf("get session mismatch: %#v", gotSession)
	}

	list := edgeRouteGET(t, handler, "/api/v1/edge/sessions")
	if list.Code != http.StatusOK {
		t.Fatalf("list sessions status = %d, want 200 body=%s", list.Code, list.Body.String())
	}
	var page edgeSessionPageJSON
	decodeEdgeRouteJSON(t, list, &page)
	if len(page.Items) != 1 || page.Items[0].SessionID != createResp.SessionID {
		t.Fatalf("list sessions items = %#v, want one created session", page.Items)
	}

	heartbeat := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID+"/heartbeat", `{}`)
	if heartbeat.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200 body=%s", heartbeat.Code, heartbeat.Body.String())
	}
	var heartbeatResp edgeHeartbeatResponseJSON
	decodeEdgeRouteJSON(t, heartbeat, &heartbeatResp)
	if heartbeatResp.SessionID != createResp.SessionID || !heartbeatResp.HeartbeatAlive {
		t.Fatalf("heartbeat response = %#v, want same session and alive=true", heartbeatResp)
	}

	end := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID+"/end", `{"status":"ended"}`)
	if end.Code != http.StatusOK {
		t.Fatalf("end session status = %d, want 200 body=%s", end.Code, end.Body.String())
	}
	var ended edgecore.EdgeSession
	decodeEdgeRouteJSON(t, end, &ended)
	if ended.Status != edgecore.SessionStatusEnded || ended.EndedAt == nil {
		t.Fatalf("ended session = %#v, want status ended with ended_at", ended)
	}
}

func TestGatewayEdgeExecutionLifecycleResponseContract(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	create := edgeRoutePOST(t, handler, "/api/v1/edge/executions", `{
		"session_id":"`+session.SessionID+`",
		"adapter":"claude-code-hook",
		"mode":"local-dev",
		"workflow_run_id":"workflow-link-only",
		"step_id":"step-link-only",
		"job_id":"edge-job-link-only",
		"attempt":2,
		"worker_id":"worker-link-only",
		"policy_snapshot":"snap-execution-005",
		"labels":{"purpose":"edge005-exec"}
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create execution status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertNoEdgeTokenLeak(t, create.Body.String())

	var created edgecore.AgentExecution
	decodeEdgeRouteJSON(t, create, &created)
	if created.ExecutionID == "" {
		t.Fatalf("create execution missing execution_id: %#v", created)
	}
	if created.SessionID != session.SessionID || created.TenantID != edgeRouteTenant {
		t.Fatalf("execution tenant/session mismatch: %#v", created)
	}
	if created.Adapter != edgecore.AdapterClaudeCodeHook || created.Mode != edgecore.ExecutionModeLocalDev {
		t.Fatalf("execution adapter/mode = %q/%q", created.Adapter, created.Mode)
	}
	if created.WorkflowRunID != "workflow-link-only" || created.JobID != "edge-job-link-only" {
		t.Fatalf("execution optional links were not preserved: %#v", created)
	}
	if created.Status != edgecore.ExecutionStatusRunning {
		t.Fatalf("execution status = %q, want running", created.Status)
	}

	get := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+created.ExecutionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get execution status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	var got edgecore.AgentExecution
	decodeEdgeRouteJSON(t, get, &got)
	if got.ExecutionID != created.ExecutionID || got.JobID != "edge-job-link-only" {
		t.Fatalf("get execution mismatch: %#v", got)
	}

	linkedJob := edgeRouteGET(t, handler, "/api/v1/jobs/edge-job-link-only")
	if linkedJob.Code != http.StatusNotFound {
		t.Fatalf("linked job status = %d, want 404 proving execution create did not create Job state; body=%s", linkedJob.Code, linkedJob.Body.String())
	}

	end := edgeRoutePOST(t, handler, "/api/v1/edge/executions/"+created.ExecutionID+"/end", `{"status":"succeeded"}`)
	if end.Code != http.StatusOK {
		t.Fatalf("end execution status = %d, want 200 body=%s", end.Code, end.Body.String())
	}
	var ended edgecore.AgentExecution
	decodeEdgeRouteJSON(t, end, &ended)
	if ended.Status != edgecore.ExecutionStatusSucceeded || ended.EndedAt == nil {
		t.Fatalf("ended execution = %#v, want status succeeded with ended_at", ended)
	}
}

func TestGatewayEdgeSessionCreateRedactsBeforePersistenceAndResponse(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	rawPolicy := "sk-edge005sessionsecret"
	rawRepo := "secret://edge005-repo"
	rawCWD := "Authorization: Bearer edge005sessionbearer"
	rawLabel := "github_pat_edge005sessionsecret"
	body := `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"repo":"` + rawRepo + `",
		"cwd":"` + rawCWD + `",
		"policy_snapshot":"` + rawPolicy + `",
		"policy_mode":"observe",
		"enforcement_layers":{"secret://edge005-layer":true},
		"labels":{"api_key":"` + rawLabel + `","purpose":"edge005"}
	}`
	create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", body)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertBodyOmits(t, create.Body.String(), rawPolicy, rawRepo, rawCWD, rawLabel, "secret://edge005-layer")
	if !bodyHasRedactionMarker(create.Body.String()) {
		t.Fatalf("create session response did not include redaction marker: %s", create.Body.String())
	}

	var created edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, create, &created)
	get := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+created.SessionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get session status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	assertBodyOmits(t, get.Body.String(), rawPolicy, rawRepo, rawCWD, rawLabel, "secret://edge005-layer")
	if !bodyHasRedactionMarker(get.Body.String()) {
		t.Fatalf("stored session readback did not include redaction marker: %s", get.Body.String())
	}
}

func TestGatewayEdgeExecutionCreateRedactsBeforePersistenceAndResponse(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	rawRun := "secret://edge005-workflow"
	rawJob := "sk-edge005executionsecret"
	rawWorker := "Authorization: Bearer edge005executionbearer"
	rawPolicy := "github_pat_edge005executionsecret"
	rawLabel := "secret://edge005-exec-label"
	create := edgeRoutePOST(t, handler, "/api/v1/edge/executions", `{
		"session_id":"`+session.SessionID+`",
		"workflow_run_id":"`+rawRun+`",
		"job_id":"`+rawJob+`",
		"worker_id":"`+rawWorker+`",
		"policy_snapshot":"`+rawPolicy+`",
		"labels":{"token":"`+rawLabel+`","purpose":"edge005-exec"}
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create execution status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertBodyOmits(t, create.Body.String(), rawRun, rawJob, rawWorker, rawPolicy, rawLabel)
	if !bodyHasRedactionMarker(create.Body.String()) {
		t.Fatalf("create execution response did not include redaction marker: %s", create.Body.String())
	}

	var created edgecore.AgentExecution
	decodeEdgeRouteJSON(t, create, &created)
	get := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+created.ExecutionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get execution status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	assertBodyOmits(t, get.Body.String(), rawRun, rawJob, rawWorker, rawPolicy, rawLabel)
	if !bodyHasRedactionMarker(get.Body.String()) {
		t.Fatalf("stored execution readback did not include redaction marker: %s", get.Body.String())
	}
}

func TestGatewayEdgeSessionCreateCleansUpPartialStateOnLaterFailure(t *testing.T) {
	for _, tc := range []struct {
		name                string
		failCreateExecution bool
		failHeartbeat       bool
	}{
		{name: "execution create failure", failCreateExecution: true},
		{name: "heartbeat failure", failHeartbeat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, handler := newEdgeRouteTestServer(t)
			base := edgecore.NewRedisStoreFromClient(s.jobStore.Client())
			failing := &edgeCreateSessionFailureStore{
				Store:               base,
				failCreateExecution: tc.failCreateExecution,
				failHeartbeat:       tc.failHeartbeat,
			}
			s.edgeStore = failing

			create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
				"agent_product":"claude-code",
				"mode":"local-dev",
				"policy_snapshot":"snap-edge-005-cleanup"
			}`)
			if create.Code != http.StatusInternalServerError {
				t.Fatalf("create session status = %d, want 500 body=%s", create.Code, create.Body.String())
			}
			if failing.sessionID == "" {
				t.Fatalf("test store did not observe CreateSession")
			}
			if got, found, err := base.GetSession(context.Background(), edgeRouteTenant, failing.sessionID); err != nil || found || got != nil {
				t.Fatalf("partial session remained after failed create: found=%v got=%#v err=%v", found, got, err)
			}
			if failing.executionID != "" {
				if got, found, err := base.GetExecution(context.Background(), edgeRouteTenant, failing.executionID); err != nil || found || got != nil {
					t.Fatalf("partial execution remained after failed create: found=%v got=%#v err=%v", found, got, err)
				}
			}
		})
	}
}

func TestGatewayEdgeValidationErrorsDoNotEchoRequestPayload(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	secretLabelKey := strings.Repeat("x", edgecore.MaxLabelKeyBytes+1) + "-super-secret-token"
	body := `{
		"agent_product":"claude-code",
		"mode":"local-dev",
		"policy_snapshot":"snap-edge-005",
		"labels":{"` + secretLabelKey + `":"redacted-value"}
	}`
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid label status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "super-secret-token") || strings.Contains(rr.Body.String(), "redacted-value") {
		t.Fatalf("validation error echoed request payload/secret: %s", rr.Body.String())
	}
}

func TestGatewayEdgeErrorMappingAndTenantIsolation(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	otherTenantGet := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions/"+session.SessionID, nil)
	addEdgeRouteAuthFor(otherTenantGet, edgeRouteOtherAPIKey)
	otherTenantGet.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, otherTenantGet)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d, want 404 body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), session.SessionID) || strings.Contains(rr.Body.String(), edgeRouteTenant) {
		t.Fatalf("cross-tenant miss leaked protected identifiers: %s", rr.Body.String())
	}

	invalidEnd := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/end", `{"status":"running"}`)
	if invalidEnd.Code != http.StatusBadRequest {
		t.Fatalf("invalid terminal status = %d, want 400 body=%s", invalidEnd.Code, invalidEnd.Body.String())
	}

	staleEnd := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/end", `{"status":"ended","ended_at":"1970-01-01T00:00:00Z"}`)
	if staleEnd.Code != http.StatusBadRequest {
		t.Fatalf("invalid ended_at status = %d, want 400 body=%s", staleEnd.Code, staleEnd.Body.String())
	}

	s.edgeStore = nil
	unavailable := edgeRouteGET(t, handler, "/api/v1/edge/sessions")
	assertEdgeErrorShape(t, unavailable, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable)
}

// TestGatewayEdgeSessionLifecycleEmitsAuditEvents pins EDGE-014 step-10
// Gateway audit instrumentation for session/execution lifecycle. Each
// successful create/end step must fire exactly one audit event of the
// matching edge.* type with bounded TenantID and Extra fields. Audit
// failures must not change the response (SendSIEMEvent is panic-safe).
func TestGatewayEdgeSessionLifecycleEmitsAuditEvents(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	sink := &testAuditSender{}
	s.auditExporter = sink

	create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"policy_snapshot":"snap-edge-014-step-10"
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session status = %d body=%s", create.Code, create.Body.String())
	}
	var createResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, create, &createResp)

	// After create: session_started + execution_started.
	if got := sink.Len(); got != 2 {
		t.Fatalf("after create: audit events = %d, want 2 (session_started + execution_started)", got)
	}
	first, second := sink.Get(0), sink.Get(1)
	if first.EventType != audit.EventEdgeSessionStarted {
		t.Errorf("first event type = %q, want %q", first.EventType, audit.EventEdgeSessionStarted)
	}
	if second.EventType != audit.EventEdgeExecutionStarted {
		t.Errorf("second event type = %q, want %q", second.EventType, audit.EventEdgeExecutionStarted)
	}
	if first.TenantID != edgeRouteTenant {
		t.Errorf("first event TenantID = %q, want %q", first.TenantID, edgeRouteTenant)
	}
	if first.Severity != audit.SeverityInfo {
		t.Errorf("first event Severity = %q, want info", first.Severity)
	}
	if got := first.Extra["session_id"]; got != createResp.SessionID {
		t.Errorf("first event Extra[session_id] = %q, want %q", got, createResp.SessionID)
	}

	// End execution -> execution_ended.
	endExec := edgeRoutePOST(t, handler, "/api/v1/edge/executions/"+createResp.ExecutionID+"/end", `{"status":"succeeded"}`)
	if endExec.Code != http.StatusOK {
		t.Fatalf("end execution status = %d body=%s", endExec.Code, endExec.Body.String())
	}
	if got := sink.Len(); got != 3 {
		t.Fatalf("after end execution: audit events = %d, want 3", got)
	}
	if ev := sink.Get(2); ev.EventType != audit.EventEdgeExecutionEnded {
		t.Errorf("third event type = %q, want %q", ev.EventType, audit.EventEdgeExecutionEnded)
	}

	// End session -> session_ended.
	endSess := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID+"/end", `{"status":"ended"}`)
	if endSess.Code != http.StatusOK {
		t.Fatalf("end session status = %d body=%s", endSess.Code, endSess.Body.String())
	}
	if got := sink.Len(); got != 4 {
		t.Fatalf("after end session: audit events = %d, want 4", got)
	}
	if ev := sink.Get(3); ev.EventType != audit.EventEdgeSessionEnded {
		t.Errorf("fourth event type = %q, want %q", ev.EventType, audit.EventEdgeSessionEnded)
	}
	if ev := sink.Get(3); ev.Severity != audit.SeverityInfo {
		t.Errorf("session_ended Severity = %q, want info (clean ended)", ev.Severity)
	}
}

// TestGatewayEdgeSessionLifecycleAuditNilSenderIsNoOp pins that nil
// auditExporter is safe (no panic) — Edge handlers must not require
// the audit pipeline to be configured.
func TestGatewayEdgeSessionLifecycleAuditNilSenderIsNoOp(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	s.auditExporter = nil
	create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"mode":"local-dev"
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session with nil auditExporter status = %d body=%s", create.Code, create.Body.String())
	}
}

func newEdgeRouteTestServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[` +
			`{"key":"` + edgeRouteTestAPIKey + `","tenant":"` + edgeRouteTenant + `","role":"admin","principal_id":"principal-edge-a"},` +
			`{"key":"` + edgeRouteReviewerAPIKey + `","tenant":"` + edgeRouteTenant + `","role":"admin","principal_id":"principal-reviewer"},` +
			`{"key":"` + edgeRouteViewerAPIKey + `","tenant":"` + edgeRouteTenant + `","role":"viewer","principal_id":"principal-viewer"},` +
			`{"key":"` + edgeRouteUserAPIKey + `","tenant":"` + edgeRouteTenant + `","role":"user","principal_id":"principal-edge-user"},` +
			`{"key":"` + edgeRouteOtherAPIKey + `","tenant":"` + edgeRouteOtherTenant + `","role":"admin","principal_id":"principal-edge-b"}` +
			`]`,
	})
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return s, apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
}

func addEdgeRouteAuth(req *http.Request) {
	addEdgeRouteAuthFor(req, edgeRouteTestAPIKey)
}

func addEdgeRouteAuthFor(req *http.Request, apiKey string) {
	req.Header.Set("X-API-Key", edgeRouteTestAPIKey)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
}

type edgeSessionCreateResponseJSON struct {
	SessionID      string                  `json:"session_id"`
	ExecutionID    string                  `json:"execution_id"`
	TraceID        string                  `json:"trace_id"`
	PolicySnapshot string                  `json:"policy_snapshot"`
	DashboardURL   string                  `json:"dashboard_url"`
	Session        edgecore.EdgeSession    `json:"session"`
	Execution      edgecore.AgentExecution `json:"execution"`
}

type edgeSessionPageJSON struct {
	Items      []edgecore.EdgeSession `json:"items"`
	NextCursor string                 `json:"next_cursor"`
}

type edgeHeartbeatResponseJSON struct {
	SessionID      string `json:"session_id"`
	HeartbeatAlive bool   `json:"heartbeat_alive"`
}

func createEdgeRouteSession(t *testing.T, handler http.Handler) edgeSessionCreateResponseJSON {
	t.Helper()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"policy_snapshot":"snap-session-for-execution",
		"policy_mode":"observe"
	}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var session edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rr, &session)
	return session
}

func edgeRouteGET(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func edgeRoutePOST(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeEdgeRouteJSON(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON response %q: %v", rr.Body.String(), err)
	}
}

func assertStreamedEdgeEvent(t *testing.T, event wsEvent, wantEventID string, wantDecision edgecore.EdgeDecision, wantKind edgecore.EventKind) {
	t.Helper()
	if event.tenant != edgeRouteTenant {
		t.Fatalf("stream tenant = %q, want %q", event.tenant, edgeRouteTenant)
	}
	var envelope struct {
		Type  string                    `json:"type"`
		Event edgecore.AgentActionEvent `json:"event"`
	}
	if err := json.Unmarshal(event.data, &envelope); err != nil {
		t.Fatalf("decode streamed edge.event: %v body=%s", err, string(event.data))
	}
	if envelope.Type != "edge.event" ||
		envelope.Event.EventID != wantEventID ||
		envelope.Event.Decision != wantDecision ||
		envelope.Event.Kind != wantKind {
		t.Fatalf("stream envelope = type:%q event:%q decision:%q kind:%q, want edge.event/%q/%q/%q body=%s",
			envelope.Type, envelope.Event.EventID, envelope.Event.Decision, envelope.Event.Kind, wantEventID, wantDecision, wantKind, string(event.data))
	}
}

func assertNoEdgeTokenLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"hook_policy_token", "enterprise_hook_token", "api_key", "secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Edge response leaked forbidden token/secret field %q in %s", forbidden, body)
		}
	}
}

func assertBodyOmits(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	mustNotContain(t, body, forbidden...)
}

func mustNotContain(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if strings.Contains(body, value) {
			t.Fatalf("response leaked raw value %q in %s", value, body)
		}
	}
}

func bodyHasRedactionMarker(body string) bool {
	return strings.Contains(body, "<redacted>") || strings.Contains(body, `\u003credacted\u003e`)
}

func findEdgeEventByID(t *testing.T, events []edgecore.AgentActionEvent, eventID string) edgecore.AgentActionEvent {
	t.Helper()
	for _, event := range events {
		if event.EventID == eventID {
			return event
		}
	}
	t.Fatalf("event %q not found in %#v", eventID, events)
	return edgecore.AgentActionEvent{}
}

// assertEdgeErrorShape verifies that an /api/v1/edge/* error response uses
// the standard envelope `{ code, message, request_id, details? }` documented
// in PRD_ROADMAP §7.10. Pass empty wantCode to accept any code.
func assertEdgeErrorShape(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rr.Code != wantStatus {
		t.Fatalf("edge error status = %d, want %d body=%s", rr.Code, wantStatus, rr.Body.String())
	}
	var envelope struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		RequestID *string        `json:"request_id"`
		Details   map[string]any `json:"details"`
		Error     *string        `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode edge error envelope %q: %v", rr.Body.String(), err)
	}
	if envelope.Error != nil {
		t.Fatalf("edge error response uses legacy {error,status} shape: %s", rr.Body.String())
	}
	if strings.TrimSpace(envelope.Code) == "" {
		t.Fatalf("edge error response missing `code` field body=%s", rr.Body.String())
	}
	if strings.TrimSpace(envelope.Message) == "" {
		t.Fatalf("edge error response missing `message` field body=%s", rr.Body.String())
	}
	if envelope.RequestID == nil {
		t.Fatalf("edge error response missing `request_id` field body=%s", rr.Body.String())
	}
	if wantCode != "" && envelope.Code != wantCode {
		t.Fatalf("edge error code = %q, want %q body=%s", envelope.Code, wantCode, rr.Body.String())
	}
}

type edgeCreateSessionFailureStore struct {
	edgecore.Store
	failCreateExecution bool
	failHeartbeat       bool
	sessionID           string
	executionID         string
}

func (s *edgeCreateSessionFailureStore) CreateSession(ctx context.Context, session edgecore.EdgeSession) error {
	s.sessionID = session.SessionID
	return s.Store.CreateSession(ctx, session)
}

func (s *edgeCreateSessionFailureStore) CreateExecution(ctx context.Context, execution edgecore.AgentExecution) error {
	s.executionID = execution.ExecutionID
	if s.failCreateExecution {
		return errors.New("injected create execution failure")
	}
	return s.Store.CreateExecution(ctx, execution)
}

func (s *edgeCreateSessionFailureStore) TouchHeartbeat(ctx context.Context, tenantID, sessionID string) error {
	if s.failHeartbeat {
		return errors.New("injected heartbeat failure")
	}
	return s.Store.TouchHeartbeat(ctx, tenantID, sessionID)
}

func edgeRouteExpectations() []edgeRouteExpectation {
	return []edgeRouteExpectation{
		{method: http.MethodPost, path: "/api/v1/edge/sessions"},
		{method: http.MethodGet, path: "/api/v1/edge/sessions"},
		{method: http.MethodGet, path: "/api/v1/edge/sessions/{session_id}"},
		{method: http.MethodPost, path: "/api/v1/edge/sessions/{session_id}/heartbeat"},
		{method: http.MethodPost, path: "/api/v1/edge/sessions/{session_id}/end"},
		{method: http.MethodPost, path: "/api/v1/edge/executions"},
		{method: http.MethodGet, path: "/api/v1/edge/executions/{execution_id}"},
		{method: http.MethodPost, path: "/api/v1/edge/executions/{execution_id}/end"},
		{method: http.MethodGet, path: "/api/v1/edge/approvals"},
		{method: http.MethodGet, path: "/api/v1/edge/approvals/{approval_ref}"},
		{method: http.MethodPost, path: "/api/v1/edge/approvals/{approval_ref}/approve"},
		{method: http.MethodPost, path: "/api/v1/edge/approvals/{approval_ref}/reject"},
		{method: http.MethodPost, path: "/api/v1/edge/approvals/{approval_ref}/wait"},
		{method: http.MethodPost, path: "/api/v1/edge/evaluate"},
		{method: http.MethodPost, path: "/api/v1/edge/sessions/{session_id}/export"},
	}
}

func (r routeInfo) methodPathKey() string {
	return r.Method + " " + r.Path
}

// TestGatewayEdgeMaxExecutionsPerSessionCapReturns429 (EDGE-037) verifies that
// CreateExecution rejects the (cap+1)th execution for a session with the
// stable Edge error envelope and HTTP 429 max_executions_exceeded.
// Uses CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION=2 to keep the test fast; the
// helper is the same env knob production operators use.
func TestGatewayEdgeMaxExecutionsPerSessionCapReturns429(t *testing.T) {
	t.Setenv("CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION", "2")

	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	body := `{
		"session_id":"` + session.SessionID + `",
		"adapter":"claude-code-hook",
		"mode":"local-dev",
		"attempt":1
	}`

	// First two creates land successfully (cap = 2; createEdgeRouteSession
	// already created the session-initial execution which counts toward the
	// cap, so this is execution #2 and #2 — wait, the cap counts existing
	// rows. createEdgeRouteSession created 1 already; the next CreateExecution
	// makes it 2, and the one after that should be rejected at count=2 >= cap=2).
	r1 := edgeRoutePOST(t, handler, "/api/v1/edge/executions", body)
	if r1.Code != http.StatusCreated {
		t.Fatalf("first CreateExecution status = %d body=%s, want 201", r1.Code, r1.Body.String())
	}
	// Now count = 2 (initial + r1); the next create should hit the cap.
	r2 := edgeRoutePOST(t, handler, "/api/v1/edge/executions", body)
	if r2.Code != http.StatusTooManyRequests {
		t.Fatalf("over-cap CreateExecution status = %d body=%s, want 429", r2.Code, r2.Body.String())
	}
	type errEnvelope struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	var env errEnvelope
	decodeEdgeRouteJSON(t, r2, &env)
	if env.Code != "max_executions_exceeded" {
		t.Fatalf("over-cap error code = %q, want max_executions_exceeded (body=%s)", env.Code, r2.Body.String())
	}
	if env.Details == nil {
		t.Fatalf("over-cap error missing details body=%s", r2.Body.String())
	}
	if limit, ok := env.Details["limit"].(float64); !ok || int(limit) != 2 {
		t.Fatalf("over-cap details limit = %v, want 2 (body=%s)", env.Details["limit"], r2.Body.String())
	}
	if current, ok := env.Details["current"].(float64); !ok || int(current) < 2 {
		t.Fatalf("over-cap details current = %v, want >= 2 (body=%s)", env.Details["current"], r2.Body.String())
	}
}

// TestGatewayEdgeMaxExecutionsPerSessionCapDefaultAcceptsSmallSessions ensures
// the cap default doesn't break realistic dev workflows. With the default
// (DefaultMaxExecutionsPerSession=100), a few executions in a row stay under
// cap and continue to return 201.
func TestGatewayEdgeMaxExecutionsPerSessionCapDefaultAcceptsSmallSessions(t *testing.T) {
	// No env override — exercises the production default code path.
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	body := `{
		"session_id":"` + session.SessionID + `",
		"adapter":"claude-code-hook",
		"mode":"local-dev",
		"attempt":1
	}`
	for i := 0; i < 5; i++ {
		r := edgeRoutePOST(t, handler, "/api/v1/edge/executions", body)
		if r.Code != http.StatusCreated {
			t.Fatalf("CreateExecution #%d status = %d body=%s, want 201 under default cap", i, r.Code, r.Body.String())
		}
	}
}
