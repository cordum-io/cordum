package config

import (
	"encoding/json"
	"log"
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
		log.Printf("config: failed to parse effective safety: %v", err)
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

// EffectiveSafetyFromEnv extracts safety config from job env if present.
func EffectiveSafetyFromEnv(env map[string]string) (SafetyConfig, bool) {
	if len(env) == 0 {
		return SafetyConfig{}, false
	}
	payload := env[EffectiveConfigEnvVar]
	if payload == "" {
		return SafetyConfig{}, false
	}
	return ParseEffectiveSafety([]byte(payload))
}
