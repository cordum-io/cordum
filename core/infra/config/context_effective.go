package config

import (
	"encoding/json"
	"path"
	"strings"
)

// ParseEffectiveContext extracts context config from an effective config payload.
func ParseEffectiveContext(payload []byte) (ContextConfig, bool) {
	if len(payload) == 0 {
		return ContextConfig{}, false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil {
		return ContextConfig{}, false
	}
	if raw, ok := top["context"]; ok && len(raw) > 0 {
		var cfg ContextConfig
		if err := json.Unmarshal(raw, &cfg); err == nil {
			return cfg, true
		}
	}
	if raw, ok := top["data"]; ok && len(raw) > 0 {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil {
			if cRaw, ok := nested["context"]; ok && len(cRaw) > 0 {
				var cfg ContextConfig
				if err := json.Unmarshal(cRaw, &cfg); err == nil {
					return cfg, true
				}
			}
		}
	}
	return ContextConfig{}, false
}

// ParseEffectiveContextMap extracts context config from a merged config map.
func ParseEffectiveContextMap(data map[string]any) (ContextConfig, bool) {
	if len(data) == 0 {
		return ContextConfig{}, false
	}
	raw, ok := data["context"]
	if !ok || raw == nil {
		return ContextConfig{}, false
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return ContextConfig{}, false
	}
	var cfg ContextConfig
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return ContextConfig{}, false
	}
	return cfg, true
}

// MemoryIDAllowed validates a memory_id against allow/deny policy.
func MemoryIDAllowed(cfg ContextConfig, memoryID string) (bool, string) {
	id := strings.TrimSpace(memoryID)
	if id == "" {
		return true, ""
	}
	if matchAny(cfg.DeniedMemoryIDs, id) {
		return false, "memory id denied by policy"
	}
	if len(cfg.AllowedMemoryIDs) > 0 && !matchAny(cfg.AllowedMemoryIDs, id) {
		return false, "memory id not allowed by policy"
	}
	return true, ""
}

func matchAny(patterns []string, value string) bool {
	if value == "" {
		return false
	}
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if ok, _ := path.Match(pat, value); ok {
			return true
		}
	}
	return false
}
