package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// ---------- request / response types ----------

type policyAnalyticsRequest struct {
	From       string `json:"from"`
	To         string `json:"to"`
	RuleFilter string `json:"rule_filter,omitempty"`
}

type policyAnalyticsResponse struct {
	TimeRange analyticsTimeRange `json:"time_range"`
	Rules     []ruleAnalytics    `json:"rules"`
	Summary   analyticsSummary   `json:"summary"`
}

type analyticsTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ruleAnalytics struct {
	RuleID               string  `json:"rule_id"`
	HitCount             int     `json:"hit_count"`
	ApprovalCount        int     `json:"approval_count"`
	OverrideCount        int     `json:"override_count"`
	OverrideRate         float64 `json:"override_rate"`
	AvgApprovalLatencyMs int64   `json:"avg_approval_latency_ms"`
	DailyHits            []int   `json:"daily_hits"`
}

type analyticsSummary struct {
	TotalRules          int    `json:"total_rules"`
	TotalHits           int    `json:"total_hits"`
	TotalOverrides      int    `json:"total_overrides"`
	HighestOverrideRule string `json:"highest_override_rule_id"`
}

// ---------- handler ----------

const (
	analyticsMaxSpan   = 7 * 24 * time.Hour
	analyticsMaxJobs   = 1000
	analyticsBatchSize = 200
)

const policyRuleFiringLast7dBuckets = 7

func (s *server) ruleFiringLast7d(ctx context.Context, tenant string, ruleIDs []string, now time.Time) (map[string][]int, error) {
	result, wanted := initializeRuleFiringBuckets(ruleIDs)
	if len(wanted) == 0 {
		return result, nil
	}
	if s == nil || s.jobStore == nil {
		return nil, fmt.Errorf("job store unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	startDay := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(policyRuleFiringLast7dBuckets - 1))
	endDay := startDay.AddDate(0, 0, policyRuleFiringLast7dBuckets)

	var cursor int64
	scanned := 0
	for scanned < analyticsMaxJobs {
		limit := analyticsBatchSize
		if remaining := analyticsMaxJobs - scanned; remaining < limit {
			limit = remaining
		}
		batch, err := s.jobStore.ListRecentJobsByTimeRange(ctx, startDay.UnixMicro(), endDay.UnixMicro(), cursor, int64(limit))
		if err != nil {
			return nil, fmt.Errorf("list recent jobs: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		cursor += int64(len(batch))
		scanned += len(batch)
		if err := s.addRuleFiringBatchCounts(ctx, tenant, wanted, result, batch, startDay); err != nil {
			return nil, err
		}
		if len(batch) < limit {
			break
		}
	}
	return result, nil
}

func initializeRuleFiringBuckets(ruleIDs []string) (map[string][]int, map[string]struct{}) {
	result := make(map[string][]int, len(ruleIDs))
	wanted := make(map[string]struct{}, len(ruleIDs))
	for _, raw := range ruleIDs {
		ruleID := strings.TrimSpace(raw)
		if ruleID == "" {
			continue
		}
		if _, exists := wanted[ruleID]; exists {
			continue
		}
		wanted[ruleID] = struct{}{}
		result[ruleID] = make([]int, policyRuleFiringLast7dBuckets)
	}
	return result, wanted
}

func (s *server) addRuleFiringBatchCounts(ctx context.Context, tenant string, wanted map[string]struct{}, result map[string][]int, jobIDs []string, startDay time.Time) error {
	metas, err := s.jobStore.GetJobMetas(ctx, jobIDs)
	if err != nil {
		return fmt.Errorf("get job metas: %w", err)
	}
	for _, jobID := range jobIDs {
		meta := metas[jobID]
		if meta == nil || !strings.EqualFold(strings.TrimSpace(meta[metaFieldTenant]), tenant) {
			continue
		}
		ruleID := strings.TrimSpace(meta["safety_rule_id"])
		if _, ok := wanted[ruleID]; !ok {
			continue
		}
		updatedAt, ok := parseMicrosMeta(meta["updated_at"])
		if !ok {
			continue
		}
		dayIndex := int(time.UnixMicro(updatedAt).UTC().Truncate(24*time.Hour).Sub(startDay).Hours() / 24)
		if dayIndex >= 0 && dayIndex < policyRuleFiringLast7dBuckets {
			result[ruleID][dayIndex]++
		}
	}
	return nil
}

func parseMicrosMeta(raw string) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func (s *server) handlePolicyAnalytics(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyRead, "admin") {
		return
	}
	if s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}

	var body policyAnalyticsRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	fromTime, err := time.Parse(time.RFC3339, strings.TrimSpace(body.From))
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid 'from' timestamp: must be RFC3339")
		return
	}
	toTime, err := time.Parse(time.RFC3339, strings.TrimSpace(body.To))
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid 'to' timestamp: must be RFC3339")
		return
	}
	if !fromTime.Before(toTime) {
		writeErrorJSON(w, http.StatusBadRequest, "'from' must be before 'to'")
		return
	}
	if toTime.Sub(fromTime) > analyticsMaxSpan {
		writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("time range exceeds maximum of %d days", int(analyticsMaxSpan.Hours()/24)))
		return
	}

	ctx := r.Context()
	fromMicros := fromTime.UnixMicro()
	toMicros := toTime.UnixMicro()
	ruleFilter := strings.TrimSpace(body.RuleFilter)
	callerTenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	slog.Info("policy analytics request",
		"from", body.From, "to", body.To,
		"rule_filter", ruleFilter,
		"tenant", callerTenant,
		"actor", policybundles.PolicyActorID(r))

	// Fetch job IDs in batches.
	var allJobIDs []string
	var cursor int64
	for {
		batch, err := s.jobStore.ListRecentJobsByTimeRange(ctx, fromMicros, toMicros, cursor, analyticsBatchSize)
		if err != nil {
			slog.Error("policy analytics: list recent jobs failed", "error", err)
			writeInternalError(w, r, "list recent jobs", err)
			return
		}
		allJobIDs = append(allJobIDs, batch...)
		if len(batch) < analyticsBatchSize || len(allJobIDs) >= analyticsMaxJobs {
			break
		}
		cursor += int64(len(batch))
	}
	if len(allJobIDs) > analyticsMaxJobs {
		allJobIDs = allJobIDs[:analyticsMaxJobs]
	}

	// Batch fetch metadata.
	metas, err := s.jobStore.GetJobMetas(ctx, allJobIDs)
	if err != nil {
		slog.Error("policy analytics: get job metas failed", "error", err)
		writeInternalError(w, r, "get job metas", err)
		return
	}

	// Compute the number of day buckets between from and to (max 7).
	numDays := int(math.Ceil(toTime.Sub(fromTime).Hours() / 24))
	if numDays < 1 {
		numDays = 1
	}
	if numDays > 7 {
		numDays = 7
	}

	type ruleAccum struct {
		hitCount      int
		approvalCount int
		overrideCount int
		latencySum    int64
		latencyCount  int
		dailyHits     []int
	}
	accum := map[string]*ruleAccum{}

	for _, id := range allJobIDs {
		meta, ok := metas[id]
		if !ok {
			continue
		}
		tenant := strings.TrimSpace(meta[metaFieldTenant])
		if !strings.EqualFold(tenant, callerTenant) {
			continue
		}
		ruleID := strings.TrimSpace(meta["safety_rule_id"])
		if ruleID == "" {
			continue
		}
		if ruleFilter != "" && ruleID != ruleFilter {
			continue
		}

		ra, exists := accum[ruleID]
		if !exists {
			ra = &ruleAccum{dailyHits: make([]int, numDays)}
			accum[ruleID] = ra
		}
		ra.hitCount++

		// Compute which day bucket this job falls into.
		updatedAt := int64(0)
		if raw := meta["updated_at"]; raw != "" {
			_, _ = fmt.Sscanf(raw, "%d", &updatedAt)
		}
		if updatedAt > 0 {
			jobTime := time.UnixMicro(updatedAt)
			dayIndex := int(jobTime.Sub(fromTime).Hours() / 24)
			if dayIndex < 0 {
				dayIndex = 0
			}
			if dayIndex >= numDays {
				dayIndex = numDays - 1
			}
			ra.dailyHits[dayIndex]++
		}

		// Check if this was an approval-requiring job.
		decision := strings.TrimSpace(meta["safety_decision"])
		if decision == "REQUIRE_APPROVAL" {
			ra.approvalCount++

			// Check if a human overrode (approved) the decision.
			approvalBy := strings.TrimSpace(meta["approval_by"])
			if approvalBy != "" {
				ra.overrideCount++

				// Compute approval latency if timestamps available.
				checkedAt := int64(0)
				approvedAt := int64(0)
				if raw := meta["safety_checked_at"]; raw != "" {
					_, _ = fmt.Sscanf(raw, "%d", &checkedAt)
				}
				if raw := meta["approval_at"]; raw != "" {
					_, _ = fmt.Sscanf(raw, "%d", &approvedAt)
				}
				if checkedAt > 0 && approvedAt > 0 && approvedAt > checkedAt {
					latencyMs := (approvedAt - checkedAt) / 1000 // micros to ms
					ra.latencySum += latencyMs
					ra.latencyCount++
				}
			}
		}
	}

	// Build response.
	rules := make([]ruleAnalytics, 0, len(accum))
	totalHits := 0
	totalOverrides := 0
	highestOverrideRate := 0.0
	highestOverrideRuleID := ""

	for ruleID, ra := range accum {
		overrideRate := 0.0
		if ra.approvalCount > 0 {
			overrideRate = float64(ra.overrideCount) / float64(ra.approvalCount)
		}
		avgLatency := int64(0)
		if ra.latencyCount > 0 {
			avgLatency = ra.latencySum / int64(ra.latencyCount)
		}

		rules = append(rules, ruleAnalytics{
			RuleID:               ruleID,
			HitCount:             ra.hitCount,
			ApprovalCount:        ra.approvalCount,
			OverrideCount:        ra.overrideCount,
			OverrideRate:         math.Round(overrideRate*1000) / 1000, // 3 decimal places
			AvgApprovalLatencyMs: avgLatency,
			DailyHits:            ra.dailyHits,
		})

		totalHits += ra.hitCount
		totalOverrides += ra.overrideCount
		if overrideRate > highestOverrideRate {
			highestOverrideRate = overrideRate
			highestOverrideRuleID = ruleID
		}
	}

	// Sort by hit count descending for consistent output.
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].HitCount > rules[j].HitCount
	})

	resp := policyAnalyticsResponse{
		TimeRange: analyticsTimeRange{
			From: fromTime.Format(time.RFC3339),
			To:   toTime.Format(time.RFC3339),
		},
		Rules: rules,
		Summary: analyticsSummary{
			TotalRules:          len(rules),
			TotalHits:           totalHits,
			TotalOverrides:      totalOverrides,
			HighestOverrideRule: highestOverrideRuleID,
		},
	}

	slog.Info("policy analytics completed",
		"total_rules", len(rules),
		"total_hits", totalHits,
		"total_overrides", totalOverrides,
		"jobs_scanned", len(allJobIDs),
		"tenant", callerTenant,
		"actor", policybundles.PolicyActorID(r))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("policy analytics: encode response failed", "error", err)
	}
}
