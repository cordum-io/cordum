package safetykernel

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	scopeEvalTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_safety_scope_evaluations_total",
		Help: "Total scope evaluations by result (allow, deny, error)",
	}, []string{"result"})
	scopeViolationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_safety_scope_violations_total",
		Help: "Total scope violations by intent and violation type",
	}, []string{"intent", "type"})
)

// scopeFinding describes a specific scope violation found by the evaluator.
type scopeFinding struct {
	Type     string `json:"type"` // "scope_violation", "missing_input", "ambiguous_intent", "malformed_payload"
	Detail   string `json:"detail"`
	Item     string `json:"item,omitempty"`
	Category string `json:"category,omitempty"`
	Intent   string `json:"intent,omitempty"`
}

// evaluateScope runs the structured instruction-vs-cart scope evaluator.
// It returns true if the content violates scope (i.e., should be denied),
// along with findings describing what was detected.
func evaluateScope(cfg *config.ScopeConfig, content []byte) (bool, []scopeFinding) {
	if cfg == nil {
		return false, nil
	}

	// Parse the JSON payload.
	var payload map[string]interface{}
	if err := json.Unmarshal(content, &payload); err != nil {
		slog.Warn("scope evaluator: malformed JSON payload",
			"component", "safety", "error", err)
		scopeEvalTotal.WithLabelValues("error").Inc()
		if cfg.OnMissingInput == "allow" {
			return false, nil
		}
		return true, []scopeFinding{{
			Type:   "malformed_payload",
			Detail: fmt.Sprintf("failed to parse input as JSON: %s", truncateError(err)),
		}}
	}

	// Extract instruction.
	instruction, ok := resolveJSONPath(payload, cfg.InstructionPath)
	instructionStr := ""
	if ok {
		instructionStr, _ = instruction.(string)
	}
	if instructionStr == "" {
		slog.Debug("scope evaluator: instruction field missing or empty",
			"component", "safety", "path", cfg.InstructionPath)
		if cfg.OnMissingInput == "allow" {
			return false, nil
		}
		return true, []scopeFinding{{
			Type:   "missing_input",
			Detail: fmt.Sprintf("instruction field missing or empty at path %q", cfg.InstructionPath),
		}}
	}

	// Extract items array.
	itemsRaw, ok := resolveJSONPath(payload, cfg.ItemsPath)
	if !ok {
		slog.Debug("scope evaluator: items field missing",
			"component", "safety", "path", cfg.ItemsPath)
		if cfg.OnMissingInput == "allow" {
			return false, nil
		}
		return true, []scopeFinding{{
			Type:   "missing_input",
			Detail: fmt.Sprintf("items field missing at path %q", cfg.ItemsPath),
		}}
	}
	items, ok := itemsRaw.([]interface{})
	if !ok || len(items) == 0 {
		if cfg.OnMissingInput == "allow" {
			return false, nil
		}
		return true, []scopeFinding{{
			Type:   "missing_input",
			Detail: "items field is empty or not an array",
		}}
	}

	// Classify the intent from the instruction.
	intent := classifyIntent(instructionStr, cfg.AllowedCategories)
	if intent == "" {
		slog.Debug("scope evaluator: ambiguous intent",
			"component", "safety", "instruction", instructionStr)
		if cfg.OnAmbiguous == "allow" {
			return false, nil
		}
		return true, []scopeFinding{{
			Type:   "ambiguous_intent",
			Detail: fmt.Sprintf("cannot classify instruction %q into a known intent", truncateString(instructionStr, 100)),
		}}
	}

	// Get allowed categories for this intent.
	allowed := cfg.AllowedCategories[intent]
	if len(allowed) == 0 {
		// Empty allowed list means all categories are permitted for this intent.
		return false, nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, c := range allowed {
		allowedSet[normalizeCategory(c, cfg.Aliases)] = true
	}

	// Check each item's category against allowed set.
	categoryPath := cfg.CategoryPath
	if categoryPath == "" {
		categoryPath = "category"
	}
	namePath := cfg.NamePath
	if namePath == "" {
		namePath = "name"
	}

	var findings []scopeFinding
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		catRaw, _ := item[categoryPath].(string)
		nameRaw, _ := item[namePath].(string)
		normalized := normalizeCategory(catRaw, cfg.Aliases)
		if normalized == "" {
			findings = append(findings, scopeFinding{
				Type:   "scope_violation",
				Detail: fmt.Sprintf("item %q has no category", nameRaw),
				Item:   nameRaw,
			})
			continue
		}
		if !allowedSet[normalized] {
			findings = append(findings, scopeFinding{
				Type:     "scope_violation",
				Detail:   fmt.Sprintf("item %q has category %q which is not allowed for intent %q", nameRaw, catRaw, intent),
				Item:     nameRaw,
				Category: catRaw,
				Intent:   intent,
			})
		}
	}

	if len(findings) > 0 {
		slog.Info("scope evaluator: violations found",
			"component", "safety",
			"intent", intent,
			"violation_count", len(findings),
			"total_items", len(items),
		)
		scopeEvalTotal.WithLabelValues("deny").Inc()
		for _, f := range findings {
			scopeViolationTotal.WithLabelValues(intent, f.Type).Inc()
		}
		return true, findings
	}

	scopeEvalTotal.WithLabelValues("allow").Inc()
	return false, nil
}

// classifyIntent extracts the most likely intent keyword from the instruction
// by checking which allowed_categories keys appear in the instruction text.
// Returns empty string if no intent can be determined.
func classifyIntent(instruction string, allowedCategories map[string][]string) string {
	lower := normalizeText(instruction)
	var bestMatch string
	var bestLen int
	for intent := range allowedCategories {
		normalized := normalizeText(intent)
		if strings.Contains(lower, normalized) && len(normalized) > bestLen {
			bestMatch = intent
			bestLen = len(normalized)
		}
	}
	return bestMatch
}

// normalizeCategory applies alias resolution and text normalization.
func normalizeCategory(cat string, aliases map[string]string) string {
	norm := normalizeText(cat)
	if aliases != nil {
		if canonical, ok := aliases[norm]; ok {
			return normalizeText(canonical)
		}
	}
	return norm
}

// normalizeText lowercases and normalizes separators (hyphens, spaces → underscores).
func normalizeText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// resolveJSONPath traverses a map using a dot-separated path.
func resolveJSONPath(obj map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	var current interface{} = obj
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// truncateString limits a string to maxLen characters with ellipsis.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateError produces a safe error string for structured findings.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	return truncateString(err.Error(), 200)
}

// validateScopeConfig checks that a ScopeConfig is valid at compile time.
func validateScopeConfig(cfg *config.ScopeConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.InstructionPath == "" {
		return fmt.Errorf("scope: instruction_path is required")
	}
	if cfg.ItemsPath == "" {
		return fmt.Errorf("scope: items_path is required")
	}
	if cfg.OnMissingInput != "" && cfg.OnMissingInput != "deny" && cfg.OnMissingInput != "allow" {
		return fmt.Errorf("scope: on_missing_input must be 'deny' or 'allow', got %q", cfg.OnMissingInput)
	}
	if cfg.OnAmbiguous != "" && cfg.OnAmbiguous != "deny" && cfg.OnAmbiguous != "allow" {
		return fmt.Errorf("scope: on_ambiguous must be 'deny' or 'allow', got %q", cfg.OnAmbiguous)
	}
	return nil
}
