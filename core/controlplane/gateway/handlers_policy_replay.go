package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

// ---------- request / response types ----------

type policyReplayRequest struct {
	From              string              `json:"from"`
	To                string              `json:"to"`
	Filters           *policyReplayFilter `json:"filters,omitempty"`
	CandidateBundleID string              `json:"candidate_bundle_id,omitempty"`
	CandidateContent  string              `json:"candidate_content,omitempty"`
	UseCurrentPolicy  bool                `json:"use_current_policy,omitempty"`
	MaxJobs           int                 `json:"max_jobs,omitempty"`
}

type policyReplayFilter struct {
	Tenant           string `json:"tenant,omitempty"`
	TopicPattern     string `json:"topic_pattern,omitempty"`
	OriginalDecision string `json:"original_decision,omitempty"`
}

type policyReplayResponse struct {
	ReplayID       string                `json:"replay_id"`
	PolicySnapshot string                `json:"policy_snapshot"`
	TimeRange      policyReplayTimeRange `json:"time_range"`
	Summary        policyReplaySummary   `json:"summary"`
	RuleHits       []policyReplayRule    `json:"rule_hits"`
	Changes        []policyReplayChange  `json:"changes"`
	Warnings       []string              `json:"warnings,omitempty"`
	Errors         []string              `json:"errors,omitempty"`
}

type policyReplayTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type policyReplaySummary struct {
	TotalJobs int `json:"total_jobs"`
	Evaluated int `json:"evaluated"`
	Escalated int `json:"escalated"`
	Relaxed   int `json:"relaxed"`
	Unchanged int `json:"unchanged"`
	Errored   int `json:"errored"`
}

type policyReplayRule struct {
	RuleID   string `json:"rule_id"`
	Decision string `json:"decision"`
	Count    int    `json:"count"`
}

type policyReplayChange struct {
	JobID            string `json:"job_id"`
	Topic            string `json:"topic"`
	Tenant           string `json:"tenant"`
	OriginalDecision string `json:"original_decision"`
	NewDecision      string `json:"new_decision"`
	NewRuleID        string `json:"new_rule_id,omitempty"`
	NewReason        string `json:"new_reason,omitempty"`
	Direction        string `json:"direction"` // escalated | relaxed | unchanged
}

// ---------- decision severity for comparison ----------

var decisionSeverity = map[string]int{
	"ALLOW":                  0,
	"ALLOW_WITH_CONSTRAINTS": 1,
	"REQUIRE_APPROVAL":       2,
	"THROTTLE":               3,
	"DENY":                   4,
}

func compareDecisions(original, newDecision string) string {
	origSev, origOK := decisionSeverity[strings.ToUpper(original)]
	newSev, newOK := decisionSeverity[strings.ToUpper(newDecision)]
	if !origOK || !newOK {
		return "unchanged"
	}
	switch {
	case newSev > origSev:
		return "escalated"
	case newSev < origSev:
		return "relaxed"
	default:
		return "unchanged"
	}
}

func protoDecisionToString(d pb.DecisionType) string {
	switch d {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return "ALLOW"
	case pb.DecisionType_DECISION_TYPE_DENY:
		return "DENY"
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return "REQUIRE_APPROVAL"
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return "THROTTLE"
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return "ALLOW_WITH_CONSTRAINTS"
	default:
		return "ALLOW"
	}
}

// ---------- handler ----------

const (
	replayMaxSpan        = 7 * 24 * time.Hour
	replayDefaultMaxJobs = 500
	replayAbsoluteMax    = 1000
	replayBatchSize      = 200
)

func (s *server) handlePolicyReplay(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	if s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}

	var body policyReplayRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	// Parse and validate time range.
	fromTime, err := time.Parse(time.RFC3339, strings.TrimSpace(body.From))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "invalid 'from' timestamp: must be RFC3339")
		return
	}
	toTime, err := time.Parse(time.RFC3339, strings.TrimSpace(body.To))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "invalid 'to' timestamp: must be RFC3339")
		return
	}
	if !fromTime.Before(toTime) {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "'from' must be before 'to'")
		return
	}
	if toTime.Sub(fromTime) > replayMaxSpan {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "time range exceeds maximum of 7 days")
		return
	}

	// Defaults and caps.
	maxJobs := body.MaxJobs
	if maxJobs <= 0 {
		maxJobs = replayDefaultMaxJobs
	}
	if maxJobs > replayAbsoluteMax {
		maxJobs = replayAbsoluteMax
	}

	// Determine which policy must be provided.
	if !body.UseCurrentPolicy && strings.TrimSpace(body.CandidateContent) == "" && strings.TrimSpace(body.CandidateBundleID) == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "one of candidate_content, candidate_bundle_id, or use_current_policy must be specified")
		return
	}

	// Load bundles.
	ctx := r.Context()
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		writeInternalError(w, r, "policy replay load bundles", err)
		return
	}

	var policy *config.SafetyPolicy
	var snapshot string

	if body.UseCurrentPolicy {
		policy, snapshot, err = policybundles.BuildPolicyFromBundles(bundles)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, fmt.Sprintf("current policy invalid: %s", err.Error()))
			return
		}
	} else {
		working := policybundles.CloneBundleMap(bundles)
		candidateContent := strings.TrimSpace(body.CandidateContent)
		bundleID := strings.TrimSpace(body.CandidateBundleID)
		if bundleID == "" {
			bundleID = "__replay_candidate__"
		}
		if candidateContent != "" {
			working[bundleID] = map[string]any{
				"content": candidateContent,
				"enabled": true,
			}
		}
		policy, snapshot, err = policybundles.BuildPolicyFromBundles(working)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, fmt.Sprintf("candidate policy invalid: %s", err.Error()))
			return
		}
	}

	// Detect warnings from the policy.
	var warnings []string
	if policy != nil {
		for _, rule := range policy.Rules {
			if rule.Velocity != nil {
				warnings = append(warnings, "Velocity rules are not replayed (they depend on time-windowed counters)")
				break
			}
		}
		if len(policy.InputRules) > 0 {
			warnings = append(warnings, "Input content rules are not replayed (raw content is not stored)")
		}
	}

	// Convert time range to microseconds for Redis scores.
	fromMicros := fromTime.UTC().UnixMicro()
	toMicros := toTime.UTC().UnixMicro()

	// Iterate over jobs in batches.
	var (
		changes   []policyReplayChange
		ruleHits  = map[string]*policyReplayRule{}
		summary   policyReplaySummary
		evalErrs  []string
		cursor    int64
		collected int
	)

	for collected < maxJobs {
		jobIDs, err := s.jobStore.ListRecentJobsByTimeRange(ctx, fromMicros, toMicros, cursor, int64(replayBatchSize))
		if err != nil {
			writeInternalError(w, r, "policy replay list jobs", err)
			return
		}
		if len(jobIDs) == 0 {
			break
		}
		cursor += int64(len(jobIDs))

		// Batch-fetch metadata and requests.
		metas, err := s.jobStore.GetJobMetas(ctx, jobIDs)
		if err != nil {
			writeInternalError(w, r, "policy replay get metas", err)
			return
		}
		reqs, err := s.jobStore.GetJobRequests(ctx, jobIDs)
		if err != nil {
			writeInternalError(w, r, "policy replay get requests", err)
			return
		}

		for _, jobID := range jobIDs {
			if collected >= maxJobs {
				break
			}

			meta := metas[jobID]
			if meta == nil {
				continue
			}

			topic := strings.TrimSpace(meta[metaFieldTopic])
			tenant := strings.TrimSpace(meta[metaFieldTenant])
			originalDecision := strings.TrimSpace(meta[metaFieldSafetyDecision])
			if originalDecision == "" {
				originalDecision = "ALLOW"
			}

			// Apply filters.
			if body.Filters != nil {
				if f := strings.TrimSpace(body.Filters.Tenant); f != "" {
					if !strings.EqualFold(tenant, f) {
						continue
					}
				}
				if f := strings.TrimSpace(body.Filters.TopicPattern); f != "" {
					matched, _ := path.Match(f, topic)
					if !matched {
						continue
					}
				}
				if f := strings.TrimSpace(body.Filters.OriginalDecision); f != "" {
					if !strings.EqualFold(originalDecision, f) {
						continue
					}
				}
			}

			summary.TotalJobs++
			collected++

			// Build a PolicyCheckRequest from the stored job request payload.
			reqBytes := reqs[jobID]
			var checkReq *pb.PolicyCheckRequest
			if len(reqBytes) > 0 {
				var jobReq pb.JobRequest
				if unmarshalErr := protojson.Unmarshal(reqBytes, &jobReq); unmarshalErr != nil {
					summary.Errored++
					evalErrs = append(evalErrs, fmt.Sprintf("job %s: unmarshal request: %s", jobID, unmarshalErr.Error()))
					continue
				}
				checkReq = jobRequestToPolicyCheckRequest(&jobReq)
			} else {
				// Reconstruct a minimal check request from metadata.
				checkReq = metaToPolicyCheckRequest(jobID, meta)
			}

			// Evaluate the candidate policy.
			resp := policybundles.EvaluatePolicyCheck(policy, snapshot, checkReq)
			newDecision := protoDecisionToString(resp.GetDecision())
			newRuleID := resp.GetRuleId()
			newReason := resp.GetReason()

			summary.Evaluated++

			// Track rule hits.
			if newRuleID != "" {
				hit, ok := ruleHits[newRuleID]
				if !ok {
					hit = &policyReplayRule{
						RuleID:   newRuleID,
						Decision: newDecision,
					}
					ruleHits[newRuleID] = hit
				}
				hit.Count++
			}

			// Compare decisions.
			direction := compareDecisions(originalDecision, newDecision)
			switch direction {
			case "escalated":
				summary.Escalated++
			case "relaxed":
				summary.Relaxed++
			default:
				summary.Unchanged++
			}

			// Only record actual changes in the changes list.
			if direction != "unchanged" {
				changes = append(changes, policyReplayChange{
					JobID:            jobID,
					Topic:            topic,
					Tenant:           tenant,
					OriginalDecision: originalDecision,
					NewDecision:      newDecision,
					NewRuleID:        newRuleID,
					NewReason:        newReason,
					Direction:        direction,
				})
			}
		}
	}

	// Build rule hits slice.
	ruleHitList := make([]policyReplayRule, 0, len(ruleHits))
	for _, hit := range ruleHits {
		ruleHitList = append(ruleHitList, *hit)
	}

	if changes == nil {
		changes = []policyReplayChange{}
	}
	resp := policyReplayResponse{
		ReplayID:       uuid.NewString(),
		PolicySnapshot: snapshot,
		TimeRange: policyReplayTimeRange{
			From: fromTime.UTC().Format(time.RFC3339),
			To:   toTime.UTC().Format(time.RFC3339),
		},
		Summary:  summary,
		RuleHits: ruleHitList,
		Changes:  changes,
		Warnings: warnings,
		Errors:   evalErrs,
	}

	slog.Info("policy replay completed",
		"replay_id", resp.ReplayID,
		"from", body.From,
		"to", body.To,
		"total_jobs", summary.TotalJobs,
		"evaluated", summary.Evaluated,
		"escalated", summary.Escalated,
		"relaxed", summary.Relaxed,
		"unchanged", summary.Unchanged,
		"errored", summary.Errored,
	)

	writeJSON(w, resp)
}

// ---------- helpers ----------

const metaFieldTopic = "topic"
const metaFieldTenant = "tenant"
const metaFieldSafetyDecision = "safety_decision"

// jobRequestToPolicyCheckRequest converts a stored JobRequest into a
// PolicyCheckRequest suitable for policy evaluation replay.
func jobRequestToPolicyCheckRequest(req *pb.JobRequest) *pb.PolicyCheckRequest {
	if req == nil {
		return &pb.PolicyCheckRequest{}
	}
	tenant := req.GetTenantId()
	meta := req.GetMeta()
	if meta != nil && meta.GetTenantId() != "" {
		tenant = meta.GetTenantId()
	}
	labels := req.GetLabels()

	checkReq := &pb.PolicyCheckRequest{
		JobId:       req.GetJobId(),
		Topic:       req.GetTopic(),
		Tenant:      tenant,
		Priority:    req.GetPriority(),
		Budget:      req.GetBudget(),
		PrincipalId: req.GetPrincipalId(),
		Labels:      labels,
		MemoryId:    req.GetMemoryId(),
		Meta:        meta,
	}
	if envMap := req.GetEnv(); envMap != nil {
		if eff, ok := envMap["CORDUM_EFFECTIVE_CONFIG"]; ok && eff != "" {
			checkReq.EffectiveConfig = []byte(eff)
		}
	}
	return checkReq
}

// metaToPolicyCheckRequest constructs a minimal PolicyCheckRequest from job
// metadata when the original request payload is unavailable (expired).
func metaToPolicyCheckRequest(jobID string, meta map[string]string) *pb.PolicyCheckRequest {
	checkReq := &pb.PolicyCheckRequest{
		JobId:       jobID,
		Topic:       strings.TrimSpace(meta["topic"]),
		Tenant:      strings.TrimSpace(meta["tenant"]),
		PrincipalId: strings.TrimSpace(meta["principal"]),
		Labels:      metaLabels(meta),
	}

	jobMeta := &pb.JobMetadata{
		TenantId:       strings.TrimSpace(meta["tenant"]),
		ActorId:        strings.TrimSpace(meta["actor_id"]),
		ActorType:      parseActorType(strings.TrimSpace(meta["actor_type"])),
		IdempotencyKey: strings.TrimSpace(meta["idempotency_key"]),
		Capability:     strings.TrimSpace(meta["capability"]),
		PackId:         strings.TrimSpace(meta["pack_id"]),
	}

	if raw := strings.TrimSpace(meta["risk_tags"]); raw != "" {
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err == nil {
			jobMeta.RiskTags = tags
		}
	}
	if raw := strings.TrimSpace(meta["requires"]); raw != "" {
		var reqs []string
		if err := json.Unmarshal([]byte(raw), &reqs); err == nil {
			jobMeta.Requires = reqs
		}
	}
	checkReq.Meta = jobMeta
	return checkReq
}

// metaLabels extracts the labels JSON stored in job metadata.
func metaLabels(meta map[string]string) map[string]string {
	raw := strings.TrimSpace(meta["labels"])
	if raw == "" {
		return nil
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil
	}
	return labels
}
