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
func compileInputRules(policy *config.SafetyPolicy) []compiledInputRule {
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
	// Metadata matching.
	if len(rule.tenants) > 0 && !containsAnyFold([]string{req.tenant}, rule.tenants) {
		return false, nil
	}
	if len(rule.topics) > 0 && !matchAny(rule.topics, req.topic) {
		return false, nil
	}
	if len(rule.capabilities) > 0 && !containsAnyFold(req.capabilities, rule.capabilities) {
		return false, nil
	}
	if len(rule.riskTags) > 0 && !containsAnyFold(req.riskTags, rule.riskTags) {
		return false, nil
	}
	if len(rule.contentTypes) > 0 && !containsAnyFold([]string{req.contentType}, rule.contentTypes) {
		return false, nil
	}

	// Size check.
	if rule.maxBytes > 0 {
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

	// Content scanning — only if content is available.
	hasContentCriteria := len(rule.scanners) > 0 || len(rule.patterns) > 0 || len(rule.keywords) > 0 || rule.scope != nil
	if len(req.content) == 0 {
		// Scope rules that require content should not be silently bypassed.
		// When content is unavailable and the scope's on_missing_input is "deny"
		// (the default), the rule matches with a content_required finding.
		if rule.scope != nil && rule.scope.OnMissingInput != "allow" {
			slog.Warn("scope rule matched by metadata but content unavailable — denying",
				"component", "safety", "rule", rule.id, "topic", req.topic)
			return true, []outputFinding{{
				Type:     "content_required_but_missing",
				Severity: rule.severity,
				Detail:   "scope rule requires structured input content but none was provided",
				Scanner:  "scope_evaluator",
			}}
		}
		if hasContentCriteria {
			return false, nil
		}
		// Pure metadata rule matched.
		return true, nil
	}

	findings := make([]outputFinding, 0, 8)

	// Structured scope evaluation (instruction-vs-cart comparison).
	if rule.scope != nil {
		violated, scopeFindings := evaluateScope(rule.scope, req.content)
		for _, sf := range scopeFindings {
			findings = append(findings, outputFinding{
				Type:     sf.Type,
				Severity: rule.severity,
				Detail:   sf.Detail,
				Scanner:  "scope_evaluator",
			})
		}
		if !violated && len(findings) == 0 {
			return false, nil
		}
	}

	// Run regex patterns (reuses output_policy scanWithContentPatterns infrastructure).
	if len(rule.patterns) > 0 {
		for _, pat := range rule.patterns {
			if pat.re.Match(req.content) {
				findings = append(findings, outputFinding{
					Type:           "content_pattern_match",
					Severity:       rule.severity,
					Detail:         "input content matched pattern",
					Scanner:        "regex",
					MatchedPattern: pat.raw,
				})
			}
		}
		if len(findings) == 0 {
			return false, nil
		}
	}

	// Run keyword matching.
	if len(rule.keywords) > 0 {
		kwScanner := newKeywordScanner(rule.keywords)
		kwFindings := kwScanner.Scan(req.content)
		if len(kwFindings) == 0 && len(findings) == 0 {
			return false, nil
		}
		findings = append(findings, kwFindings...)
	}

	// Run named scanners (PII, secrets, injection — same instances as output policy).
	if len(rule.scanners) > 0 {
		scannerFindings := scanWithScanners(req.content, rule.scanners, scanners)
		if len(scannerFindings) == 0 && len(findings) == 0 {
			return false, nil
		}
		findings = append(findings, scannerFindings...)
	}

	// If rule specifies content criteria and no findings, no match.
	if (len(rule.patterns) > 0 || len(rule.keywords) > 0 || len(rule.scanners) > 0) && len(findings) == 0 {
		return false, nil
	}

	return true, findings
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
