package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/coretex-os/core/configsvc"
	"gopkg.in/yaml.v3"
)

const (
	policySnapshotsScope = "system"
	policySnapshotsID    = "policy_snapshots"
	policySnapshotsKey   = "snapshots"
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
	bundles, updatedAt, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bundles":    bundles,
		"updated_at": updatedAt,
	})
}

func (s *server) handlePolicyRules(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
		return
	}
	bundles, _, err := s.loadPolicyBundles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

func (s *server) handleListPolicyBundleSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		http.Error(w, "config service unavailable", http.StatusServiceUnavailable)
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
				"reason":   fmt.Sprintf("topic '%s' denied by tenant policy", topic),
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
