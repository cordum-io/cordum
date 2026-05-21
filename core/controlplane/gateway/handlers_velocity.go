package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/timeutil"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

const (
	velocityRuleBundlePrefix     = "velocity/"
	defaultTeamVelocityRuleLimit = int64(20)
	velocityRulesUpgradeURL      = licensing.DefaultUpgradeURL
)

var (
	velocityRuleIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	velocityRuleLabelKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type velocityRuleMatch struct {
	Topics   []string `json:"topics,omitempty" yaml:"topics,omitempty"`
	Tenants  []string `json:"tenants,omitempty" yaml:"tenants,omitempty"`
	RiskTags []string `json:"risk_tags,omitempty" yaml:"risk_tags,omitempty"`
}

type velocityRuleUpsertRequest struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name"`
	Match     velocityRuleMatch `json:"match"`
	Window    string            `json:"window"`
	Key       string            `json:"key"`
	Threshold int               `json:"threshold"`
	Decision  string            `json:"decision"`
	Reason    string            `json:"reason"`
	Enabled   *bool             `json:"enabled,omitempty"`
	Author    string            `json:"author,omitempty"`
	Message   string            `json:"message,omitempty"`
}

type velocityRuleResponse struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Match     velocityRuleMatch `json:"match"`
	Window    string            `json:"window"`
	Key       string            `json:"key"`
	Threshold int               `json:"threshold"`
	Decision  string            `json:"decision"`
	Reason    string            `json:"reason"`
	Enabled   bool              `json:"enabled"`
	CreatedAt string            `json:"created_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

type velocityRuleDefinition struct {
	ID            string
	Name          string
	Match         velocityRuleMatch
	Window        string
	WindowSeconds int
	Key           string
	Threshold     int
	Decision      string
	Reason        string
	Enabled       *bool
	Author        string
	Message       string
}

type velocityRuleStatsResponse struct {
	ID                 string  `json:"id"`
	HitCount24h        int64   `json:"hit_count_24h"`
	HitRate24h         float64 `json:"hit_rate_24h"`
	CurrentWindowCount int64   `json:"current_window_count"`
	CurrentWindowMax   int64   `json:"current_window_max"`
	ActiveBuckets      int     `json:"active_buckets"`
	ExceededBuckets    int     `json:"exceeded_buckets"`
	LastTriggered      string  `json:"last_triggered,omitempty"`
	HourlyHits         []int64 `json:"hourly_hits,omitempty"`
}

type velocityRuleBundleDoc struct {
	Version string                   `yaml:"version"`
	Rules   []velocityRuleBundleRule `yaml:"rules"`
}

type velocityRuleBundleRule struct {
	ID       string                 `yaml:"id"`
	Match    *velocityRuleMatch     `yaml:"match,omitempty"`
	Velocity velocityRuleYAMLConfig `yaml:"velocity"`
	Decision string                 `yaml:"decision"`
	Reason   string                 `yaml:"reason"`
}

type velocityRuleYAMLConfig struct {
	MaxRequests   int    `yaml:"max_requests"`
	WindowSeconds int    `yaml:"window_seconds"`
	Key           string `yaml:"key"`
}

func (s *server) handleVelocityRules(w http.ResponseWriter, r *http.Request) {
	if !s.requireFeatureEntitlement(w, "velocity_rules", "velocity rules require an Enterprise license") {
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, s.configSvc) {
		return
	}
	bundles, updatedAt, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}

	items, parseErrors := listVelocityRules(bundles)
	resp := map[string]any{
		"items":       items,
		"count":       len(items),
		"limit":       s.velocityRuleLimit(),
		"updated_at":  updatedAt,
		"upgrade_url": velocityRulesUpgradeURL,
	}
	if len(parseErrors) > 0 {
		resp["errors"] = parseErrors
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleCreateVelocityRule(w http.ResponseWriter, r *http.Request) {
	if !s.requireFeatureEntitlement(w, "velocity_rules", "velocity rules require an Enterprise license") {
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyWrite, []string{"admin"}, s.configSvc) {
		return
	}

	var body velocityRuleUpsertRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	def, err := normalizeVelocityRuleRequest(body, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeVelocityRuleInvalid, err.Error())
		return
	}

	doc, bundles, err := s.loadVelocityRuleBundleDoc(r.Context())
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}

	bundleID := velocityRuleBundleID(def.ID)
	if _, exists := bundles[bundleID]; exists {
		writeJSONError(w, http.StatusConflict, errorCodeVelocityRuleConflict, "velocity rule already exists")
		return
	}
	if limitErr := s.velocityRuleLimitError(int64(countVelocityRuleBundles(bundles) + 1)); limitErr != nil {
		writeTierLimitJSON(w, limitErr)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	bundle, err := velocityRuleBundleMap(def, now, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeVelocityRuleInvalid, err.Error())
		return
	}
	bundles[bundleID] = bundle
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	_ = s.appendPolicyAudit(r.Context(), policybundles.PolicyAuditEntry{
		Action:       "create",
		ResourceType: "velocity_rule",
		ResourceID:   def.ID,
		ResourceName: def.Name,
		ActorID:      policybundles.PolicyActorID(r),
		Role:         policybundles.PolicyRole(r),
		Message:      "create velocity rule " + def.ID,
	})

	resp, err := velocityRuleFromBundle(bundleID, bundle)
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, resp)
}

func (s *server) handleVelocityRuleStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireFeatureEntitlement(w, "velocity_rules", "velocity rules require an Enterprise license") {
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, s.configSvc) {
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	rules, parseErrors := listVelocityRules(bundles)
	stats := make([]velocityRuleStatsResponse, len(rules))
	for i, rule := range rules {
		stats[i] = velocityRuleStatsResponse{ID: rule.ID}
	}
	if s.jobStore != nil && s.jobStore.Client() != nil && len(rules) > 0 {
		stats, err = collectVelocityRuleStats(r.Context(), s.jobStore.Client(), rules)
		if err != nil {
			writeInternalError(w, r, "velocity rule stats", err)
			return
		}
	}

	topRules := append([]velocityRuleStatsResponse{}, stats...)
	sort.SliceStable(topRules, func(i, j int) bool {
		if topRules[i].HitCount24h == topRules[j].HitCount24h {
			return topRules[i].ID < topRules[j].ID
		}
		return topRules[i].HitCount24h > topRules[j].HitCount24h
	})
	if len(topRules) > 10 {
		topRules = topRules[:10]
	}

	resp := map[string]any{
		"items":        stats,
		"top_rules":    topRules,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if len(parseErrors) > 0 {
		resp["errors"] = parseErrors
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handlePutVelocityRule(w http.ResponseWriter, r *http.Request) {
	if !s.requireFeatureEntitlement(w, "velocity_rules", "velocity rules require an Enterprise license") {
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyWrite, []string{"admin"}, s.configSvc) {
		return
	}

	ruleID, err := normalizeVelocityRuleID(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeVelocityRuleInvalid, err.Error())
		return
	}

	var body velocityRuleUpsertRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	def, err := normalizeVelocityRuleRequest(body, ruleID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeVelocityRuleInvalid, err.Error())
		return
	}

	doc, bundles, err := s.loadVelocityRuleBundleDoc(r.Context())
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}

	bundleID := velocityRuleBundleID(ruleID)
	existingRaw, exists := bundles[bundleID]
	if !exists {
		writeJSONError(w, http.StatusNotFound, errorCodeVelocityRuleConflict, "velocity rule not found")
		return
	}
	existingBundle, _ := existingRaw.(map[string]any)
	createdAt := strings.TrimSpace(policybundles.StringFromAny(existingBundle["created_at"]))
	now := time.Now().UTC().Format(time.RFC3339)
	bundle, err := velocityRuleBundleMap(def, now, createdAt)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeVelocityRuleInvalid, err.Error())
		return
	}
	if def.Enabled == nil && existingBundle != nil {
		if enabledRaw, ok := existingBundle["enabled"]; ok {
			bundle["enabled"] = enabledRaw
		}
	}

	bundles[bundleID] = bundle
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	_ = s.appendPolicyAudit(r.Context(), policybundles.PolicyAuditEntry{
		Action:       "edit",
		ResourceType: "velocity_rule",
		ResourceID:   def.ID,
		ResourceName: def.Name,
		ActorID:      policybundles.PolicyActorID(r),
		Role:         policybundles.PolicyRole(r),
		Message:      "edit velocity rule " + def.ID,
	})

	resp, err := velocityRuleFromBundle(bundleID, bundle)
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleDeleteVelocityRule(w http.ResponseWriter, r *http.Request) {
	if !s.requireFeatureEntitlement(w, "velocity_rules", "velocity rules require an Enterprise license") {
		return
	}
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyWrite, []string{"admin"}, s.configSvc) {
		return
	}

	ruleID, err := normalizeVelocityRuleID(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeVelocityRuleInvalid, err.Error())
		return
	}

	doc, bundles, err := s.loadVelocityRuleBundleDoc(r.Context())
	if err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	bundleID := velocityRuleBundleID(ruleID)
	if _, exists := bundles[bundleID]; !exists {
		writeJSONError(w, http.StatusNotFound, errorCodeVelocityRuleConflict, "velocity rule not found")
		return
	}

	delete(bundles, bundleID)
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeInternalError(w, r, "velocity rule operation", err)
		return
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	_ = s.appendPolicyAudit(r.Context(), policybundles.PolicyAuditEntry{
		Action:       "delete",
		ResourceType: "velocity_rule",
		ResourceID:   ruleID,
		ResourceName: ruleID,
		ActorID:      policybundles.PolicyActorID(r),
		Role:         policybundles.PolicyRole(r),
		Message:      "delete velocity rule " + ruleID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func listVelocityRules(bundles map[string]any) ([]velocityRuleResponse, []policybundles.PolicyRuleParseError) {
	items := make([]velocityRuleResponse, 0)
	parseErrors := make([]policybundles.PolicyRuleParseError, 0)
	for bundleID, rawBundle := range bundles {
		if !strings.HasPrefix(bundleID, velocityRuleBundlePrefix) {
			continue
		}
		item, err := velocityRuleFromBundle(bundleID, rawBundle)
		if err != nil {
			parseErrors = append(parseErrors, policybundles.PolicyRuleParseError{
				FragmentID: bundleID,
				Error:      err.Error(),
			})
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items, parseErrors
}

func velocityRuleFromBundle(bundleID string, rawBundle any) (velocityRuleResponse, error) {
	content := ""
	switch typed := rawBundle.(type) {
	case string:
		content = strings.TrimSpace(typed)
	case map[string]any:
		content = strings.TrimSpace(policybundles.StringFromAny(typed["content"]))
		if content == "" {
			content = strings.TrimSpace(policybundles.StringFromAny(typed["policy"]))
		}
		if content == "" {
			content = strings.TrimSpace(policybundles.StringFromAny(typed["data"]))
		}
	}
	if content == "" {
		return velocityRuleResponse{}, fmt.Errorf("velocity rule bundle %q content missing", bundleID)
	}
	policy, err := config.ParseSafetyPolicy([]byte(content))
	if err != nil {
		return velocityRuleResponse{}, fmt.Errorf("parse velocity rule bundle %q: %w", bundleID, err)
	}
	if policy == nil || len(policy.Rules) != 1 {
		return velocityRuleResponse{}, fmt.Errorf("velocity rule bundle %q must contain exactly one rule", bundleID)
	}
	rule := policy.Rules[0]
	if rule.Velocity == nil {
		return velocityRuleResponse{}, fmt.Errorf("velocity rule bundle %q missing velocity config", bundleID)
	}
	bundle, _ := rawBundle.(map[string]any)
	if bundle == nil {
		bundle = map[string]any{}
	}
	respID := strings.TrimPrefix(bundleID, velocityRuleBundlePrefix)
	if strings.TrimSpace(rule.ID) != "" && strings.TrimSpace(rule.ID) != respID {
		return velocityRuleResponse{}, fmt.Errorf("velocity rule bundle %q rule id mismatch", bundleID)
	}

	return velocityRuleResponse{
		ID:        respID,
		Name:      strings.TrimSpace(firstNonEmpty(policybundles.StringFromAny(bundle["name"]), rule.ID)),
		Match:     velocityRuleMatchFromPolicyMatch(rule.Match),
		Window:    (time.Duration(rule.Velocity.WindowSeconds) * time.Second).String(),
		Key:       strings.TrimSpace(rule.Velocity.Key),
		Threshold: rule.Velocity.MaxRequests,
		Decision:  normalizeVelocityDecision(rule.Decision),
		Reason:    strings.TrimSpace(rule.Reason),
		Enabled:   policybundles.BundleEnabled(bundle),
		CreatedAt: strings.TrimSpace(policybundles.StringFromAny(bundle["created_at"])),
		UpdatedAt: strings.TrimSpace(policybundles.StringFromAny(bundle["updated_at"])),
	}, nil
}

func normalizeVelocityRuleRequest(body velocityRuleUpsertRequest, pathID string) (velocityRuleDefinition, error) {
	id := strings.TrimSpace(body.ID)
	if pathID != "" {
		if id != "" && !strings.EqualFold(id, pathID) {
			return velocityRuleDefinition{}, fmt.Errorf("rule id in path and body must match")
		}
		id = pathID
	}
	normalizedID, err := normalizeVelocityRuleID(id)
	if err != nil {
		return velocityRuleDefinition{}, err
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		return velocityRuleDefinition{}, fmt.Errorf("name required")
	}
	if len(name) > 120 {
		return velocityRuleDefinition{}, fmt.Errorf("name must be 120 characters or fewer")
	}

	match := velocityRuleMatch{
		Topics:   sanitizeStringSlice(body.Match.Topics),
		Tenants:  sanitizeStringSlice(body.Match.Tenants),
		RiskTags: sanitizeStringSlice(body.Match.RiskTags),
	}

	window := strings.TrimSpace(body.Window)
	if window == "" {
		return velocityRuleDefinition{}, fmt.Errorf("window required")
	}
	duration, err := time.ParseDuration(window)
	if err != nil {
		return velocityRuleDefinition{}, fmt.Errorf("invalid window: %w", err)
	}
	if duration <= 0 {
		return velocityRuleDefinition{}, fmt.Errorf("window must be greater than zero")
	}
	if duration%time.Second != 0 {
		return velocityRuleDefinition{}, fmt.Errorf("window must resolve to whole seconds")
	}
	windowSeconds := int(duration / time.Second)
	if windowSeconds <= 0 || windowSeconds > 24*60*60 {
		return velocityRuleDefinition{}, fmt.Errorf("window must be between 1s and 24h")
	}

	key := strings.TrimSpace(body.Key)
	if key == "" {
		return velocityRuleDefinition{}, fmt.Errorf("key required")
	}
	if err := validateVelocityKey(key); err != nil {
		return velocityRuleDefinition{}, err
	}

	if body.Threshold < 1 {
		return velocityRuleDefinition{}, fmt.Errorf("threshold must be at least 1")
	}

	decision, err := validateVelocityDecision(body.Decision)
	if err != nil {
		return velocityRuleDefinition{}, err
	}

	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		return velocityRuleDefinition{}, fmt.Errorf("reason required")
	}

	return velocityRuleDefinition{
		ID:            normalizedID,
		Name:          name,
		Match:         match,
		Window:        duration.String(),
		WindowSeconds: windowSeconds,
		Key:           key,
		Threshold:     body.Threshold,
		Decision:      decision,
		Reason:        reason,
		Enabled:       body.Enabled,
		Author:        strings.TrimSpace(body.Author),
		Message:       strings.TrimSpace(body.Message),
	}, nil
}

func velocityRuleBundleMap(def velocityRuleDefinition, updatedAt, createdAt string) (map[string]any, error) {
	content, err := marshalVelocityRuleBundle(def)
	if err != nil {
		return nil, err
	}
	if createdAt == "" {
		createdAt = updatedAt
	}
	sum := sha256.Sum256([]byte(content))
	bundle := map[string]any{
		"name":       def.Name,
		"content":    content,
		"created_at": createdAt,
		"updated_at": updatedAt,
		"sha256":     hex.EncodeToString(sum[:]),
	}
	if def.Author != "" {
		bundle["author"] = def.Author
	}
	if def.Message != "" {
		bundle["message"] = def.Message
	}
	if def.Enabled != nil {
		bundle["enabled"] = *def.Enabled
	}
	return bundle, nil
}

func marshalVelocityRuleBundle(def velocityRuleDefinition) (string, error) {
	doc := velocityRuleBundleDoc{
		Version: "1",
		Rules: []velocityRuleBundleRule{
			{
				ID:       def.ID,
				Match:    velocityRuleMatchPointer(def.Match),
				Velocity: velocityRuleYAMLConfig{MaxRequests: def.Threshold, WindowSeconds: def.WindowSeconds, Key: def.Key},
				Decision: def.Decision,
				Reason:   def.Reason,
			},
		},
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal velocity rule bundle: %w", err)
	}
	content := policybundles.SanitizePolicyBundleYAML(string(data))
	if _, err := config.ParseSafetyPolicy([]byte(content)); err != nil {
		return "", fmt.Errorf("invalid velocity rule content: %w", err)
	}
	return content, nil
}

func velocityRuleMatchPointer(match velocityRuleMatch) *velocityRuleMatch {
	if len(match.Topics) == 0 && len(match.Tenants) == 0 && len(match.RiskTags) == 0 {
		return nil
	}
	copyMatch := velocityRuleMatch{
		Topics:   append([]string{}, match.Topics...),
		Tenants:  append([]string{}, match.Tenants...),
		RiskTags: append([]string{}, match.RiskTags...),
	}
	return &copyMatch
}

func velocityRuleMatchFromPolicyMatch(match config.PolicyMatch) velocityRuleMatch {
	return velocityRuleMatch{
		Topics:   append([]string{}, match.Topics...),
		Tenants:  append([]string{}, match.Tenants...),
		RiskTags: append([]string{}, match.RiskTags...),
	}
}

func normalizeVelocityRuleID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", fmt.Errorf("rule id required")
	}
	if !velocityRuleIDPattern.MatchString(id) {
		return "", fmt.Errorf("rule id must match %s", velocityRuleIDPattern.String())
	}
	return id, nil
}

func velocityRuleBundleID(ruleID string) string {
	return velocityRuleBundlePrefix + ruleID
}

func sanitizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func validateVelocityDecision(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow", "permit":
		return "allow", nil
	case "deny", "block":
		return "deny", nil
	case "require_approval", "require-approval", "require_human":
		return "require_approval", nil
	case "allow_with_constraints", "allow-with-constraints":
		return "allow_with_constraints", nil
	case "throttle":
		return "throttle", nil
	default:
		return "", fmt.Errorf("decision must be one of allow, deny, require_approval, allow_with_constraints, throttle")
	}
}

func normalizeVelocityDecision(raw string) string {
	decision, err := validateVelocityDecision(raw)
	if err != nil {
		return "deny"
	}
	return decision
}

func validateVelocityKey(raw string) error {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("key segments must be non-empty")
		}
		switch part {
		case "actor_id", "actor_type", "tenant", "topic", "pack_id", "capability":
			continue
		}
		if strings.HasPrefix(part, "labels.") {
			labelKey := strings.TrimSpace(strings.TrimPrefix(part, "labels."))
			if labelKey == "" || !velocityRuleLabelKeyPattern.MatchString(labelKey) {
				return fmt.Errorf("labels key segment %q is invalid", part)
			}
			continue
		}
		return fmt.Errorf("unsupported key segment %q", part)
	}
	return nil
}

func countVelocityRuleBundles(bundles map[string]any) int {
	count := 0
	for bundleID := range bundles {
		if strings.HasPrefix(bundleID, velocityRuleBundlePrefix) {
			count++
		}
	}
	return count
}

func collectVelocityRuleStats(ctx context.Context, client redis.UniversalClient, rules []velocityRuleResponse) ([]velocityRuleStatsResponse, error) {
	statsByID := make(map[string]*velocityRuleStatsResponse, len(rules))
	rulesByID := make(map[string]velocityRuleResponse, len(rules))
	items := make([]velocityRuleStatsResponse, 0, len(rules))
	for _, rule := range rules {
		item := velocityRuleStatsResponse{ID: rule.ID, HourlyHits: make([]int64, 24)}
		items = append(items, item)
		statsByID[rule.ID] = &items[len(items)-1]
		rulesByID[rule.ID] = rule
	}

	keysByRule, err := scanVelocityRuleKeys(ctx, client, rulesByID)
	if err != nil {
		return nil, err
	}
	if len(keysByRule) == 0 {
		return items, nil
	}

	now := time.Now().UTC()
	nowUnix := strconv.FormatInt(now.Unix(), 10)
	dayStart := strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10)

	type ruleKeyCommands struct {
		ruleID    string
		threshold int64
		hits24h   *redis.IntCmd
		current   *redis.IntCmd
		latest    *redis.ZSliceCmd
		samples   *redis.ZSliceCmd
	}

	pipe := client.Pipeline()
	cmds := make([]ruleKeyCommands, 0)
	for ruleID, keys := range keysByRule {
		rule := rulesByID[ruleID]
		windowDuration, err := time.ParseDuration(rule.Window)
		if err != nil {
			return nil, fmt.Errorf("parse velocity rule window for %s: %w", ruleID, err)
		}
		windowStart := strconv.FormatInt(now.Add(-windowDuration).Unix(), 10)
		for _, key := range keys {
			cmds = append(cmds, ruleKeyCommands{
				ruleID:    ruleID,
				threshold: int64(rule.Threshold),
				hits24h:   pipe.ZCount(ctx, key, dayStart, nowUnix),
				current:   pipe.ZCount(ctx, key, windowStart, nowUnix),
				latest:    pipe.ZRevRangeWithScores(ctx, key, 0, 0),
				samples:   pipe.ZRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{Min: dayStart, Max: nowUnix}),
			})
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	for _, cmd := range cmds {
		stats := statsByID[cmd.ruleID]
		if stats == nil {
			continue
		}
		hits24h, err := cmd.hits24h.Result()
		if err != nil {
			return nil, err
		}
		current, err := cmd.current.Result()
		if err != nil {
			return nil, err
		}
		stats.HitCount24h += hits24h
		stats.CurrentWindowCount += current
		if current > stats.CurrentWindowMax {
			stats.CurrentWindowMax = current
		}
		if current > 0 {
			stats.ActiveBuckets++
		}
		if current > cmd.threshold {
			stats.ExceededBuckets++
		}
		latestEntries, err := cmd.latest.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if len(latestEntries) > 0 {
			triggeredAt := timeutil.FromSeconds(int64(latestEntries[0].Score))
			if triggeredAt > stats.LastTriggered {
				stats.LastTriggered = triggeredAt
			}
		}
		samples, err := cmd.samples.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for _, sample := range samples {
			if len(stats.HourlyHits) != 24 {
				stats.HourlyHits = make([]int64, 24)
			}
			scoreUnix := int64(sample.Score)
			bucketIdx := int((scoreUnix - now.Add(-24*time.Hour).Unix()) / int64(time.Hour.Seconds()))
			if bucketIdx < 0 {
				bucketIdx = 0
			}
			if bucketIdx >= len(stats.HourlyHits) {
				bucketIdx = len(stats.HourlyHits) - 1
			}
			stats.HourlyHits[bucketIdx]++
		}
	}

	for _, item := range items {
		stats := statsByID[item.ID]
		if stats == nil {
			continue
		}
		stats.HitRate24h = float64(stats.HitCount24h) / 24.0
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ID == items[j].ID {
			return false
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func scanVelocityRuleKeys(ctx context.Context, client redis.UniversalClient, rules map[string]velocityRuleResponse) (map[string][]string, error) {
	out := make(map[string][]string)
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, "cordum:velocity:*", 200).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			parts := strings.SplitN(key, ":", 4)
			if len(parts) != 4 {
				continue
			}
			ruleID := strings.TrimSpace(parts[2])
			if _, ok := rules[ruleID]; !ok {
				continue
			}
			out[ruleID] = append(out[ruleID], key)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return out, nil
}

func (s *server) velocityRuleLimit() int64 {
	entitlements := s.currentEntitlements()
	if !entitlements.VelocityRules {
		return 0
	}
	if entitlements.Limits != nil {
		for _, key := range []string{"velocity_rule_count", "velocity_rules"} {
			if value, ok := entitlements.Limits[key]; ok {
				return value
			}
		}
	}
	switch s.resolvedPlan() {
	case licensing.PlanEnterprise:
		return licensing.Unlimited
	case licensing.PlanTeam:
		return defaultTeamVelocityRuleLimit
	default:
		return 0
	}
}

func (s *server) velocityRuleLimitError(current int64) *licensing.TierLimitError {
	allowed := s.velocityRuleLimit()
	if allowed == licensing.Unlimited || (allowed > 0 && current <= allowed) {
		return nil
	}
	return &licensing.TierLimitError{
		Limit:      "velocity_rules",
		Current:    current,
		Allowed:    allowed,
		UpgradeURL: velocityRulesUpgradeURL,
	}
}

func (s *server) loadVelocityRuleBundleDoc(ctx context.Context) (*configsvc.Document, map[string]any, error) {
	doc, err := getConfigDoc(ctx, s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return nil, nil, err
		}
		doc = &configsvc.Document{Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := packs.NormalizeJSON(doc.Data[packs.PolicyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	return doc, bundles, nil
}
