package workflow

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func parseSwitchCases(step *Step, scope map[string]any) ([]switchCase, error) {
	if step == nil || step.Input == nil {
		return nil, fmt.Errorf("switch input.cases required")
	}
	rawCases, ok := step.Input["cases"]
	if !ok {
		return nil, fmt.Errorf("switch input.cases required")
	}
	evaledCases, err := evalTemplates(rawCases, scope)
	if err != nil {
		return nil, fmt.Errorf("switch cases eval failed: %w", err)
	}
	switch typed := evaledCases.(type) {
	case []any:
		out := make([]switchCase, 0, len(typed))
		for idx, entry := range typed {
			parsed, err := parseSwitchCaseEntry(entry)
			if err != nil {
				return nil, fmt.Errorf("switch case %d invalid: %w", idx, err)
			}
			out = append(out, parsed)
		}
		return out, nil
	case map[string]any:
		out := make([]switchCase, 0, len(typed))
		for matchVal, targetRaw := range typed {
			stepID := strings.TrimSpace(fmt.Sprint(targetRaw))
			if stepID == "" {
				return nil, fmt.Errorf("switch case %q has empty target step", matchVal)
			}
			out = append(out, switchCase{MatchValue: matchVal, StepID: stepID})
		}
		return out, nil
	case nil:
		return []switchCase{}, nil
	default:
		return nil, fmt.Errorf("switch input.cases must be array or map, got %T", evaledCases)
	}
}

func parseSwitchCaseEntry(value any) (switchCase, error) {
	var raw map[string]any
	switch typed := value.(type) {
	case map[string]any:
		raw = typed
	case map[string]string:
		raw = make(map[string]any, len(typed))
		for k, v := range typed {
			raw[k] = v
		}
	default:
		return switchCase{}, fmt.Errorf("case must be object, got %T", value)
	}
	matchValue, matchKey, hasMatch := firstSwitchValue(raw, "when", "match", "value")
	if !hasMatch {
		return switchCase{}, fmt.Errorf("case requires one of when/match/value")
	}
	if matchKey == "when" {
		// "when" is treated as a match literal, not an expression.
		matchValue = strings.TrimSpace(fmt.Sprint(matchValue))
	}
	targetRaw, _, hasTarget := firstSwitchValue(raw, "next", "step", "target")
	if !hasTarget {
		return switchCase{}, fmt.Errorf("case requires one of next/step/target")
	}
	stepID := strings.TrimSpace(fmt.Sprint(targetRaw))
	if stepID == "" {
		return switchCase{}, fmt.Errorf("case target step id required")
	}
	return switchCase{MatchValue: matchValue, StepID: stepID}, nil
}

func firstSwitchValue(values map[string]any, keys ...string) (any, string, bool) {
	for _, key := range keys {
		if val, ok := values[key]; ok {
			return val, key, true
		}
	}
	return nil, "", false
}

func parseSwitchDefault(step *Step, scope map[string]any) (string, error) {
	if step == nil || step.Input == nil {
		return "", nil
	}
	rawDefault, ok := step.Input["default"]
	if !ok {
		rawDefault, ok = step.Input["default_step"]
		if !ok {
			return "", nil
		}
	}
	evaledDefault, err := evalTemplates(rawDefault, scope)
	if err != nil {
		return "", fmt.Errorf("switch default eval failed: %w", err)
	}
	if evaledDefault == nil {
		return "", nil
	}
	defaultStepID := strings.TrimSpace(fmt.Sprint(evaledDefault))
	return defaultStepID, nil
}

func switchValueEquals(actual, expected any) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	if actualNum, ok := switchComparableNumber(actual); ok {
		if expectedNum, ok := switchComparableNumber(expected); ok {
			return actualNum == expectedNum
		}
	}
	switch actualTyped := actual.(type) {
	case string:
		if expectedTyped, ok := expected.(string); ok {
			return strings.TrimSpace(actualTyped) == strings.TrimSpace(expectedTyped)
		}
	case bool:
		if expectedTyped, ok := expected.(bool); ok {
			return actualTyped == expectedTyped
		}
	}
	return fmt.Sprint(actual) == fmt.Sprint(expected)
}

func switchComparableNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		if parsed, err := v.Float64(); err == nil {
			return parsed, true
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func isSwitchBranchNotTaken(sr *StepRun) bool {
	if sr == nil || sr.Status != StepStatusCancelled || sr.Error == nil {
		return false
	}
	reason, _ := sr.Error["reason"].(string)
	return strings.TrimSpace(reason) == switchBranchNotTakenReason
}
