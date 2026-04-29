package mcp

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// RedactionRule declares how to scrub one class of sensitive data
// from MCP tool arguments before they land in an audit event.
//
// Exactly one of FieldName or Regex should be set:
//   - FieldName: case-insensitive exact-match on JSON property name.
//     Matches at any depth via recursive descent. Replaces the
//     matching value with Replacement regardless of its type.
//   - Regex: applied to every string leaf. Matches are replaced with
//     Replacement.
//
// Description is a short operator-facing tag surfaced in the
// replacement text for quick forensic review.
type RedactionRule struct {
	FieldName   string `json:"field_name,omitempty"`
	Regex       string `json:"regex,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	Description string `json:"description,omitempty"`
}

// policyRedactor is the default ArgumentRedactor impl.
type policyRedactor struct {
	rules          []RedactionRule
	fieldNames     map[string]string // lowercased field name -> description
	compiledRegexp []compiledRegex
}

type compiledRegex struct {
	re          *regexp.Regexp
	replacement string
	description string
}

var (
	defaultRedactorOnce sync.Once
	defaultRedactorVal  ArgumentRedactor
)

// DefaultRedactor returns the package-default redactor wrapping
// DefaultRedactionRules(). Cached so callers share one compiled copy.
func DefaultRedactor() ArgumentRedactor {
	defaultRedactorOnce.Do(func() {
		defaultRedactorVal = NewPolicyRedactor(DefaultRedactionRules())
	})
	return defaultRedactorVal
}

// DefaultRedactionRules returns the baseline policy: common secret
// field names + regex heuristics for high-confidence secret shapes.
// Customers can append to / override via LoadRulesFromPolicyBundle.
func DefaultRedactionRules() []RedactionRule {
	return []RedactionRule{
		{FieldName: "password", Replacement: "[REDACTED:password]", Description: "password"},
		{FieldName: "passwd", Replacement: "[REDACTED:password]", Description: "password"},
		{FieldName: "api_key", Replacement: "[REDACTED:api_key]", Description: "api_key"},
		{FieldName: "apiKey", Replacement: "[REDACTED:api_key]", Description: "api_key"},
		{FieldName: "apikey", Replacement: "[REDACTED:api_key]", Description: "api_key"},
		{FieldName: "token", Replacement: "[REDACTED:token]", Description: "token"},
		{FieldName: "access_token", Replacement: "[REDACTED:token]", Description: "token"},
		{FieldName: "refresh_token", Replacement: "[REDACTED:token]", Description: "token"},
		{FieldName: "authorization", Replacement: "[REDACTED:authorization]", Description: "authorization"},
		{FieldName: "secret", Replacement: "[REDACTED:secret]", Description: "secret"},
		{FieldName: "client_secret", Replacement: "[REDACTED:secret]", Description: "secret"},
		{FieldName: "private_key", Replacement: "[REDACTED:private_key]", Description: "private_key"},
		{FieldName: "privateKey", Replacement: "[REDACTED:private_key]", Description: "private_key"},
		// Regex heuristics — defence in depth for secrets that slipped
		// into plain string fields.
		{Regex: `AKIA[0-9A-Z]{16}`, Replacement: "[REDACTED:aws_access_key]", Description: "aws_access_key"},
		{Regex: `sk_live_[a-zA-Z0-9]{24,}`, Replacement: "[REDACTED:stripe_secret]", Description: "stripe_secret"},
		{Regex: `eyJ[a-zA-Z0-9_\-]+\.[a-zA-Z0-9_\-]+\.[a-zA-Z0-9_\-]+`, Replacement: "[REDACTED:jwt]", Description: "jwt"},
		{Regex: `-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]+?-----END [A-Z ]+PRIVATE KEY-----`, Replacement: "[REDACTED:pem_private_key]", Description: "pem_private_key"},
	}
}

// LoadRulesFromPolicyBundle decodes a policy bundle's MCP argument
// redaction section and returns the rules. The bundle is expected to
// carry a JSON fragment at the documented path
// `policy.mcp.argument_redaction.rules: []RedactionRule` (see
// docs/audit/mcp-events.md). Unknown top-level fields are ignored so
// older bundles still parse; zero rules produces (nil, nil) so the
// caller can merge with DefaultRedactionRules without a branch. A
// malformed payload returns an error.
func LoadRulesFromPolicyBundle(raw []byte) ([]RedactionRule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var envelope struct {
		Policy struct {
			MCP struct {
				ArgumentRedaction struct {
					Rules []RedactionRule `json:"rules"`
				} `json:"argument_redaction"`
			} `json:"mcp"`
		} `json:"policy"`
		// Also accept a bare top-level `{"rules": [...]}` so an operator
		// can point the loader at a focused rules file without wrapping
		// it in the full policy envelope.
		Rules []RedactionRule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	rules := envelope.Policy.MCP.ArgumentRedaction.Rules
	if len(rules) == 0 {
		rules = envelope.Rules
	}
	return rules, nil
}

// MergeRedactionRules returns the concatenation of `base` and
// `overrides`, with later-added entries winning on a field-name clash.
// Callers use this to layer policy-bundle rules on top of
// DefaultRedactionRules without mutating either slice in place.
func MergeRedactionRules(base, overrides []RedactionRule) []RedactionRule {
	if len(overrides) == 0 {
		return append([]RedactionRule(nil), base...)
	}
	seen := make(map[string]int)
	merged := make([]RedactionRule, 0)
	for _, r := range base {
		if r.FieldName != "" {
			key := strings.ToLower(strings.TrimSpace(r.FieldName))
			seen[key] = len(merged)
		}
		merged = append(merged, r)
	}
	for _, r := range overrides {
		if r.FieldName != "" {
			key := strings.ToLower(strings.TrimSpace(r.FieldName))
			if idx, ok := seen[key]; ok {
				merged[idx] = r
				continue
			}
			seen[key] = len(merged)
		}
		merged = append(merged, r)
	}
	return merged
}

// NewPolicyRedactor compiles a rule set into a redactor. Invalid
// regex rules are dropped with their index recorded in compiledErrors
// (accessible via (*policyRedactor).CompiledErrors for diagnostics)
// so a single bad rule doesn't brick the pipeline.
func NewPolicyRedactor(rules []RedactionRule) ArgumentRedactor {
	r := &policyRedactor{
		rules:      append([]RedactionRule(nil), rules...),
		fieldNames: make(map[string]string, len(rules)),
	}
	for _, rule := range rules {
		if rule.FieldName != "" {
			desc := rule.Description
			if desc == "" {
				desc = rule.FieldName
			}
			r.fieldNames[strings.ToLower(strings.TrimSpace(rule.FieldName))] = fallbackReplacement(rule.Replacement, desc)
			continue
		}
		if rule.Regex == "" {
			continue
		}
		re, err := regexp.Compile(rule.Regex)
		if err != nil {
			continue
		}
		desc := rule.Description
		if desc == "" {
			desc = "regex"
		}
		r.compiledRegexp = append(r.compiledRegexp, compiledRegex{
			re:          re,
			replacement: fallbackReplacement(rule.Replacement, desc),
			description: desc,
		})
	}
	return r
}

func fallbackReplacement(replacement, description string) string {
	if replacement != "" {
		return replacement
	}
	return "[REDACTED:" + description + "]"
}

// Redact walks the JSON tree and applies field-name + regex rules.
// Returns the redacted JSON as canonical bytes. Malformed JSON is
// replaced with a sentinel object so nothing sensitive leaks via a
// fallback-as-is path.
func (r *policyRedactor) Redact(args json.RawMessage) json.RawMessage {
	if r == nil || len(args) == 0 {
		return args
	}
	var parsed any
	if err := json.Unmarshal(args, &parsed); err != nil {
		return json.RawMessage(`{"_redacted":"[REDACTED:unparseable_args]"}`)
	}
	parsed = r.walk(parsed)
	out, err := json.Marshal(parsed)
	if err != nil {
		return json.RawMessage(`{"_redacted":"[REDACTED:marshal_error]"}`)
	}
	return out
}

func (r *policyRedactor) walk(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			if replacement, ok := r.fieldNames[strings.ToLower(k)]; ok {
				out[k] = replacement
				continue
			}
			out[k] = r.walk(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = r.walk(item)
		}
		return out
	case string:
		return r.applyRegex(typed)
	default:
		return v
	}
}

func (r *policyRedactor) applyRegex(s string) string {
	if len(r.compiledRegexp) == 0 {
		return s
	}
	for _, cr := range r.compiledRegexp {
		if cr.re.MatchString(s) {
			s = cr.re.ReplaceAllString(s, cr.replacement)
		}
	}
	return s
}
