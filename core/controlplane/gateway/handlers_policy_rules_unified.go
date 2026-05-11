package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/policy"
)

// rulesListResponse is the unified GET /api/v1/policy/rules envelope.
// `items` carries fully-populated `policy.Rule` values (with the
// previously-Unknown `type` discriminator now correctly set per source
// bucket). `has_more` + `next_cursor` mirror Backend 5b's pagination
// contract for `policy.Decision`.
type rulesListResponse struct {
	Items      []*policy.Rule `json:"items"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

const (
	rulesDefaultLimit = 100
	rulesMaxLimit     = 500
)

// rulesListFilters carries the parsed query params. All filters are
// AND-combined when applied to the merged result set. Empty fields
// pass everything through.
type rulesListFilters struct {
	ruleType   policy.RuleType
	scopeKind  policy.RuleScopeKind
	scopeValue string
	status     policy.RuleStatus
	packID     string
	limit      int
	cursor     string
}

// handlePolicyRulesUnified replaces the legacy `handlePolicyRules` GET
// handler so the response carries the full `policy.Rule` envelope (with
// `type` populated) instead of the legacy `{decision, match, reason,
// tier, source, firing_last_7d}` shape that left the dashboard
// rendering all rules as Type=Unknown (Yaron's 2026-05-10 bug report).
//
// Sources merged (per Phase 1 inventory):
//  1. RuleStore.ListRules (Backend 5c — interactively-authored rules).
//  2. YAML bundles → policybundles.RulesFromPolicyContent → Type=input.
//  3. YAML bundles → policybundles.OutputRulesFromPolicyContent → Type=output.
//  4. Velocity bundles → listVelocityRules → Type=velocity.
//
// Edge rules surface only via (1) — no clean YAML-pack mapping exists
// today; documented for D11 cut-over awareness.
func (s *server) handlePolicyRulesUnified(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, s.configSvc) {
		return
	}

	filters, err := parseRulesListFilters(r.URL.Query())
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeInternalError(w, r, "policy operation", err)
		return
	}

	merged, err := s.mergeRulesAcrossSources(r.Context(), bundles)
	if err != nil {
		writeInternalError(w, r, "policy operation", err)
		return
	}

	filtered := applyRulesListFilters(merged, filters)
	page, hasMore, nextCursor := paginateRules(filtered, filters)

	s.attachRuleFiringLast7d(r, page)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, rulesListResponse{
		Items:      page,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

// attachRuleFiringLast7d populates `FiringLast7d` on each rule in the page via
// a single bulk job-history scan. Tenant-scoped per the request; degrades
// silently when the tenant header is missing or jobStore is unavailable so the
// rule listing itself never 500s on analytics-only failures.
func (s *server) attachRuleFiringLast7d(r *http.Request, page []*policy.Rule) {
	if len(page) == 0 {
		return
	}
	tenant := strings.TrimSpace(tenantFromRequest(r))
	if tenant == "" {
		return
	}
	ids := make([]string, 0, len(page))
	for _, rule := range page {
		if rule != nil && rule.ID != "" {
			ids = append(ids, rule.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	firing, err := s.ruleFiringLast7d(r.Context(), tenant, ids, time.Now().UTC())
	if err != nil {
		slog.WarnContext(r.Context(), "policy rules firing analytics unavailable",
			"tenant", tenant, "rules", len(ids), "err", err)
		return
	}
	for _, rule := range page {
		if rule == nil {
			continue
		}
		rule.FiringLast7d = firing[rule.ID]
	}
}

func parseRulesListFilters(q map[string][]string) (rulesListFilters, error) {
	out := rulesListFilters{limit: rulesDefaultLimit}
	if raw := strings.TrimSpace(firstQueryValue(q, "type")); raw != "" {
		t, err := policy.ParseRuleType(raw)
		if err != nil {
			return out, fmt.Errorf("invalid type filter: %w", err)
		}
		out.ruleType = t
	}
	if raw := strings.TrimSpace(firstQueryValue(q, "scope_kind")); raw != "" {
		k, err := policy.ParseRuleScopeKind(raw)
		if err != nil {
			return out, fmt.Errorf("invalid scope_kind filter: %w", err)
		}
		out.scopeKind = k
	}
	out.scopeValue = strings.TrimSpace(firstQueryValue(q, "scope_value"))
	if raw := strings.TrimSpace(firstQueryValue(q, "status")); raw != "" {
		st, err := policy.ParseRuleStatus(raw)
		if err != nil {
			return out, fmt.Errorf("invalid status filter: %w", err)
		}
		out.status = st
	}
	out.packID = strings.TrimSpace(firstQueryValue(q, "pack_id"))
	if raw := strings.TrimSpace(firstQueryValue(q, "limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return out, fmt.Errorf("invalid limit: must be integer")
		}
		if n < 1 || n > rulesMaxLimit {
			return out, fmt.Errorf("invalid limit: must be 1..%d", rulesMaxLimit)
		}
		out.limit = n
	}
	out.cursor = strings.TrimSpace(firstQueryValue(q, "cursor"))
	return out, nil
}

func firstQueryValue(q map[string][]string, key string) string {
	if vs, ok := q[key]; ok && len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// mergeRulesAcrossSources walks the 4 sources and returns the merged
// list. Order: RuleStore first (interactively authored, generally
// freshest), then YAML buckets (input → output → velocity). The final
// sort by ID makes the order deterministic for cursor pagination.
func (s *server) mergeRulesAcrossSources(ctx context.Context, bundles map[string]any) ([]*policy.Rule, error) {
	out := make([]*policy.Rule, 0, 64)

	if s.policyRuleStore != nil {
		stored, err := s.policyRuleStore.ListRules(ctx)
		if err != nil {
			return nil, fmt.Errorf("list rules from RuleStore: %w", err)
		}
		out = append(out, stored...)
	}

	fragmentIDs := sortedBundleFragmentIDs(bundles)
	for _, fragmentID := range fragmentIDs {
		raw := bundles[fragmentID]
		bundle, _ := raw.(map[string]any)
		if bundle != nil && !policybundles.BundleEnabled(bundle) {
			// Disabled bundles are excluded — same posture as the legacy
			// handler's `!includeDisabled` branch (Yaron's UI never
			// surfaces them either way).
			continue
		}
		content := bundleContent(raw)
		if content == "" {
			continue
		}
		bundleMeta := bundle
		if bundleMeta == nil {
			bundleMeta = map[string]any{}
		}

		// Skip velocity bundles in the input/output walk — they ship
		// through listVelocityRules below to hit the velocityRuleResponse
		// path that handles the Velocity payload shape.
		if strings.HasPrefix(fragmentID, velocityRuleBundlePrefix) {
			continue
		}

		// Input bucket: `rules:` (or legacy `tenants:` deny/allow).
		if inputs, err := policybundles.RulesFromPolicyContent(fragmentID, bundleMeta, content); err == nil {
			for _, item := range inputs {
				rule, terr := translatePolicyRuleMap(item, policy.RuleTypeInput)
				if terr == nil && rule != nil {
					out = append(out, rule)
				}
			}
		}

		// Output bucket: `output_rules:`.
		if outputs, err := policybundles.OutputRulesFromPolicyContent(fragmentID, bundleMeta, content); err == nil {
			for _, item := range outputs {
				rule, terr := translatePolicyRuleMap(item, policy.RuleTypeOutput)
				if terr == nil && rule != nil {
					out = append(out, rule)
				}
			}
		}
	}

	// Velocity bundles ship via the listVelocityRules path.
	velocityItems, _ := listVelocityRules(bundles)
	for _, v := range velocityItems {
		rule, err := translateVelocityResponse(v, bundles)
		if err == nil && rule != nil {
			out = append(out, rule)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func sortedBundleFragmentIDs(bundles map[string]any) []string {
	ids := make([]string, 0, len(bundles))
	for id := range bundles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func bundleContent(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"content", "policy", "data"} {
			if s := strings.TrimSpace(policybundles.StringFromAny(v[key])); s != "" {
				return s
			}
		}
	}
	return ""
}

// translatePolicyRuleMap converts a policybundles-emitted rule map into
// a unified policy.Rule. Pack source attribution is read off the map's
// `source` field (already populated by `PolicyRuleSourceFromBundle`).
func translatePolicyRuleMap(legacy map[string]any, ruleType policy.RuleType) (*policy.Rule, error) {
	source := packSourceFromLegacyMap(legacy)
	return policy.LegacyMapToUnifiedRule(legacy, ruleType, source)
}

func packSourceFromLegacyMap(legacy map[string]any) *policy.PackSource {
	raw, ok := legacy["source"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case policybundles.PolicyRuleSource:
		return &policy.PackSource{
			FragmentID:  typed.FragmentID,
			Tier:        typed.Tier,
			PackID:      typed.PackID,
			OverlayName: typed.OverlayName,
			Version:     typed.Version,
			InstalledAt: typed.InstalledAt,
			Sha256:      typed.Sha256,
		}
	case map[string]any:
		// Defensive — the live RulesFromPolicyContent emits the typed
		// struct, but a future refactor (or yaml round-trip via JSON)
		// could land it as a map[string]any. Read the same 6 fields.
		return &policy.PackSource{
			FragmentID:  policybundles.StringFromAny(typed["fragment_id"]),
			Tier:        policybundles.StringFromAny(typed["tier"]),
			PackID:      policybundles.StringFromAny(typed["pack_id"]),
			OverlayName: policybundles.StringFromAny(typed["overlay_name"]),
			Version:     policybundles.StringFromAny(typed["version"]),
			InstalledAt: policybundles.StringFromAny(typed["installed_at"]),
			Sha256:      policybundles.StringFromAny(typed["sha256"]),
		}
	}
	return nil
}

// translateVelocityResponse converts the velocity-bucket response shape
// into a unified Rule with Type=velocity. The velocity payload (window,
// key, threshold) lands under match.velocity so downstream evaluators
// see a single contiguous match-aware payload.
func translateVelocityResponse(v velocityRuleResponse, bundles map[string]any) (*policy.Rule, error) {
	matchPayload := map[string]any{
		"velocity": map[string]any{
			"window":    v.Window,
			"key":       v.Key,
			"threshold": v.Threshold,
		},
	}
	// Topics from the original match block stay top-level under match
	// for evaluators that filter velocity rules by topic.
	if len(v.Match.Topics) > 0 {
		matchPayload["topics"] = v.Match.Topics
	}
	matchJSON, err := json.Marshal(matchPayload)
	if err != nil {
		return nil, err
	}
	decideJSON, err := json.Marshal(map[string]any{
		"decision": v.Decision,
		"reason":   v.Reason,
	})
	if err != nil {
		return nil, err
	}

	bundleID := velocityRuleBundlePrefix + v.ID
	source := velocityPackSource(bundleID, bundles)

	return &policy.Rule{
		ID:          v.ID,
		Name:        firstNonEmpty(v.Name, v.ID),
		Type:        policy.RuleTypeVelocity,
		Scope:       policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Status:      policy.RuleStatusPublished,
		Version:     "v1",
		Audit:       policy.AuditMetadata{PackSource: source},
		Match:       matchJSON,
		Decide:      decideJSON,
		Description: v.Reason,
	}, nil
}

func velocityPackSource(bundleID string, bundles map[string]any) *policy.PackSource {
	bundle, _ := bundles[bundleID].(map[string]any)
	if bundle == nil {
		return nil
	}
	source := policybundles.PolicyRuleSourceFromBundle(bundleID, bundle)
	return &policy.PackSource{
		FragmentID:  source.FragmentID,
		Tier:        source.Tier,
		PackID:      source.PackID,
		OverlayName: source.OverlayName,
		Version:     source.Version,
		InstalledAt: source.InstalledAt,
		Sha256:      source.Sha256,
	}
}

// applyRulesListFilters AND-combines all parsed filters across the
// merged result set. Each filter is independently a no-op when its
// field is the zero value (empty string / RuleType / etc).
func applyRulesListFilters(rules []*policy.Rule, f rulesListFilters) []*policy.Rule {
	out := rules[:0:0]
	for _, rule := range rules {
		if !rulesListFilterMatches(rule, f) {
			continue
		}
		out = append(out, rule)
	}
	return out
}

func rulesListFilterMatches(rule *policy.Rule, f rulesListFilters) bool {
	if f.ruleType != "" && rule.Type != f.ruleType {
		return false
	}
	if f.scopeKind != "" && rule.Scope.Kind != f.scopeKind {
		return false
	}
	if f.scopeValue != "" && rule.Scope.Value != f.scopeValue {
		return false
	}
	if f.status != "" && rule.Status != f.status {
		return false
	}
	if f.packID != "" {
		ps := rule.Audit.PackSource
		if ps == nil || ps.PackID != f.packID {
			return false
		}
	}
	return true
}

// paginateRules applies cursor + limit to the post-filter rule list.
// The cursor is opaque base64 of the last-returned rule id; pagination
// is stable because the merged list is sorted by id ASC. Concurrent
// inserts that lexically come AFTER the cursor surface on the next
// page; inserts that come BEFORE are skipped (acceptable per
// `feedback_no_silent_overwrite` since the dataset converges across
// pages, no row is duplicated).
func paginateRules(rules []*policy.Rule, f rulesListFilters) ([]*policy.Rule, bool, string) {
	startIdx := 0
	if f.cursor != "" {
		afterID, err := decodeRulesCursor(f.cursor)
		if err == nil {
			for i, rule := range rules {
				if rule.ID > afterID {
					startIdx = i
					break
				}
				if i == len(rules)-1 {
					startIdx = len(rules)
				}
			}
		}
	}
	end := startIdx + f.limit
	if end > len(rules) {
		end = len(rules)
	}
	page := rules[startIdx:end]
	hasMore := end < len(rules)
	cursor := ""
	if hasMore && len(page) > 0 {
		cursor = encodeRulesCursor(page[len(page)-1].ID)
	}
	return page, hasMore, cursor
}

func encodeRulesCursor(lastID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(lastID))
}

func decodeRulesCursor(cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
