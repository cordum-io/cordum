package policybundles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/infra/config"
	"gopkg.in/yaml.v3"
)

// BundleIDFromRequest extracts a bundle ID from the HTTP request.
func BundleIDFromRequest(r *http.Request) string {
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

// BundleSummaryList produces a sorted list of bundle summaries.
func BundleSummaryList(bundles map[string]any) []PolicyBundleSummary {
	if len(bundles) == 0 {
		return nil
	}
	keys := make([]string, 0, len(bundles))
	for key := range bundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]PolicyBundleSummary, 0, len(keys))
	for _, key := range keys {
		raw := bundles[key]
		bundle, _ := raw.(map[string]any)
		content := ""
		sha := ""
		if bundle != nil {
			content = strings.TrimSpace(StringFromAny(bundle["content"]))
			sha = strings.TrimSpace(StringFromAny(bundle["sha256"]))
		} else if raw != nil {
			content = strings.TrimSpace(StringFromAny(raw))
		}
		if sha == "" && content != "" {
			sum := sha256.Sum256([]byte(content))
			sha = hex.EncodeToString(sum[:])
		}
		ruleCount := 0
		if content != "" {
			var parsed struct {
				Rules []any `yaml:"rules"`
			}
			if yaml.Unmarshal([]byte(content), &parsed) == nil {
				ruleCount = len(parsed.Rules)
			}
		}
		source := "core"
		if strings.HasPrefix(key, PolicyStudioPrefix) {
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
			author = strings.TrimSpace(StringFromAny(bundle["author"]))
			message = strings.TrimSpace(StringFromAny(bundle["message"]))
			createdAt = strings.TrimSpace(StringFromAny(bundle["created_at"]))
			updatedAt = strings.TrimSpace(StringFromAny(bundle["updated_at"]))
			version = strings.TrimSpace(StringFromAny(bundle["version"]))
			installedAt = strings.TrimSpace(StringFromAny(bundle["installed_at"]))
		}
		out = append(out, PolicyBundleSummary{
			ID:          key,
			Enabled:     BundleEnabled(bundle),
			Source:      source,
			Author:      author,
			Message:     message,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			Version:     version,
			InstalledAt: installedAt,
			Sha256:      sha,
			RuleCount:   ruleCount,
		})
	}
	return out
}

// BundleEnabled returns whether a bundle map is enabled (defaults to true).
func BundleEnabled(bundle map[string]any) bool {
	if bundle == nil {
		return true
	}
	if raw, ok := bundle["enabled"]; ok {
		switch v := raw.(type) {
		case bool:
			return v
		case string:
			return ParseBool(v)
		default:
			return ParseBool(fmt.Sprint(v))
		}
	}
	return true
}

// CloneBundleMap deep-copies a bundle map.
func CloneBundleMap(bundles map[string]any) map[string]any {
	if bundles == nil {
		return map[string]any{}
	}
	copied, ok := packs.DeepCopy(bundles).(map[string]any)
	if !ok || copied == nil {
		return map[string]any{}
	}
	return copied
}

// BuildPolicyFromBundles merges all enabled bundles into a single SafetyPolicy.
func BuildPolicyFromBundles(bundles map[string]any) (*config.SafetyPolicy, string, error) {
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
		content, ok := PolicyBundleContent(bundles[key])
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		sanitizedContent := SanitizePolicyBundleYAML(content)
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
		hasher.Write([]byte(sanitizedContent))
		policy, err := config.ParseSafetyPolicy([]byte(sanitizedContent))
		if err != nil {
			return nil, "", fmt.Errorf("parse policy bundle %q: %w", key, err)
		}
		merged = MergeSafetyPolicies(merged, policy)
	}
	if merged == nil {
		return nil, "", nil
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	return merged, "cfg:" + hash, nil
}

// PolicyBundleContent extracts the policy content string from a bundle value.
func PolicyBundleContent(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case map[string]any:
		if !BundleEnabled(v) {
			return "", false
		}
		if raw, ok := v["content"]; ok {
			return StringFromAny(raw), true
		}
		if raw, ok := v["policy"]; ok {
			return StringFromAny(raw), true
		}
		if raw, ok := v["data"]; ok {
			return StringFromAny(raw), true
		}
	}
	return "", false
}

// SanitizePolicyBundleYAML normalizes and sanitizes policy YAML content.
func SanitizePolicyBundleYAML(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var payload any
	if err := yaml.Unmarshal([]byte(content), &payload); err != nil {
		return content
	}
	normalized := packs.NormalizeJSON(payload)
	if normalized == nil {
		return content
	}
	sanitized := SanitizePolicyBundleValue(normalized)
	if sanitized == nil {
		return content
	}
	encoded, err := yaml.Marshal(sanitized)
	if err != nil {
		return content
	}
	return string(encoded)
}

// SanitizePolicyBundleValue recursively sanitizes policy bundle data.
func SanitizePolicyBundleValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, raw := range typed {
			sanitized := SanitizePolicyBundleValue(raw)
			if sanitized == nil {
				continue
			}
			if str, ok := sanitized.(string); ok {
				if strings.TrimSpace(str) == "" {
					switch key {
					case "default_decision", "default_tenant", "fail_mode":
						continue
					}
				}
			}
			out[key] = sanitized
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, raw := range typed {
			sanitized := SanitizePolicyBundleValue(raw)
			if sanitized == nil {
				continue
			}
			out = append(out, sanitized)
		}
		return out
	default:
		return value
	}
}

// ValidateBundles validates all enabled policy bundles can be parsed.
func ValidateBundles(bundles map[string]any) error {
	if len(bundles) == 0 {
		return nil
	}
	keys := make([]string, 0, len(bundles))
	for key := range bundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		content, ok := PolicyBundleContent(bundles[key])
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		sanitizedContent := SanitizePolicyBundleYAML(content)
		if _, err := config.ParseSafetyPolicy([]byte(sanitizedContent)); err != nil {
			return fmt.Errorf("invalid policy bundle %q: %w", key, err)
		}
	}
	return nil
}

// ResolvePublishTargets resolves which bundles to publish.
func ResolvePublishTargets(bundles map[string]any, requested []string) []string {
	targets := []string{}
	seen := map[string]struct{}{}
	if len(requested) == 0 {
		for key := range bundles {
			if strings.HasPrefix(key, PolicyStudioPrefix) {
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					targets = append(targets, key)
				}
			}
		}
	} else {
		for _, raw := range requested {
			key := strings.TrimSpace(raw)
			if key == "" || !strings.HasPrefix(key, PolicyStudioPrefix) {
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
