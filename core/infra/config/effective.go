package config

import (
	"encoding/json"
	"log/slog"
)

// EffectiveConfigEnvVar carries the JSON-encoded effective config on job env.
const EffectiveConfigEnvVar = "CORDUM_EFFECTIVE_CONFIG"

// ParseEffectiveSafety extracts safety config from the effective config payload.
func ParseEffectiveSafety(payload []byte) (SafetyConfig, bool) {
	if len(payload) == 0 {
		return SafetyConfig{}, false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil {
		slog.Warn("config: failed to parse effective safety", "err", err)
		return SafetyConfig{}, false
	}
	if raw, ok := top["safety"]; ok && len(raw) > 0 {
		var cfg SafetyConfig
		if err := json.Unmarshal(raw, &cfg); err == nil {
			return cfg, true
		}
	}
	if raw, ok := top["data"]; ok && len(raw) > 0 {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil {
			if sraw, ok := nested["safety"]; ok && len(sraw) > 0 {
				var cfg SafetyConfig
				if err := json.Unmarshal(sraw, &cfg); err == nil {
					return cfg, true
				}
			}
		}
	}
	return SafetyConfig{}, false
}
