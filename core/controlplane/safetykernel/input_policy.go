package safetykernel

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

// compiledInputRule mirrors compiledOutputRule for pre-execution content scanning.
type compiledInputRule struct {
	id           string
	tier         string
	selector     config.PolicySelector
	decision     string // "deny", "require_approval"
	reason       string
	severity     string
	tenants      []string
	topics       []string
	capabilities []string
	riskTags     []string
	contentTypes []string
	scanners     []string
	patterns     []compiledOutputPattern // reuse the same compiled pattern type
	keywords     []string
	maxBytes     int64
	scope        *config.ScopeConfig // structured instruction-vs-cart evaluator
}

// inputEvaluateRequest is the internal request for input rule evaluation.
type inputEvaluateRequest struct {
	tenant       string
	topic        string
	capabilities []string
	riskTags     []string
	contentType  string
	content      []byte
	inputSize    int64
}

// compileInputRules mirrors compileOutputRules for input-side content scanning.
func compileInputRules(policy *config.SafetyPolicy, registry map[string]OutputScanner) []compiledInputRule {
	if policy == nil || len(policy.InputRules) == 0 {
		return nil
	}
	out := make([]compiledInputRule, 0, len(policy.InputRules))
	for _, rule := range policy.InputRules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}

		decision := strings.ToLower(strings.TrimSpace(rule.Decision))
		switch decision {
		case "deny", "require_approval", "require-approval", "require_human":
			// valid
		default:
			slog.Warn("safety-kernel: skipping input rule, invalid decision", "rule", rule.ID, "decision", rule.Decision)
			continue
		}

		maxBytes := rule.Match.MaxInputBytes
		if rule.Match.InputSizeGt > maxBytes {
			maxBytes = rule.Match.InputSizeGt
		}

		// Compile regex patterns with ReDoS protection (reuses validateRegexComplexity).
		patterns := make([]compiledOutputPattern, 0, len(rule.Match.ContentPatterns))
		for _, raw := range rule.Match.ContentPatterns {
			pat := strings.TrimSpace(raw)
			if pat == "" {
				continue
			}
			if err := validateRegexComplexity(pat); err != nil {
				slog.Warn("safety-kernel: rejecting input rule pattern", "rule", rule.ID, "pattern", pat, "err", err)
				regexRejectedTotal.Inc()
				continue
			}
			compiled, err := regexp.Compile(pat)
			if err != nil {
				slog.Warn("safety-kernel: skipping input rule pattern", "rule", rule.ID, "pattern", pat, "err", err)
				continue
			}
			patterns = append(patterns, compiledOutputPattern{raw: pat, re: compiled})
		}
		if len(rule.Match.ContentPatterns) > 0 && len(patterns) == 0 {
			continue
		}

		// Validate scope config at compile time.
		if rule.Match.Scope != nil {
			if err := validateScopeConfig(rule.Match.Scope); err != nil {
				slog.Warn("safety-kernel: skipping input rule, invalid scope config", "rule", rule.ID, "err", err)
				continue
			}
		}

		scannerList := mergeScannerLists(rule.Match.Scanners, rule.Match.Detectors)
		warnUnknownScanners(strings.TrimSpace(rule.ID), scannerList, registry)
		ruleTier := rule.Tier
		if strings.TrimSpace(ruleTier) == "" {
			ruleTier = policy.Tier
		}
		tier := config.NormalizePolicyTier(ruleTier)
		selector := config.MergePolicySelector(policy.Selector, rule.Selector)
		if !config.IsValidPolicyTier(tier) {
			tier = config.PolicyTierGlobal
		}
		if tier == config.PolicyTierGlobal {
			selector = config.PolicySelector{}
		}
		out = append(out, compiledInputRule{
			id:           strings.TrimSpace(rule.ID),
			tier:         tier,
			selector:     selector,
			decision:     decision,
			reason:       strings.TrimSpace(rule.Reason),
			severity:     normalizeSeverity(rule.Severity),
			tenants:      normalizeList(rule.Match.Tenants),
			topics:       normalizeList(rule.Match.Topics),
			capabilities: normalizeList(rule.Match.Capabilities),
			riskTags:     normalizeList(rule.Match.RiskTags),
			contentTypes: normalizeList(rule.Match.ContentTypes),
			scanners:     scannerList,
			patterns:     patterns,
			keywords:     normalizeList(rule.Match.Keywords),
			maxBytes:     maxBytes,
			scope:        rule.Match.Scope,
		})
	}
	return out
}

func selectInputRulesForScope(
	rules []compiledInputRule,
	workflowID string,
	jobID string,
) []compiledInputRule {
	if len(rules) == 0 {
		return nil
	}
	workflowID = strings.TrimSpace(workflowID)
	jobID = strings.TrimSpace(jobID)
	jobRules := make([]compiledInputRule, 0, len(rules))
	workflowRules := make([]compiledInputRule, 0, len(rules))
	globalRules := make([]compiledInputRule, 0, len(rules))
	for _, rule := range rules {
		if !inputRuleAppliesToScope(rule, workflowID, jobID) {
			continue
		}
		switch config.NormalizePolicyTier(rule.tier) {
		case config.PolicyTierJob:
			jobRules = append(jobRules, rule)
		case config.PolicyTierWorkflow:
			workflowRules = append(workflowRules, rule)
		case config.PolicyTierGlobal:
			globalRules = append(globalRules, rule)
		}
	}
	out := make([]compiledInputRule, 0, len(jobRules)+len(workflowRules)+len(globalRules))
	out = append(out, jobRules...)
	out = append(out, workflowRules...)
	out = append(out, globalRules...)
	return out
}

func inputRuleAppliesToScope(rule compiledInputRule, workflowID, jobID string) bool {
	switch config.NormalizePolicyTier(rule.tier) {
	case config.PolicyTierWorkflow:
		key := config.PolicySelectorKey(config.PolicyTierWorkflow, rule.selector)
		return key != "" && key == workflowID
	case config.PolicyTierJob:
		key := config.PolicySelectorKey(config.PolicyTierJob, rule.selector)
		return key != "" && key == jobID
	case config.PolicyTierGlobal:
		return true
	default:
		return false
	}
}

// evaluateInputRule checks if a single input rule matches the request.
// Returns (matched, findings). Mirrors evaluateOutputRule logic.
func evaluateInputRule(rule compiledInputRule, req inputEvaluateRequest, scanners map[string]OutputScanner) (bool, []outputFinding) {
	if !inputMetadataMatches(rule, req) {
		return false, nil
	}
	if inputRuleContentSensitive(rule) && inputContentIncomplete(req) {
		return true, []outputFinding{inputTruncatedFailClosedFinding()}
	}
	if rule.maxBytes > 0 {
		return evaluateInputSizeRule(rule, req)
	}
	if len(req.content) == 0 {
		return evaluateMissingInputRule(rule, req)
	}
	return evaluateInputContentRule(rule, req.content, scanners)
}

func inputMetadataMatches(rule compiledInputRule, req inputEvaluateRequest) bool {
	if len(rule.tenants) > 0 && !containsAnyFold([]string{req.tenant}, rule.tenants) {
		return false
	}
	if len(rule.topics) > 0 && !matchAny(rule.topics, req.topic) {
		return false
	}
	if len(rule.capabilities) > 0 && !containsAnyFold(req.capabilities, rule.capabilities) {
		return false
	}
	if len(rule.riskTags) > 0 && !containsAnyFold(req.riskTags, rule.riskTags) {
		return false
	}
	return len(rule.contentTypes) == 0 || containsAnyFold([]string{req.contentType}, rule.contentTypes)
}

func inputRuleContentSensitive(rule compiledInputRule) bool {
	return len(rule.scanners) > 0 || len(rule.patterns) > 0 ||
		len(rule.keywords) > 0 || rule.scope != nil
}

func inputRuleUsesContentMatchers(rule compiledInputRule) bool {
	return len(rule.scanners) > 0 || len(rule.patterns) > 0 || len(rule.keywords) > 0
}

func inputContentIncomplete(req inputEvaluateRequest) bool {
	return req.inputSize > int64(len(req.content))
}

func inputTruncatedFailClosedFinding() outputFinding {
	return outputFinding{
		Type:     "content_truncated",
		Severity: "high",
		Detail:   "declared input size exceeds received content; unscanned tail may violate policy - failing closed",
		Scanner:  "size_check",
	}
}

func evaluateInputSizeRule(rule compiledInputRule, req inputEvaluateRequest) (bool, []outputFinding) {
	size := req.inputSize
	if size <= 0 {
		size = int64(len(req.content))
	}
	if size <= rule.maxBytes {
		return false, nil
	}
	return true, []outputFinding{{
		Type:     "input_size_exceeded",
		Severity: rule.severity,
		Detail:   "input size exceeds policy limit",
		Scanner:  "size_check",
	}}
}

func evaluateMissingInputRule(rule compiledInputRule, req inputEvaluateRequest) (bool, []outputFinding) {
	if rule.scope != nil && rule.scope.OnMissingInput != "allow" {
		slog.Warn("scope rule matched by metadata but content unavailable - denying",
			"component", "safety", "rule", rule.id, "topic", req.topic)
		return true, []outputFinding{{
			Type:     "content_required_but_missing",
			Severity: rule.severity,
			Detail:   "scope rule requires structured input content but none was provided",
			Scanner:  "scope_evaluator",
		}}
	}
	if inputRuleContentSensitive(rule) {
		return false, nil
	}
	return true, nil
}

func evaluateInputContentRule(
	rule compiledInputRule,
	content []byte,
	scanners map[string]OutputScanner,
) (bool, []outputFinding) {
	findings, ok := scanInputScope(rule, content)
	if !ok {
		return false, nil
	}
	if findings, ok = scanInputPatterns(rule, content, findings); !ok {
		return false, nil
	}
	if findings, ok = scanInputKeywords(rule, content, findings); !ok {
		return false, nil
	}
	if findings, ok = scanInputNamedScanners(rule, content, scanners, findings); !ok {
		return false, nil
	}
	if inputRuleUsesContentMatchers(rule) && len(findings) == 0 {
		return false, nil
	}
	return true, findings
}

func scanInputScope(rule compiledInputRule, content []byte) ([]outputFinding, bool) {
	if rule.scope == nil {
		return nil, true
	}
	violated, scopeFindings := evaluateScope(rule.scope, content)
	findings := make([]outputFinding, 0, len(scopeFindings))
	for _, finding := range scopeFindings {
		findings = append(findings, outputFinding{
			Type:     finding.Type,
			Severity: rule.severity,
			Detail:   finding.Detail,
			Scanner:  "scope_evaluator",
		})
	}
	if !violated && len(findings) == 0 {
		return nil, false
	}
	return findings, true
}

func scanInputPatterns(
	rule compiledInputRule,
	content []byte,
	findings []outputFinding,
) ([]outputFinding, bool) {
	if len(rule.patterns) == 0 {
		return findings, true
	}
	for _, pattern := range rule.patterns {
		if pattern.re.Match(content) {
			findings = append(findings, outputFinding{
				Type: "content_pattern_match", Severity: rule.severity,
				Detail: "input content matched pattern", Scanner: "regex",
				MatchedPattern: pattern.raw,
			})
		}
	}
	return findings, len(findings) > 0
}

func scanInputKeywords(
	rule compiledInputRule,
	content []byte,
	findings []outputFinding,
) ([]outputFinding, bool) {
	if len(rule.keywords) == 0 {
		return findings, true
	}
	matches := newKeywordScanner(rule.keywords).Scan(content)
	if len(matches) == 0 && len(findings) == 0 {
		return nil, false
	}
	return append(findings, matches...), true
}

func scanInputNamedScanners(
	rule compiledInputRule,
	content []byte,
	scanners map[string]OutputScanner,
	findings []outputFinding,
) ([]outputFinding, bool) {
	if len(rule.scanners) == 0 {
		return findings, true
	}
	matches := scanWithScanners(content, rule.scanners, scanners)
	if len(matches) == 0 && len(findings) == 0 {
		return nil, false
	}
	return append(findings, matches...), true
}

// severityRank maps the severity string vocabulary to an ordinal so
// "high" / "critical" thresholds can compare against finding severities.
// Unknown values rank as 0 (below low) — fail-closed when a threshold
// references an unrecognized severity floor.
func severityRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	}
	return 0
}

// shouldDowngradeDenyToRequireHuman implements the REQUIRE_HUMAN threshold
// per architect amendment comment-79a9e609 on task-96f931fe.
//
// Returns true (= downgrade to REQUIRE_HUMAN) when the matched input rule's
// "deny" verdict is "truly ambiguous" per DoD #4. Three independent
// conditions trigger downgrade (logical OR):
//
//  1. Prompt-only: request carries no ActionDescriptor (act == nil) AND the
//     operator opted in via threshold.DowngradeWhenPromptOnly.
//  2. Below severity floor: at least one matched finding has severity
//     strictly lower than threshold.MinSeverityForDeny.
//  3. Below confidence floor: at least one matched finding has confidence
//     strictly lower than threshold.MinConfidenceForDeny.
//
// Zero values disable the corresponding floor — an empty MinSeverityForDeny
// or zero MinConfidenceForDeny means "no floor", preserving legacy DENY-on-
// match behavior unless the operator has opted in by setting the field.
//
// The "at least one below" semantics (not "all below") matches the architect's
// intent: a single ambiguous finding in a multi-pattern rule downgrades the
// whole rule, because the operator authored the rule expecting a clean
// high-severity high-confidence match. Mixed bags route to human review.
func shouldDowngradeDenyToRequireHuman(
	rule compiledInputRule,
	findings []outputFinding,
	action *config.ActionDescriptor,
	threshold config.RequireHumanThreshold,
) bool {
	if threshold.DowngradeWhenPromptOnly && action == nil {
		return true
	}
	if minRank := severityRank(threshold.MinSeverityForDeny); minRank > 0 {
		for _, f := range findings {
			if severityRank(f.Severity) < minRank {
				return true
			}
		}
		// If the rule itself authored a severity below the floor, downgrade
		// even when individual findings carry the higher synthesized severity
		// (e.g. a "low"-tier rule emits "low"-severity findings; this floor
		// captures operator intent that low-severity DENYs should be
		// human-reviewable).
		if severityRank(rule.severity) > 0 && severityRank(rule.severity) < minRank {
			return true
		}
	}
	if threshold.MinConfidenceForDeny > 0 {
		for _, f := range findings {
			if f.Confidence > 0 && f.Confidence < threshold.MinConfidenceForDeny {
				return true
			}
		}
	}
	return false
}

// inputRuleReason builds a human-readable reason string from rule + findings.
func inputRuleReason(rule compiledInputRule, findings []outputFinding) string {
	if rule.reason != "" && len(findings) == 0 {
		return rule.reason
	}
	if len(findings) > 0 {
		parts := make([]string, 0, len(findings))
		for _, f := range findings {
			parts = append(parts, f.Detail)
		}
		if rule.reason != "" {
			return rule.reason + ": " + strings.Join(parts, ", ")
		}
		return "input content flagged: " + strings.Join(parts, ", ")
	}
	return rule.reason
}
