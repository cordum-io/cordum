package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

const (
	policySnapshotsScope = "system"
	policySnapshotsID    = "policy_snapshots"
	policySnapshotsKey   = "snapshots"
	policyAuditScope     = "system"
	policyAuditID        = "policy_audit"
	policyAuditKey       = "entries"
	policyStudioPrefix   = "secops/"
)

type policyBundleSnapshot struct {
	ID        string         `json:"id"`
	CreatedAt string         `json:"created_at"`
	Note      string         `json:"note,omitempty"`
	Bundles   map[string]any `json:"bundles"`
}

type policyBundleSnapshotSummary struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Note      string `json:"note,omitempty"`
}

type policyBundleSummary struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
	Author      string `json:"author,omitempty"`
	Message     string `json:"message,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}

type policyBundleDetail struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Enabled   bool   `json:"enabled"`
	Author    string `json:"author,omitempty"`
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type policyBundleUpsertRequest struct {
	Content string `json:"content"`
	Enabled *bool  `json:"enabled"`
	Author  string `json:"author"`
	Message string `json:"message"`
}

type policyBundleSimulateRequest struct {
	Request policyCheckRequest `json:"request"`
	Content string             `json:"content"`
}

type policyPublishRequest struct {
	BundleIDs []string `json:"bundle_ids"`
	Author    string   `json:"author"`
	Message   string   `json:"message"`
	Note      string   `json:"note"`
}

type policyRollbackRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Author     string `json:"author"`
	Message    string `json:"message"`
	Note       string `json:"note"`
}

type policyAuditEntry struct {
	ID             string   `json:"id"`
	Action         string   `json:"action"`
	ActorID        string   `json:"actor_id,omitempty"`
	Role           string   `json:"role,omitempty"`
	BundleIDs      []string `json:"bundle_ids,omitempty"`
	Message        string   `json:"message,omitempty"`
	SnapshotBefore string   `json:"snapshot_before,omitempty"`
	SnapshotAfter  string   `json:"snapshot_after,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

type policyRuleSource struct {
	FragmentID  string `json:"fragment_id"`
	PackID      string `json:"pack_id,omitempty"`
	OverlayName string `json:"overlay_name,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}

type policyRuleParseError struct {
	FragmentID string `json:"fragment_id"`
	Error      string `json:"error"`
}

func (s *server) handlePolicyBundles(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	bundles, updatedAt, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := bundleSummaryList(bundles)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bundles":    bundles,
		"items":      items,
		"updated_at": updatedAt,
	})
}

func (s *server) handlePolicyRules(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		if bundle == nil {
			continue
		}
		if !includeDisabled && !bundleEnabled(bundle) {
			continue
		}
		content := strings.TrimSpace(stringFromAny(bundle["content"]))
		if content == "" {
			continue
		}
		rules, err := rulesFromPolicyContent(fragmentID, bundle, content)
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
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handleGetPolicyBundle(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	bundleID := bundleIDFromRequest(r)
	if bundleID == "" {
		http.Error(w, "bundle id required", http.StatusBadRequest)
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, ok := bundles[bundleID]
	if !ok {
		http.Error(w, "bundle not found", http.StatusNotFound)
		return
	}
	bundle, _ := raw.(map[string]any)
	if bundle == nil {
		http.Error(w, "bundle invalid", http.StatusNotFound)
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
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handlePutPolicyBundle(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	bundleID := bundleIDFromRequest(r)
	if bundleID == "" {
		http.Error(w, "bundle id required", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(bundleID, policyStudioPrefix) {
		http.Error(w, "bundle id must start with secops/", http.StatusBadRequest)
		return
	}
	var body policyBundleUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		http.Error(w, "content required", http.StatusBadRequest)
		return
	}
	if _, err := config.ParseSafetyPolicy([]byte(content)); err != nil {
		http.Error(w, fmt.Sprintf("invalid policy content: %v", err), http.StatusBadRequest)
		return
	}

	doc, err := getConfigDoc(r.Context(), s.configSvc, policyConfigScope, policyConfigID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         bundleID,
		"updated_at": now,
	})
}

func (s *server) handleSimulatePolicyBundle(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	bundleID := bundleIDFromRequest(r)
	if bundleID == "" {
		http.Error(w, "bundle id required", http.StatusBadRequest)
		return
	}
	var body policyBundleSimulateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	checkReq, err := buildPolicyCheckRequest(r.Context(), &body.Request, s.configSvc, s.tenant)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := evaluatePolicyCheck(policy, snapshot, checkReq)
	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *server) handlePublishPolicyBundles(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var body policyPublishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	bundles, doc, err := s.loadPolicyBundlesWithDoc(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(bundles) == 0 {
		http.Error(w, "no bundles configured", http.StatusBadRequest)
		return
	}
	targets := resolvePublishTargets(bundles, body.BundleIDs)
	if len(targets) == 0 {
		http.Error(w, "no policy bundles to publish", http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	afterSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), bundles, body.Note)
	_ = s.appendPolicyAudit(r.Context(), policyAuditEntry{
		Action:         "publish",
		ActorID:        policyActorID(r),
		Role:           policyRole(r),
		BundleIDs:      targets,
		Message:        strings.TrimSpace(body.Message),
		SnapshotBefore: beforeSnapshot,
		SnapshotAfter:  afterSnapshot,
		CreatedAt:      now,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshot_before": beforeSnapshot,
		"snapshot_after":  afterSnapshot,
		"published":       targets,
	})
}

func (s *server) handleRollbackPolicyBundles(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var body policyRollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	snapshotID := strings.TrimSpace(body.SnapshotID)
	if snapshotID == "" {
		http.Error(w, "snapshot_id required", http.StatusBadRequest)
		return
	}
	bundles, doc, err := s.loadPolicyBundlesWithDoc(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	beforeSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), bundles, body.Note)

	snapshots, _, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "snapshot not found", http.StatusNotFound)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	afterSnapshot, _ := s.capturePolicyBundleSnapshotWithBundles(r.Context(), target.Bundles, body.Note)
	_ = s.appendPolicyAudit(r.Context(), policyAuditEntry{
		Action:         "rollback",
		ActorID:        policyActorID(r),
		Role:           policyRole(r),
		BundleIDs:      []string{},
		Message:        strings.TrimSpace(body.Message),
		SnapshotBefore: beforeSnapshot,
		SnapshotAfter:  afterSnapshot,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshot_before": beforeSnapshot,
		"snapshot_after":  afterSnapshot,
		"rollback_to":     snapshotID,
	})
}

func (s *server) handleListPolicyAudit(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	entries, err := s.loadPolicyAudit(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": entries})
}

func (s *server) handleListPolicyBundleSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	snapshots, _, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (s *server) handleCapturePolicyBundleSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hash, err := hashValue(bundles)
	if err != nil {
		http.Error(w, "failed to hash bundles", http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	snapshots = append([]policyBundleSnapshot{snapshot}, snapshots...)
	if len(snapshots) > 10 {
		snapshots = snapshots[:10]
	}
	if err := s.savePolicySnapshots(r.Context(), snapshots, doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *server) handleGetPolicyBundleSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	snapshotID := strings.TrimSpace(r.PathValue("id"))
	if snapshotID == "" {
		http.Error(w, "snapshot id required", http.StatusBadRequest)
		return
	}
	snapshots, _, err := s.loadPolicySnapshots(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, snap := range snapshots {
		if snap.ID == snapshotID {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snap)
			return
		}
	}
	http.Error(w, "snapshot not found", http.StatusNotFound)
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

func rulesFromPolicyContent(fragmentID string, bundle map[string]any, content string) ([]map[string]any, error) {
	var payload any
	if err := yaml.Unmarshal([]byte(content), &payload); err != nil {
		return nil, err
	}
	root, _ := normalizeJSON(payload).(map[string]any)
	if root == nil {
		return nil, nil
	}
	rules := normalizePolicyRules(root["rules"])
	if len(rules) == 0 {
		rules = legacyPolicyRules(root["tenants"])
	}
	source := policyRuleSourceFromBundle(fragmentID, bundle)
	for _, rule := range rules {
		rule["source"] = source
	}
	return rules, nil
}

func normalizePolicyRules(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, rule)
	}
	return out
}

func legacyPolicyRules(value any) []map[string]any {
	tenants, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := []map[string]any{}
	for tenant, raw := range tenants {
		data, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		mcp := data["mcp"]
		denyTopics := stringSliceFromAny(data["deny_topics"])
		for idx, topic := range denyTopics {
			match := map[string]any{
				"tenants": []string{tenant},
				"topics":  []string{topic},
			}
			if mcp != nil {
				match["mcp"] = mcp
			}
			out = append(out, map[string]any{
				"id":       fmt.Sprintf("legacy:%s:deny:%d", tenant, idx+1),
				"decision": "deny",
				"reason":   fmt.Sprintf("topic %q denied by tenant policy", topic),
				"match":    match,
			})
		}
		allowTopics := stringSliceFromAny(data["allow_topics"])
		for idx, topic := range allowTopics {
			match := map[string]any{
				"tenants": []string{tenant},
				"topics":  []string{topic},
			}
			if mcp != nil {
				match["mcp"] = mcp
			}
			out = append(out, map[string]any{
				"id":       fmt.Sprintf("legacy:%s:allow:%d", tenant, idx+1),
				"decision": "allow",
				"match":    match,
			})
		}
	}
	return out
}

func stringSliceFromAny(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func policyRuleSourceFromBundle(fragmentID string, bundle map[string]any) policyRuleSource {
	source := policyRuleSource{
		FragmentID: fragmentID,
	}
	if fragmentID != "" {
		parts := strings.SplitN(fragmentID, "/", 2)
		source.PackID = parts[0]
		if len(parts) > 1 {
			source.OverlayName = parts[1]
		}
	}
	source.Version = strings.TrimSpace(stringFromAny(bundle["version"]))
	source.InstalledAt = strings.TrimSpace(stringFromAny(bundle["installed_at"]))
	source.Sha256 = strings.TrimSpace(stringFromAny(bundle["sha256"]))
	return source
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
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
		return err
	}
	var data any
	if err := json.Unmarshal(payload, &data); err != nil {
		return err
	}
	doc.Data[policySnapshotsKey] = data
	return s.configSvc.Set(ctx, doc)
}

func bundleIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("bundle_id")); raw != "" {
		return strings.ReplaceAll(raw, "~", "/")
	}
	if raw := strings.TrimSpace(r.PathValue("id")); raw != "" {
		return strings.ReplaceAll(raw, "~", "/")
	}
	return ""
}

func bundleSummaryList(bundles map[string]any) []policyBundleSummary {
	if len(bundles) == 0 {
		return nil
	}
	keys := make([]string, 0, len(bundles))
	for key := range bundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]policyBundleSummary, 0, len(keys))
	for _, key := range keys {
		raw := bundles[key]
		bundle, _ := raw.(map[string]any)
		content := ""
		sha := ""
		if bundle != nil {
			content = strings.TrimSpace(stringFromAny(bundle["content"]))
			sha = strings.TrimSpace(stringFromAny(bundle["sha256"]))
		} else if raw != nil {
			content = strings.TrimSpace(stringFromAny(raw))
		}
		if sha == "" && content != "" {
			sum := sha256.Sum256([]byte(content))
			sha = hex.EncodeToString(sum[:])
		}
		source := "core"
		if strings.HasPrefix(key, policyStudioPrefix) {
			source = "secops"
		} else if strings.Contains(key, "/") {
			source = "pack"
		}
		author := ""
		message := ""
		createdAt := ""
		updatedAt := ""
		version := ""
		installedAt := ""
		if bundle != nil {
			author = strings.TrimSpace(stringFromAny(bundle["author"]))
			message = strings.TrimSpace(stringFromAny(bundle["message"]))
			createdAt = strings.TrimSpace(stringFromAny(bundle["created_at"]))
			updatedAt = strings.TrimSpace(stringFromAny(bundle["updated_at"]))
			version = strings.TrimSpace(stringFromAny(bundle["version"]))
			installedAt = strings.TrimSpace(stringFromAny(bundle["installed_at"]))
		}
		out = append(out, policyBundleSummary{
			ID:          key,
			Enabled:     bundleEnabled(bundle),
			Source:      source,
			Author:      author,
			Message:     message,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			Version:     version,
			InstalledAt: installedAt,
			Sha256:      sha,
		})
	}
	return out
}

func bundleEnabled(bundle map[string]any) bool {
	if bundle == nil {
		return true
	}
	if raw, ok := bundle["enabled"]; ok {
		switch v := raw.(type) {
		case bool:
			return v
		case string:
			return parseBool(v)
		default:
			return parseBool(fmt.Sprint(v))
		}
	}
	return true
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

func cloneBundleMap(bundles map[string]any) map[string]any {
	if bundles == nil {
		return map[string]any{}
	}
	copied, ok := deepCopy(bundles).(map[string]any)
	if !ok || copied == nil {
		return map[string]any{}
	}
	return copied
}

func buildPolicyFromBundles(bundles map[string]any) (*config.SafetyPolicy, string, error) {
	if len(bundles) == 0 {
		return nil, "", nil
	}
	keys := make([]string, 0, len(bundles))
	for key := range bundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	var merged *config.SafetyPolicy
	for _, key := range keys {
		content, ok := policyBundleContent(bundles[key])
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
		hasher.Write([]byte(content))
		policy, err := config.ParseSafetyPolicy([]byte(content))
		if err != nil {
			return nil, "", fmt.Errorf("parse policy bundle %q: %w", key, err)
		}
		merged = mergeSafetyPolicies(merged, policy)
	}
	if merged == nil {
		return nil, "", nil
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	return merged, "cfg:" + hash, nil
}

func policyBundleContent(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case map[string]any:
		if !bundleEnabled(v) {
			return "", false
		}
		if raw, ok := v["content"]; ok {
			return stringFromAny(raw), true
		}
		if raw, ok := v["policy"]; ok {
			return stringFromAny(raw), true
		}
		if raw, ok := v["data"]; ok {
			return stringFromAny(raw), true
		}
	}
	return "", false
}

func mergeSafetyPolicies(base, extra *config.SafetyPolicy) *config.SafetyPolicy {
	if base == nil {
		return cloneSafetyPolicy(extra)
	}
	if extra == nil {
		return cloneSafetyPolicy(base)
	}
	out := cloneSafetyPolicy(base)
	if out.Version == "" {
		out.Version = extra.Version
	}
	if out.DefaultTenant == "" {
		out.DefaultTenant = extra.DefaultTenant
	}
	out.Rules = append(out.Rules, extra.Rules...)
	out.Tenants = mergeTenantPolicies(out.Tenants, extra.Tenants)
	return out
}

func cloneSafetyPolicy(policy *config.SafetyPolicy) *config.SafetyPolicy {
	if policy == nil {
		return nil
	}
	out := &config.SafetyPolicy{
		Version:       policy.Version,
		DefaultTenant: policy.DefaultTenant,
		Rules:         append([]config.PolicyRule{}, policy.Rules...),
		Tenants:       map[string]config.TenantPolicy{},
	}
	if policy.Tenants != nil {
		for k, v := range policy.Tenants {
			out.Tenants[k] = cloneTenantPolicy(v)
		}
	}
	return out
}

func mergeTenantPolicies(base map[string]config.TenantPolicy, extra map[string]config.TenantPolicy) map[string]config.TenantPolicy {
	out := map[string]config.TenantPolicy{}
	for k, v := range base {
		out[k] = cloneTenantPolicy(v)
	}
	for tenant, add := range extra {
		current, ok := out[tenant]
		if !ok {
			out[tenant] = cloneTenantPolicy(add)
			continue
		}
		merged := current
		merged.AllowTopics = append(merged.AllowTopics, add.AllowTopics...)
		merged.DenyTopics = append(merged.DenyTopics, add.DenyTopics...)
		merged.AllowedRepoHosts = append(merged.AllowedRepoHosts, add.AllowedRepoHosts...)
		merged.DeniedRepoHosts = append(merged.DeniedRepoHosts, add.DeniedRepoHosts...)
		if add.MaxConcurrent > 0 && (merged.MaxConcurrent == 0 || add.MaxConcurrent < merged.MaxConcurrent) {
			merged.MaxConcurrent = add.MaxConcurrent
		}
		merged.MCP = mergeMCPPolicy(merged.MCP, add.MCP)
		out[tenant] = merged
	}
	return out
}

func cloneTenantPolicy(policy config.TenantPolicy) config.TenantPolicy {
	return config.TenantPolicy{
		AllowTopics:      append([]string{}, policy.AllowTopics...),
		DenyTopics:       append([]string{}, policy.DenyTopics...),
		AllowedRepoHosts: append([]string{}, policy.AllowedRepoHosts...),
		DeniedRepoHosts:  append([]string{}, policy.DeniedRepoHosts...),
		MaxConcurrent:    policy.MaxConcurrent,
		MCP:              policy.MCP,
	}
}

func mergeMCPPolicy(base, extra config.MCPPolicy) config.MCPPolicy {
	return config.MCPPolicy{
		AllowServers:   append(base.AllowServers, extra.AllowServers...),
		DenyServers:    append(base.DenyServers, extra.DenyServers...),
		AllowTools:     append(base.AllowTools, extra.AllowTools...),
		DenyTools:      append(base.DenyTools, extra.DenyTools...),
		AllowResources: append(base.AllowResources, extra.AllowResources...),
		DenyResources:  append(base.DenyResources, extra.DenyResources...),
		AllowActions:   append(base.AllowActions, extra.AllowActions...),
		DenyActions:    append(base.DenyActions, extra.DenyActions...),
	}
}

func evaluatePolicyCheck(policy *config.SafetyPolicy, snapshot string, req *pb.PolicyCheckRequest) *pb.PolicyCheckResponse {
	decision := pb.DecisionType_DECISION_TYPE_ALLOW
	reason := ""

	topic := strings.TrimSpace(req.GetTopic())
	tenant := strings.TrimSpace(req.GetTenant())
	meta := req.GetMeta()
	if tenant == "" && meta != nil {
		tenant = strings.TrimSpace(meta.GetTenantId())
	}

	defaultTenant := ""
	if policy != nil {
		defaultTenant = strings.TrimSpace(policy.DefaultTenant)
	}
	if tenant == "" {
		tenant = defaultTenant
	}
	if tenant == "" {
		tenant = "default"
	}

	if topic == "" {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "missing topic"}
	}
	if !strings.HasPrefix(topic, "job.") {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "unsupported topic"}
	}

	input := config.PolicyInput{
		Tenant: tenant,
		Topic:  topic,
		Labels: req.GetLabels(),
		Meta:   policyMetaFromRequest(req),
		MCP:    extractMCPRequest(req.GetLabels()),
	}
	input.SecretsPresent = secretsPresent(input.Meta, req.GetLabels())

	policyDecision := config.PolicyDecision{Decision: "allow"}
	if policy != nil {
		policyDecision = policy.Evaluate(input)
		if tp, ok := policy.Tenants[tenant]; ok {
			if ok, mcpReason := config.MCPAllowed(tp.MCP, input.MCP); !ok {
				policyDecision.Decision = "deny"
				policyDecision.Reason = mcpReason
			}
		}
	}

	constraints := toProtoConstraints(policyDecision.Constraints)
	switch policyDecision.Decision {
	case "deny":
		decision = pb.DecisionType_DECISION_TYPE_DENY
		reason = policyDecision.Reason
	case "require_approval":
		decision = pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
		reason = policyDecision.Reason
	case "throttle":
		decision = pb.DecisionType_DECISION_TYPE_THROTTLE
		reason = policyDecision.Reason
	case "allow_with_constraints":
		decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
	case "allow":
		if constraints != nil {
			decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
		}
	}

	if eff, ok := config.ParseEffectiveSafety(req.GetEffectiveConfig()); ok {
		if matchAny(eff.DeniedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic %q denied by effective config", topic)
		}
		if len(eff.AllowedTopics) > 0 && !matchAny(eff.AllowedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic %q not allowed by effective config", topic)
		}
		if ok, mcpReason := config.MCPAllowed(eff.MCP, input.MCP); !ok {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = mcpReason
		}
	}

	approvalRequired := policyDecision.ApprovalRequired || decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	approvalRef := ""
	if approvalRequired {
		approvalRef = req.GetJobId()
	}

	return &pb.PolicyCheckResponse{
		Decision:         decision,
		Reason:           reason,
		PolicySnapshot:   snapshot,
		RuleId:           policyDecision.RuleID,
		Constraints:      constraints,
		ApprovalRequired: approvalRequired,
		ApprovalRef:      approvalRef,
		Remediations:     toProtoRemediations(policyDecision.Remediations),
	}
}

func policyMetaFromRequest(req *pb.PolicyCheckRequest) config.PolicyMeta {
	meta := req.GetMeta()
	out := config.PolicyMeta{}
	if meta == nil {
		if req.GetPrincipalId() != "" {
			out.ActorID = req.GetPrincipalId()
		}
		return out
	}
	out.ActorID = meta.GetActorId()
	out.ActorType = actorTypeString(meta.GetActorType())
	out.IdempotencyKey = meta.GetIdempotencyKey()
	out.Capability = meta.GetCapability()
	out.RiskTags = append(out.RiskTags, meta.GetRiskTags()...)
	out.Requires = append(out.Requires, meta.GetRequires()...)
	out.PackID = meta.GetPackId()
	if out.ActorID == "" {
		out.ActorID = req.GetPrincipalId()
	}
	return out
}

func actorTypeString(val pb.ActorType) string {
	switch val {
	case pb.ActorType_ACTOR_TYPE_HUMAN:
		return "human"
	case pb.ActorType_ACTOR_TYPE_SERVICE:
		return "service"
	default:
		return ""
	}
}

func secretsPresent(meta config.PolicyMeta, labels map[string]string) bool {
	if labels != nil {
		if v := strings.TrimSpace(labels["secrets_present"]); v != "" {
			return v == "true" || v == "1" || strings.EqualFold(v, "yes")
		}
	}
	for _, tag := range meta.RiskTags {
		if strings.EqualFold(tag, "secrets") {
			return true
		}
	}
	return false
}

func extractMCPRequest(labels map[string]string) config.MCPRequest {
	if len(labels) == 0 {
		return config.MCPRequest{}
	}
	return config.MCPRequest{
		Server:   pickLabel(labels, "mcp.server", "mcp_server", "mcpServer"),
		Tool:     pickLabel(labels, "mcp.tool", "mcp_tool", "mcpTool"),
		Resource: pickLabel(labels, "mcp.resource", "mcp_resource", "mcpResource"),
		Action:   strings.ToLower(pickLabel(labels, "mcp.action", "mcp_action", "mcpAction")),
	}
}

func pickLabel(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if val, ok := labels[key]; ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func toProtoConstraints(c config.PolicyConstraints) *pb.PolicyConstraints {
	if isConstraintsEmpty(c) {
		return nil
	}
	return &pb.PolicyConstraints{
		Budgets: &pb.BudgetConstraints{
			MaxRuntimeMs:      c.Budgets.MaxRuntimeMs,
			MaxRetries:        c.Budgets.MaxRetries,
			MaxArtifactBytes:  c.Budgets.MaxArtifactBytes,
			MaxConcurrentJobs: c.Budgets.MaxConcurrentJobs,
		},
		Sandbox: &pb.SandboxProfile{
			Isolated:         c.Sandbox.Isolated,
			NetworkAllowlist: c.Sandbox.NetworkAllowlist,
			FsReadOnly:       c.Sandbox.FsReadOnly,
			FsReadWrite:      c.Sandbox.FsReadWrite,
		},
		Toolchain: &pb.ToolchainConstraints{
			AllowedTools:    c.Toolchain.AllowedTools,
			AllowedCommands: c.Toolchain.AllowedCommands,
		},
		Diff: &pb.DiffConstraints{
			MaxFiles:      c.Diff.MaxFiles,
			MaxLines:      c.Diff.MaxLines,
			DenyPathGlobs: c.Diff.DenyPathGlobs,
		},
		RedactionLevel: c.RedactionLevel,
	}
}

func toProtoRemediations(remediations []config.PolicyRemediation) []*pb.PolicyRemediation {
	if len(remediations) == 0 {
		return nil
	}
	out := make([]*pb.PolicyRemediation, 0, len(remediations))
	for _, rem := range remediations {
		r := rem
		out = append(out, &pb.PolicyRemediation{
			Id:                    r.ID,
			Title:                 r.Title,
			Summary:               r.Summary,
			ReplacementTopic:      r.ReplacementTopic,
			ReplacementCapability: r.ReplacementCapability,
			AddLabels:             r.AddLabels,
			RemoveLabels:          append([]string{}, r.RemoveLabels...),
		})
	}
	return out
}

func isConstraintsEmpty(c config.PolicyConstraints) bool {
	return c.Budgets.MaxRuntimeMs == 0 && c.Budgets.MaxRetries == 0 && c.Budgets.MaxArtifactBytes == 0 && c.Budgets.MaxConcurrentJobs == 0 &&
		!c.Sandbox.Isolated && len(c.Sandbox.NetworkAllowlist) == 0 && len(c.Sandbox.FsReadOnly) == 0 && len(c.Sandbox.FsReadWrite) == 0 &&
		len(c.Toolchain.AllowedTools) == 0 && len(c.Toolchain.AllowedCommands) == 0 &&
		c.Diff.MaxFiles == 0 && c.Diff.MaxLines == 0 && len(c.Diff.DenyPathGlobs) == 0 &&
		strings.TrimSpace(c.RedactionLevel) == ""
}

func matchAny(patterns []string, value string) bool {
	if value == "" {
		return false
	}
	for _, pat := range patterns {
		if configMatch(pat, value) {
			return true
		}
	}
	return false
}

func configMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, _ := path.Match(pattern, value)
	return ok
}

func validateBundles(bundles map[string]any) error {
	if len(bundles) == 0 {
		return nil
	}
	keys := make([]string, 0, len(bundles))
	for key := range bundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		content, ok := policyBundleContent(bundles[key])
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		if _, err := config.ParseSafetyPolicy([]byte(content)); err != nil {
			return fmt.Errorf("invalid policy bundle %q: %w", key, err)
		}
	}
	return nil
}

func resolvePublishTargets(bundles map[string]any, requested []string) []string {
	targets := []string{}
	seen := map[string]struct{}{}
	if len(requested) == 0 {
		for key := range bundles {
			if strings.HasPrefix(key, policyStudioPrefix) {
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					targets = append(targets, key)
				}
			}
		}
	} else {
		for _, raw := range requested {
			key := strings.TrimSpace(raw)
			if key == "" || !strings.HasPrefix(key, policyStudioPrefix) {
				continue
			}
			if _, ok := bundles[key]; !ok {
				continue
			}
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				targets = append(targets, key)
			}
		}
	}
	sort.Strings(targets)
	return targets
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
			return err
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
		payload := entry.Action + "|" + strings.Join(entry.BundleIDs, ",") + "|" + entry.CreatedAt
		sum := sha256.Sum256([]byte(payload))
		entry.ID = entry.CreatedAt + "-" + hex.EncodeToString(sum[:6])
	}
	entries = append([]policyAuditEntry{entry}, entries...)
	if len(entries) > 100 {
		entries = entries[:100]
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	var data any
	if err := json.Unmarshal(payload, &data); err != nil {
		return err
	}
	doc.Data[policyAuditKey] = data
	return s.configSvc.Set(ctx, doc)
}

func policyActorID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if auth := authFromRequest(r); auth != nil && auth.PrincipalID != "" {
		return auth.PrincipalID
	}
	return strings.TrimSpace(r.Header.Get("X-Principal-Id"))
}

func policyRole(r *http.Request) string {
	if r == nil {
		return ""
	}
	if auth := authFromRequest(r); auth != nil && auth.Role != "" {
		return normalizeRole(auth.Role)
	}
	return normalizeRole(strings.TrimSpace(r.Header.Get("X-Principal-Role")))
}
