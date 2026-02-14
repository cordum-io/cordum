package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)


type policyBundleSimulateRequest struct {
	Request policyCheckRequest `json:"request"`
	Content string             `json:"content"`
}


func (s *server) handlePolicyBundles(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	bundles, updatedAt, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := bundleSummaryList(bundles)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"bundles":    bundles,
		"items":      items,
		"updated_at": updatedAt,
	})
}

func (s *server) handlePolicyRules(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	includeDisabled := parseBool(r.URL.Query().Get("include_disabled"))
	fragmentIDs := make([]string, 0, len(bundles))
	for fragmentID := range bundles {
		fragmentIDs = append(fragmentIDs, fragmentID)
	}
	sort.Strings(fragmentIDs)

	items := make([]map[string]any, 0)
	parseErrors := make([]policyRuleParseError, 0)
	for _, fragmentID := range fragmentIDs {
		rawBundle := bundles[fragmentID]
		bundle, _ := rawBundle.(map[string]any)
		if bundle != nil && !includeDisabled && !bundleEnabled(bundle) {
			continue
		}
		content := ""
		switch v := rawBundle.(type) {
		case string:
			content = strings.TrimSpace(v)
		case map[string]any:
			content = strings.TrimSpace(stringFromAny(v["content"]))
			if content == "" {
				content = strings.TrimSpace(stringFromAny(v["policy"]))
			}
			if content == "" {
				content = strings.TrimSpace(stringFromAny(v["data"]))
			}
		}
		if content == "" {
			continue
		}
		bundleMeta := bundle
		if bundleMeta == nil {
			bundleMeta = map[string]any{}
		}
		rules, err := rulesFromPolicyContent(fragmentID, bundleMeta, content)
		if err != nil {
			parseErrors = append(parseErrors, policyRuleParseError{
				FragmentID: fragmentID,
				Error:      err.Error(),
			})
			continue
		}
		items = append(items, rules...)
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"items": items}
	if len(parseErrors) > 0 {
		resp["errors"] = parseErrors
	}
	writeJSON(w, resp)
}

func (s *server) handlePolicyOutputRules(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	includeDisabled := parseBool(r.URL.Query().Get("include_disabled"))
	fragmentIDs := make([]string, 0, len(bundles))
	for fragmentID := range bundles {
		fragmentIDs = append(fragmentIDs, fragmentID)
	}
	sort.Strings(fragmentIDs)

	items := make([]map[string]any, 0)
	parseErrors := make([]policyRuleParseError, 0)
	for _, fragmentID := range fragmentIDs {
		rawBundle := bundles[fragmentID]
		bundle, _ := rawBundle.(map[string]any)
		if bundle != nil && !includeDisabled && !bundleEnabled(bundle) {
			continue
		}
		content := ""
		switch v := rawBundle.(type) {
		case string:
			content = strings.TrimSpace(v)
		case map[string]any:
			content = strings.TrimSpace(stringFromAny(v["content"]))
			if content == "" {
				content = strings.TrimSpace(stringFromAny(v["policy"]))
			}
			if content == "" {
				content = strings.TrimSpace(stringFromAny(v["data"]))
			}
		}
		if content == "" {
			continue
		}
		bundleMeta := bundle
		if bundleMeta == nil {
			bundleMeta = map[string]any{}
		}
		rules, err := outputRulesFromPolicyContent(fragmentID, bundleMeta, content)
		if err != nil {
			parseErrors = append(parseErrors, policyRuleParseError{
				FragmentID: fragmentID,
				Error:      err.Error(),
			})
			continue
		}
		items = append(items, rules...)
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"items": items}
	if len(parseErrors) > 0 {
		resp["errors"] = parseErrors
	}
	writeJSON(w, resp)
}

func (s *server) handlePolicyOutputStats(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	resp := map[string]any{
		"total_checks_24h": 0,
		"quarantined_24h":  0,
		"avg_latency_ms":   0,
		"last_check_at":    "",
	}
	if s.jobStore == nil {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, resp)
		return
	}

	limit := int64(500)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	limit = clampListLimit(limit)
	if limit <= 0 {
		limit = 500
	}

	jobs, err := s.jobStore.ListRecentJobs(r.Context(), limit)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	cutoffMicros := time.Now().UTC().Add(-24 * time.Hour).UnixMicro()
	var totalChecks int64
	var quarantined int64
	var latencySumMs int64
	var latencySamples int64
	var lastCheckMicros int64

	for _, job := range jobs {
		if tenant := strings.TrimSpace(job.Tenant); tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				continue
			}
		}
		record, err := s.jobStore.GetOutputDecision(r.Context(), job.ID)
		if err != nil || record.Decision == "" {
			continue
		}
		checkedAt := record.CheckedAt
		if checkedAt <= 0 {
			checkedAt = job.UpdatedAt
		}
		if checkedAt <= 0 || checkedAt < cutoffMicros {
			continue
		}

		totalChecks++
		if record.Decision == model.OutputQuarantine || record.Decision == model.OutputDeny {
			quarantined++
		}
		if record.CheckDurationMs > 0 {
			latencySumMs += record.CheckDurationMs
			latencySamples++
		}
		if checkedAt > lastCheckMicros {
			lastCheckMicros = checkedAt
		}
	}

	avgLatencyMs := int64(0)
	if latencySamples > 0 {
		avgLatencyMs = latencySumMs / latencySamples
	}

	resp["total_checks_24h"] = totalChecks
	resp["quarantined_24h"] = quarantined
	resp["avg_latency_ms"] = avgLatencyMs
	resp["last_check_at"] = timestampFromMicros(lastCheckMicros)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handlePutPolicyOutputRule(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	ruleID := strings.TrimSpace(r.PathValue("id"))
	if ruleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "rule id required")
		return
	}
	var body outputRuleToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Enabled == nil {
		writeErrorJSON(w, http.StatusBadRequest, "enabled is required")
		return
	}

	bundles, doc, err := s.loadPolicyBundlesWithDoc(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	updatedBundleID := ""
	updated := false

	keys := make([]string, 0, len(bundles))
	for key := range bundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		rawBundle := bundles[key]
		content, ok := policyBundleContent(rawBundle)
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		sanitizedContent := sanitizePolicyBundleYAML(content)
		policy, err := config.ParseSafetyPolicy([]byte(sanitizedContent))
		if err != nil || policy == nil || len(policy.OutputRules) == 0 {
			continue
		}
		foundInBundle := false
		for idx := range policy.OutputRules {
			if strings.TrimSpace(policy.OutputRules[idx].ID) != ruleID {
				continue
			}
			enabledVal := *body.Enabled
			policy.OutputRules[idx].Enabled = &enabledVal
			foundInBundle = true
			break
		}
		if !foundInBundle {
			continue
		}
		payload, err := yaml.Marshal(policy)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to encode policy bundle")
			return
		}
		bundle, _ := rawBundle.(map[string]any)
		if bundle == nil {
			bundle = map[string]any{}
		}
		bundle["content"] = string(payload)
		bundle["updated_at"] = now
		bundles[key] = bundle
		updatedBundleID = key
		updated = true
		break
	}

	if !updated {
		writeErrorJSON(w, http.StatusNotFound, "output rule not found")
		return
	}

	if doc == nil {
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(policyConfigScope),
			ScopeID: policyConfigID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	doc.Data[policyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	message := fmt.Sprintf("set output rule %s enabled=%t", ruleID, *body.Enabled)
	s.appendAuditEntryNamed(r.Context(), "edit", "output_rule", ruleID, updatedBundleID, policyActorID(r), policyRole(r), message)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"id":         ruleID,
		"enabled":    *body.Enabled,
		"bundle_id":  updatedBundleID,
		"updated_at": now,
	})
}

func (s *server) handleGetPolicyBundle(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	bundleID := bundleIDFromRequest(r)
	if bundleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "bundle id required")
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw, ok := bundles[bundleID]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "bundle not found")
		return
	}
	bundle, _ := raw.(map[string]any)
	if bundle == nil {
		writeErrorJSON(w, http.StatusNotFound, "bundle invalid")
		return
	}
	content := strings.TrimSpace(stringFromAny(bundle["content"]))
	resp := policyBundleDetail{
		ID:        bundleID,
		Content:   content,
		Enabled:   bundleEnabled(bundle),
		Author:    strings.TrimSpace(stringFromAny(bundle["author"])),
		Message:   strings.TrimSpace(stringFromAny(bundle["message"])),
		CreatedAt: strings.TrimSpace(stringFromAny(bundle["created_at"])),
		UpdatedAt: strings.TrimSpace(stringFromAny(bundle["updated_at"])),
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handlePutPolicyBundle(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	bundleID := bundleIDFromRequest(r)
	if bundleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "bundle id required")
		return
	}
	if !strings.HasPrefix(bundleID, policyStudioPrefix) {
		writeErrorJSON(w, http.StatusBadRequest, "bundle id must start with secops/")
		return
	}
	var body policyBundleUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	content := sanitizePolicyBundleYAML(strings.TrimSpace(body.Content))
	if content == "" {
		writeErrorJSON(w, http.StatusBadRequest, "content required")
		return
	}
	if _, err := config.ParseSafetyPolicy([]byte(content)); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid policy content: %v", err))
		return
	}

	doc, err := getConfigDoc(r.Context(), s.configSvc, policyConfigScope, policyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(policyConfigScope), ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	bundle, _ := bundles[bundleID].(map[string]any)
	if bundle == nil {
		bundle = map[string]any{}
		bundle["created_at"] = now
	}
	bundle["content"] = content
	bundle["updated_at"] = now
	if body.Author != "" {
		bundle["author"] = strings.TrimSpace(body.Author)
	}
	if body.Message != "" {
		bundle["message"] = strings.TrimSpace(body.Message)
	}
	if body.Enabled != nil {
		bundle["enabled"] = *body.Enabled
	}
	bundles[bundleID] = bundle
	doc.Data[policyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendAuditEntryNamed(r.Context(), "edit", "policy", bundleID, bundleID, policyActorID(r), policyRole(r), "edit policy bundle "+bundleID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"id":         bundleID,
		"updated_at": now,
	})
}

func (s *server) handleSimulatePolicyBundle(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	bundleID := bundleIDFromRequest(r)
	if bundleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "bundle id required")
		return
	}
	var body policyBundleSimulateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	tenant, err := s.resolveTenant(r, body.Request.Tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	body.Request.Tenant = tenant
	body.Request.OrgId = tenant
	principalID, err := s.resolvePrincipal(r, body.Request.PrincipalId)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	body.Request.PrincipalId = principalID
	if body.Request.Meta == nil {
		body.Request.Meta = &policyMetaRequest{}
	}
	body.Request.Meta.TenantId = tenant
	checkReq, err := buildPolicyCheckRequest(r.Context(), &body.Request, s.configSvc, s.tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	working := cloneBundleMap(bundles)
	if strings.TrimSpace(body.Content) != "" {
		working[bundleID] = map[string]any{
			"content": strings.TrimSpace(body.Content),
			"enabled": true,
		}
	}
	policy, snapshot, err := buildPolicyFromBundles(working)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := evaluatePolicyCheck(policy, snapshot, checkReq)
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

func (s *server) handlePublishPolicyBundles(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var body policyPublishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	bundles, doc, err := s.loadPolicyBundlesWithDoc(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(bundles) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "no bundles configured")
		return
	}
	targets := resolvePublishTargets(bundles, body.BundleIDs)
	if len(targets) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "no policy bundles to publish")
		return
	}
	beforeSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), bundles, body.Note)

	now := time.Now().UTC().Format(time.RFC3339)
	for _, bundleID := range targets {
		raw := bundles[bundleID]
		bundle, _ := raw.(map[string]any)
		if bundle == nil {
			bundle = map[string]any{}
		}
		bundle["enabled"] = true
		bundle["updated_at"] = now
		if body.Author != "" {
			bundle["author"] = strings.TrimSpace(body.Author)
		}
		if body.Message != "" {
			bundle["message"] = strings.TrimSpace(body.Message)
		}
		if bundle["created_at"] == nil {
			bundle["created_at"] = now
		}
		bundles[bundleID] = bundle
	}
	if err := validateBundles(bundles); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if doc == nil {
		doc = &configsvc.Document{Scope: configsvc.Scope(policyConfigScope), ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	doc.Data[policyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	afterSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), bundles, body.Note)
	_ = s.appendPolicyAudit(r.Context(), policyAuditEntry{
		Action:         "publish",
		ResourceType:   "policy",
		ActorID:        policyActorID(r),
		Role:           policyRole(r),
		BundleIDs:      targets,
		Message:        strings.TrimSpace(body.Message),
		SnapshotBefore: beforeSnapshot,
		SnapshotAfter:  afterSnapshot,
		CreatedAt:      now,
	})
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"snapshot_before": beforeSnapshot,
		"snapshot_after":  afterSnapshot,
		"published":       targets,
	})
}

func (s *server) handleRollbackPolicyBundles(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var body policyRollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	snapshotID := strings.TrimSpace(body.SnapshotID)
	if snapshotID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "snapshot_id required")
		return
	}
	bundles, doc, err := s.loadPolicyBundlesWithDoc(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	beforeSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), bundles, body.Note)

	snapshots, _, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	var target *policyBundleSnapshot
	for _, snap := range snapshots {
		if snap.ID == snapshotID {
			target = &snap
			break
		}
	}
	if target == nil {
		writeErrorJSON(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if doc == nil {
		doc = &configsvc.Document{Scope: configsvc.Scope(policyConfigScope), ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	doc.Data[policyConfigKey] = target.Bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	afterSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), target.Bundles, body.Note)
	_ = s.appendPolicyAudit(r.Context(), policyAuditEntry{
		Action:         "rollback",
		ResourceType:   "policy",
		ActorID:        policyActorID(r),
		Role:           policyRole(r),
		BundleIDs:      []string{},
		Message:        strings.TrimSpace(body.Message),
		SnapshotBefore: beforeSnapshot,
		SnapshotAfter:  afterSnapshot,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"snapshot_before": beforeSnapshot,
		"snapshot_after":  afterSnapshot,
		"rollback_to":     snapshotID,
	})
}

func (s *server) handleListPolicyAudit(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	limit := parseAuditLimit(r.URL.Query().Get("limit"))
	ruleID := strings.TrimSpace(r.URL.Query().Get("rule_id"))
	auditType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))

	if auditType == "output" {
		items, err := s.listOutputPolicyAudit(r, ruleID, limit)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"items": items})
		return
	}

	entries, err := s.loadPolicyAudit(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered := make([]policyAuditEntry, 0, len(entries))
	for _, entry := range entries {
		if ruleID != "" && !strings.EqualFold(strings.TrimSpace(entry.ResourceID), ruleID) {
			continue
		}
		if auditType != "" && !strings.EqualFold(strings.TrimSpace(entry.ResourceType), auditType) {
			continue
		}
		filtered = append(filtered, entry)
		if int64(len(filtered)) >= limit {
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": filtered})
}

func parseAuditLimit(raw string) int64 {
	limit := int64(100)
	if parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && parsed > 0 {
		limit = parsed
	}
	return clampListLimit(limit)
}

func (s *server) listOutputPolicyAudit(r *http.Request, ruleID string, limit int64) ([]map[string]any, error) {
	if s.jobStore == nil || limit <= 0 {
		return []map[string]any{}, nil
	}
	fetchLimit := limit * 8
	if fetchLimit < 64 {
		fetchLimit = 64
	}
	if fetchLimit > 1000 {
		fetchLimit = 1000
	}
	jobs, err := s.jobStore.ListRecentJobs(r.Context(), fetchLimit)
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, limit)
	for _, job := range jobs {
		if int64(len(items)) >= limit {
			break
		}
		if job.State != model.JobStateQuarantined {
			continue
		}
		if tenant := strings.TrimSpace(job.Tenant); tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				continue
			}
		}
		record, err := s.jobStore.GetOutputDecision(r.Context(), job.ID)
		if err != nil || record.Decision == "" {
			continue
		}
		matchedRule := strings.TrimSpace(record.RuleID)
		if ruleID != "" && !strings.EqualFold(matchedRule, ruleID) {
			continue
		}
		createdAt := timestampFromMicros(record.CheckedAt)
		if createdAt == "" {
			createdAt = timestampFromMicros(job.UpdatedAt)
		}
		findingRows := make([]map[string]any, 0, len(record.Findings))
		for _, finding := range record.Findings {
			row := map[string]any{
				"type":     strings.TrimSpace(finding.Type),
				"severity": strings.TrimSpace(finding.Severity),
				"detail":   strings.TrimSpace(finding.Detail),
			}
			if scanner := strings.TrimSpace(finding.Scanner); scanner != "" {
				row["scanner"] = scanner
			}
			if finding.Confidence > 0 {
				row["confidence"] = finding.Confidence
			}
			if matchedPattern := strings.TrimSpace(finding.MatchedPattern); matchedPattern != "" {
				row["matched_pattern"] = matchedPattern
			}
			if finding.Offset > 0 {
				row["offset"] = finding.Offset
			}
			if finding.Length > 0 {
				row["length"] = finding.Length
			}
			findingRows = append(findingRows, row)
		}
		entry := map[string]any{
			"id":            fmt.Sprintf("output:%s:%d", job.ID, len(items)+1),
			"action":        "output_quarantine",
			"resource_type": "output",
			"resource_id":   matchedRule,
			"resource_name": job.ID,
			"job_id":        job.ID,
			"rule_id":       matchedRule,
			"decision":      strings.ToLower(strings.TrimSpace(string(record.Decision))),
			"reason":        strings.TrimSpace(record.Reason),
			"phase":         strings.TrimSpace(record.Phase),
			"created_at":    createdAt,
			"findings":      findingRows,
		}
		if ptr := strings.TrimSpace(record.OriginalPtr); ptr != "" {
			entry["original_ptr"] = ptr
		}
		if ptr := strings.TrimSpace(record.RedactedPtr); ptr != "" {
			entry["redacted_ptr"] = ptr
		}
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		return stringFromAny(items[i]["created_at"]) > stringFromAny(items[j]["created_at"])
	})
	return items, nil
}

func timestampFromMicros(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.UnixMicro(value).UTC().Format(time.RFC3339)
}

func (s *server) handleListPolicyBundleSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	snapshots, _, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]policyBundleSnapshotSummary, 0, len(snapshots))
	for _, snap := range snapshots {
		items = append(items, policyBundleSnapshotSummary{
			ID:        snap.ID,
			CreatedAt: snap.CreatedAt,
			Note:      snap.Note,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

func (s *server) handleCapturePolicyBundleSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	hash, err := hashValue(bundles)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to hash bundles")
		return
	}
	timestamp := time.Now().UTC().Format(time.RFC3339)
	id := timestamp + "-" + hash[:10]
	snapshot := policyBundleSnapshot{
		ID:        id,
		CreatedAt: timestamp,
		Note:      strings.TrimSpace(body.Note),
		Bundles:   bundles,
	}

	snapshots, doc, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	snapshots = append([]policyBundleSnapshot{snapshot}, snapshots...)
	if len(snapshots) > 10 {
		snapshots = snapshots[:10]
	}
	if err := s.savePolicySnapshots(r.Context(), snapshots, doc); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.appendAuditEntryNamed(r.Context(), "snapshot", "policy", snapshot.ID, snapshot.ID, policyActorID(r), policyRole(r), "capture policy snapshot "+snapshot.ID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, snapshot)
}

func (s *server) handleGetPolicyBundleSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	snapshotID := strings.TrimSpace(r.PathValue("id"))
	if snapshotID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "snapshot id required")
		return
	}
	snapshots, _, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, snap := range snapshots {
		if snap.ID == snapshotID {
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, snap)
			return
		}
	}
	writeErrorJSON(w, http.StatusNotFound, "snapshot not found")
}

func (s *server) loadPolicyBundles(ctx context.Context) (map[string]any, string, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(policyConfigScope), policyConfigID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return map[string]any{}, "", nil
		}
		return nil, "", err
	}
	raw := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := raw.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	updatedAt := ""
	if !doc.Updated.IsZero() {
		updatedAt = doc.Updated.UTC().Format(time.RFC3339)
	}
	return bundles, updatedAt, nil
}

func (s *server) loadPolicySnapshots(ctx context.Context) ([]policyBundleSnapshot, *configsvc.Document, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(policySnapshotsScope), policySnapshotsID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return []policyBundleSnapshot{}, nil, nil
		}
		return nil, nil, err
	}
	raw := normalizeJSON(doc.Data[policySnapshotsKey])
	if raw == nil {
		return []policyBundleSnapshot{}, doc, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	var snapshots []policyBundleSnapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return nil, nil, err
	}
	return snapshots, doc, nil
}


func (s *server) savePolicySnapshots(ctx context.Context, snapshots []policyBundleSnapshot, doc *configsvc.Document) error {
	if doc == nil {
		doc = &configsvc.Document{Scope: configsvc.Scope(policySnapshotsScope), ScopeID: policySnapshotsID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	payload, err := json.Marshal(snapshots)
	if err != nil {
		return fmt.Errorf("policy bundle save snapshots marshal: %w", err)
	}
	var data any
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("policy bundle save snapshots unmarshal: %w", err)
	}
	doc.Data[policySnapshotsKey] = data
	return s.configSvc.Set(ctx, doc)
}


func (s *server) loadPolicyBundlesWithDoc(ctx context.Context) (map[string]any, *configsvc.Document, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(policyConfigScope), policyConfigID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return map[string]any{}, nil, nil
		}
		return nil, nil, err
	}
	raw := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := raw.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	return bundles, doc, nil
}

func (s *server) capturePolicyBundleSnapshotWithBundles(ctx context.Context, bundles map[string]any, note string) (string, error) {
	if len(bundles) == 0 {
		return "", nil
	}
	hash, err := hashValue(bundles)
	if err != nil {
		return "", err
	}
	timestamp := time.Now().UTC().Format(time.RFC3339)
	id := timestamp + "-" + hash[:10]
	copyBundles, ok := deepCopy(bundles).(map[string]any)
	if !ok || copyBundles == nil {
		copyBundles = cloneBundleMap(bundles)
	}
	snapshot := policyBundleSnapshot{
		ID:        id,
		CreatedAt: timestamp,
		Note:      strings.TrimSpace(note),
		Bundles:   copyBundles,
	}
	snapshots, doc, err := s.loadPolicySnapshots(ctx)
	if err != nil {
		return "", err
	}
	snapshots = append([]policyBundleSnapshot{snapshot}, snapshots...)
	if len(snapshots) > 10 {
		snapshots = snapshots[:10]
	}
	if err := s.savePolicySnapshots(ctx, snapshots, doc); err != nil {
		return "", err
	}
	return id, nil
}


func (s *server) loadPolicyAudit(ctx context.Context) ([]policyAuditEntry, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(policyAuditScope), policyAuditID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return []policyAuditEntry{}, nil
		}
		return nil, err
	}
	raw := normalizeJSON(doc.Data[policyAuditKey])
	if raw == nil {
		return []policyAuditEntry{}, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var entries []policyAuditEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt > entries[j].CreatedAt })
	return entries, nil
}

func (s *server) appendPolicyAudit(ctx context.Context, entry policyAuditEntry) error {
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(policyAuditScope), policyAuditID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return fmt.Errorf("policy bundle append audit get document: %w", err)
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(policyAuditScope), ScopeID: policyAuditID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	entries := []policyAuditEntry{}
	raw := normalizeJSON(doc.Data[policyAuditKey])
	if raw != nil {
		if data, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(data, &entries)
		}
	}
	if entry.CreatedAt == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.ID == "" {
		payload := entry.Action + "|" + entry.ResourceType + "|" + entry.ResourceID + "|" + strings.Join(entry.BundleIDs, ",") + "|" + entry.CreatedAt
		sum := sha256.Sum256([]byte(payload))
		entry.ID = entry.CreatedAt + "-" + hex.EncodeToString(sum[:6])
	}
	entries = append([]policyAuditEntry{entry}, entries...)
	if len(entries) > 500 {
		entries = entries[:500]
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("policy bundle append audit marshal entries: %w", err)
	}
	var data any
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("policy bundle append audit unmarshal entries: %w", err)
	}
	doc.Data[policyAuditKey] = data
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return fmt.Errorf("policy bundle append audit save document: %w", err)
	}

	// Fan-out to SIEM exporter (non-blocking) after persistence.
	if s.auditExporter != nil {
		s.auditExporter.Send(auditEntryToSIEM(entry, s.tenant))
	}

	return nil
}

func (s *server) appendAuditEntryNamed(ctx context.Context, action, resourceType, resourceID, resourceName, actorID, role, message string) {
	_ = s.appendPolicyAudit(ctx, policyAuditEntry{
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		ActorID:      actorID,
		Role:         role,
		Message:      message,
	})
}

