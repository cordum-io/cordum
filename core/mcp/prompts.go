package mcp

// MCP prompts — Cordum's server-side prompt registry.
//
// Prompts are templated inputs an LLM client requests by name. The
// server fills in Cordum-specific context (policy grammar, audit
// fetches, version diffs) and returns a chat-shaped message chain the
// client feeds to its model.
//
// The registry mirrors ToolRegistry: Register + List + Render. Four
// first-party prompts ship via RegisterAllPrompts:
//   - draft_safety_rule      — scaffold a new safety policy rule
//   - explain_denial         — plain-English explanation of a deny event
//   - summarize_approvals    — natural-language approvals digest
//   - policy_migration_helper — convert a policy bundle across versions
//
// Each prompt has a Render function: (ctx, args) → []PromptMessage.
// Render is the ONLY place server-side data fetches happen (for
// explain_denial + summarize_approvals). The fetches are optional —
// when no data source is wired the prompt falls back to a template
// that asks the user for the missing context rather than hallucinating.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors returned by the registry.
var (
	// ErrPromptNotFound is returned by Render when the named prompt
	// isn't registered. handlePromptsGet maps it to JSON-RPC -32601.
	ErrPromptNotFound = errors.New("mcp: prompt not found")
	// ErrPromptInvalidArgs is returned by Render when required
	// arguments are missing or malformed. handlePromptsGet maps it to
	// JSON-RPC -32602.
	ErrPromptInvalidArgs = errors.New("mcp: invalid prompt arguments")
)

// PromptService is the narrow interface the MCP server consumes. Any
// implementation that satisfies List + Render can back prompts/list +
// prompts/get.
type PromptService interface {
	List(ctx context.Context) []Prompt
	Render(ctx context.Context, name string, args map[string]string) (*PromptGetResult, error)
}

// PromptRenderer is the server-side template function. Receives the
// request args and returns the rendered messages + an optional
// description override for the PromptGetResult.
type PromptRenderer func(ctx context.Context, args map[string]string) ([]PromptMessage, string, error)

// PromptEntry pairs a Prompt descriptor with its render function.
type PromptEntry struct {
	Descriptor Prompt
	Render     PromptRenderer
}

// PromptRegistry is the default PromptService implementation.
type PromptRegistry struct {
	mu      sync.RWMutex
	entries map[string]PromptEntry
}

// NewPromptRegistry returns an empty registry.
func NewPromptRegistry() *PromptRegistry {
	return &PromptRegistry{entries: map[string]PromptEntry{}}
}

// Register adds or replaces a prompt. Name must be non-empty; duplicate
// registrations replace the previous entry so test harnesses can
// override a first-party prompt.
func (r *PromptRegistry) Register(entry PromptEntry) error {
	if r == nil {
		return errors.New("mcp: nil prompt registry")
	}
	name := strings.TrimSpace(entry.Descriptor.Name)
	if name == "" {
		return errors.New("mcp: prompt name is required")
	}
	if entry.Render == nil {
		return errors.New("mcp: prompt renderer is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry.Descriptor.Name = name
	r.entries[name] = entry
	return nil
}

// List returns the sorted list of registered prompts. Sorted for
// deterministic catalogue output.
func (r *PromptRegistry) List(ctx context.Context) []Prompt {
	_ = ctx
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Prompt, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Descriptor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Render invokes the named prompt's renderer. Returns
// ErrPromptNotFound when no such prompt exists and ErrPromptInvalidArgs
// when the renderer surfaces an argument-level failure.
func (r *PromptRegistry) Render(ctx context.Context, name string, args map[string]string) (*PromptGetResult, error) {
	if r == nil {
		return nil, ErrPromptNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: prompt name is required", ErrPromptInvalidArgs)
	}
	r.mu.RLock()
	entry, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrPromptNotFound
	}
	if err := validatePromptArgs(entry.Descriptor, args); err != nil {
		return nil, err
	}
	messages, description, err := entry.Render(ctx, args)
	if err != nil {
		return nil, err
	}
	if description == "" {
		description = entry.Descriptor.Description
	}
	return &PromptGetResult{Description: description, Messages: messages}, nil
}

// validatePromptArgs confirms every Required argument is present and
// non-empty. The server enforces this BEFORE invoking the renderer so
// the templates can assume their required inputs are non-zero.
func validatePromptArgs(descriptor Prompt, args map[string]string) error {
	var missing []string
	for _, arg := range descriptor.Arguments {
		if !arg.Required {
			continue
		}
		v := strings.TrimSpace(args[arg.Name])
		if v == "" {
			missing = append(missing, arg.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing required %s", ErrPromptInvalidArgs, strings.Join(missing, ", "))
	}
	return nil
}

// RegisterAllPrompts registers Cordum's four first-party prompts. Call
// this once at server boot to populate the registry with the shipped
// prompts. Callers that want a subset register them individually.
func RegisterAllPrompts(r *PromptRegistry) error {
	if r == nil {
		return errors.New("mcp: nil prompt registry")
	}
	prompts := []PromptEntry{
		draftSafetyRulePrompt(),
		explainDenialPrompt(),
		summarizeApprovalsPrompt(),
		policyMigrationHelperPrompt(),
	}
	for _, p := range prompts {
		if err := r.Register(p); err != nil {
			return fmt.Errorf("register %q: %w", p.Descriptor.Name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// draft_safety_rule
// ---------------------------------------------------------------------------

const draftSafetyRulePromptName = "draft_safety_rule"

// draftSafetyRuleDisclaimer is the load-bearing phrase every rendered
// output MUST contain. Tests pin this exact string so a future
// template edit that loses the disclaimer fails fast.
const draftSafetyRuleDisclaimer = "Always run the scaffolded rule through /api/v1/policy/simulate on a staging tenant before promoting it to production. Policy changes are signed; a mistake at this layer can block real jobs."

func draftSafetyRulePrompt() PromptEntry {
	return PromptEntry{
		Descriptor: Prompt{
			Name:        draftSafetyRulePromptName,
			Description: "Scaffold a new Cordum safety rule for a described scenario. Output includes a policy-YAML block + a required simulate-before-apply disclaimer.",
			Arguments: []PromptArgument{
				{Name: "scenario", Description: "Plain-language description of the behaviour the rule should govern.", Required: true},
				{Name: "topic", Description: "Target job topic glob (e.g. job.payments.*).", Required: false},
				{Name: "risk_level", Description: "Risk level: low | medium | high.", Required: false},
			},
		},
		Render: func(_ context.Context, args map[string]string) ([]PromptMessage, string, error) {
			scenario := strings.TrimSpace(args["scenario"])
			topic := strings.TrimSpace(args["topic"])
			if topic == "" {
				topic = "job.*"
			}
			risk := strings.ToLower(strings.TrimSpace(args["risk_level"]))
			switch risk {
			case "low", "medium", "high":
			case "":
				risk = "medium"
			default:
				return nil, "", fmt.Errorf("%w: risk_level must be low|medium|high (got %q)", ErrPromptInvalidArgs, args["risk_level"])
			}
			systemText := strings.Join([]string{
				"You are a Cordum safety-policy author. You write rules in Cordum's policy-bundle YAML grammar.",
				"",
				"Grammar summary (one rule looks like):",
				"  - id: <kebab>",
				"    match:",
				"      tenants: [...]   # optional",
				"      topics: [...]    # supports path.Match globs",
				"      capabilities: [...]",
				"      risk_tags: [...]",
				"      requires: [...]",
				"      labels: {key: value}",
				"      mcp:",
				"        allow_tools: [...]",
				"        deny_tools: [...]",
				"    decision: allow | deny | require_approval | throttle | allow_with_constraints",
				"    reason: <one line>",
				"    remediations: [...]    # optional",
				"",
				"Decision sentinels are exact strings — do not rename them.",
				"",
				"SAFETY REQUIREMENT: every draft rule MUST be followed by this verbatim disclaimer so the operator runs /api/v1/policy/simulate before applying the change:",
				"",
				"    " + draftSafetyRuleDisclaimer,
			}, "\n")
			userText := strings.Join([]string{
				"Draft one safety rule for this scenario:",
				"",
				"  scenario: " + scenario,
				"  topic scope: " + topic,
				"  risk level: " + risk,
				"",
				"Respond with:",
				"1. One `rules:`-compatible YAML block wrapped in ```yaml fences.",
				"2. The verbatim simulate-before-apply disclaimer on its own line.",
				"3. A two-sentence rationale for the chosen decision.",
			}, "\n")
			return []PromptMessage{
				{Role: "system", Content: PromptBlock{Type: "text", Text: systemText}},
				{Role: "user", Content: PromptBlock{Type: "text", Text: userText}},
			}, "", nil
		},
	}
}

// ---------------------------------------------------------------------------
// explain_denial
// ---------------------------------------------------------------------------

const explainDenialPromptName = "explain_denial"

// DenialContextFetcher lets the caller supply a real audit fetch for
// explain_denial. A typical implementation reads the deny event from
// the tenant audit chain; the fallback renderer assumes no fetcher is
// wired and asks the user to paste the context.
type DenialContextFetcher func(ctx context.Context, jobID string) (DenialContext, error)

// DenialContext is the decision context explain_denial consumes. All
// fields are optional — the prompt adapts to whatever the fetcher
// manages to retrieve.
type DenialContext struct {
	JobID      string
	Decision   string
	RuleID     string
	Reason     string
	Tenant     string
	Topic      string
	AgentID    string
	RiskTags   []string
	OccurredAt string
}

// explainDenialFetcherKey is the context key the gateway's handler
// sets when it wires a real DenialContextFetcher onto the request
// context. Tests can set it too via context.WithValue.
type explainDenialFetcherKey struct{}

// WithDenialContextFetcher returns a derived context that carries the
// given fetcher. The explain_denial renderer reads it from the
// context so the prompt can embed a real decision record without the
// registry depending on the audit package.
func WithDenialContextFetcher(ctx context.Context, fetcher DenialContextFetcher) context.Context {
	if fetcher == nil {
		return ctx
	}
	return context.WithValue(ctx, explainDenialFetcherKey{}, fetcher)
}

func explainDenialPrompt() PromptEntry {
	return PromptEntry{
		Descriptor: Prompt{
			Name:        explainDenialPromptName,
			Description: "Explain why a Cordum job was denied and suggest remediation. Fetches the real deny event when a DenialContextFetcher is wired into the request context.",
			Arguments: []PromptArgument{
				{Name: "job_id", Description: "The denied job's identifier.", Required: true},
			},
		},
		Render: func(ctx context.Context, args map[string]string) ([]PromptMessage, string, error) {
			jobID := strings.TrimSpace(args["job_id"])
			var dc DenialContext
			var fetchErr error
			if fetcher, ok := ctx.Value(explainDenialFetcherKey{}).(DenialContextFetcher); ok && fetcher != nil {
				dc, fetchErr = fetcher(ctx, jobID)
			}
			systemText := strings.Join([]string{
				"You are a Cordum policy explainer. You translate a deny decision into plain English for an operator.",
				"",
				"Your response must:",
				"1. Name the rule that fired (quote its id + reason).",
				"2. Restate the user-facing impact in one sentence.",
				"3. Suggest ONE of the following remediations: (a) request approval via the approvals API, (b) change the tool/topic/capability to match an allowed rule, or (c) file a policy-exception request via the runbook.",
				"4. Never invent fields that weren't in the provided context; say 'not recorded' when the field is missing.",
			}, "\n")
			userText := denialContextText(jobID, dc, fetchErr)
			return []PromptMessage{
				{Role: "system", Content: PromptBlock{Type: "text", Text: systemText}},
				{Role: "user", Content: PromptBlock{Type: "text", Text: userText}},
			}, "", nil
		},
	}
}

func denialContextText(jobID string, dc DenialContext, fetchErr error) string {
	var sb strings.Builder
	sb.WriteString("A Cordum job was denied. Explain why and suggest next steps.\n\n")
	fmt.Fprintf(&sb, "  job_id: %s\n", jobID)
	if fetchErr != nil {
		fmt.Fprintf(&sb, "  decision_lookup: failed (%s)\n", fetchErr)
		sb.WriteString("\nThe audit fetch failed — ask the operator to paste the deny event into the conversation before answering.\n")
		return sb.String()
	}
	if dc.JobID == "" {
		sb.WriteString("  decision_lookup: no fetcher wired\n\n")
		sb.WriteString("No audit fetcher was wired into this request — ask the operator to paste the deny event's rule_id, reason, and job context before answering.\n")
		return sb.String()
	}
	if dc.Decision != "" {
		fmt.Fprintf(&sb, "  decision: %s\n", dc.Decision)
	}
	if dc.RuleID != "" {
		fmt.Fprintf(&sb, "  rule_id: %s\n", dc.RuleID)
	}
	if dc.Reason != "" {
		fmt.Fprintf(&sb, "  reason: %s\n", dc.Reason)
	}
	if dc.Tenant != "" {
		fmt.Fprintf(&sb, "  tenant: %s\n", dc.Tenant)
	}
	if dc.Topic != "" {
		fmt.Fprintf(&sb, "  topic: %s\n", dc.Topic)
	}
	if dc.AgentID != "" {
		fmt.Fprintf(&sb, "  agent_id: %s\n", dc.AgentID)
	}
	if len(dc.RiskTags) > 0 {
		fmt.Fprintf(&sb, "  risk_tags: %s\n", strings.Join(dc.RiskTags, ", "))
	}
	if dc.OccurredAt != "" {
		fmt.Fprintf(&sb, "  occurred_at: %s\n", dc.OccurredAt)
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// summarize_approvals
// ---------------------------------------------------------------------------

const summarizeApprovalsPromptName = "summarize_approvals"

// ApprovalsSummarySource lets the caller inject a real digest fetch.
// Empty counts means "no source wired" — the renderer then asks the
// user to paste the digest.
type ApprovalsSummarySource func(ctx context.Context, window, tenant string) (ApprovalsSummary, error)

// ApprovalsSummary is the compact digest the renderer embeds into the
// prompt. Intentionally small — the LLM writes prose, not SQL.
type ApprovalsSummary struct {
	Window    string
	Tenant    string
	Pending   int
	Approved  int
	Rejected  int
	Expired   int
	ByRule    map[string]int
	Approvers map[string]int
}

type approvalsSourceKey struct{}

// WithApprovalsSummarySource wires a real digest fetcher into the
// request context. Tests can inject fakes here.
func WithApprovalsSummarySource(ctx context.Context, src ApprovalsSummarySource) context.Context {
	if src == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalsSourceKey{}, src)
}

func summarizeApprovalsPrompt() PromptEntry {
	return PromptEntry{
		Descriptor: Prompt{
			Name:        summarizeApprovalsPromptName,
			Description: "Produce a human-readable digest of MCP + job approvals in a time window. Pulls real counts when an ApprovalsSummarySource is wired; otherwise asks the caller to paste the digest.",
			Arguments: []PromptArgument{
				{Name: "window", Description: "Lookback window (e.g. 24h, 7d). Default 24h, max 30d.", Required: false},
				{Name: "tenant", Description: "Tenant slug to scope the summary. Default: session tenant.", Required: false},
			},
		},
		Render: func(ctx context.Context, args map[string]string) ([]PromptMessage, string, error) {
			window := strings.TrimSpace(args["window"])
			if window == "" {
				window = "24h"
			}
			if !isValidApprovalsWindow(window) {
				return nil, "", fmt.Errorf("%w: window must be a duration like 24h or 7d (got %q)", ErrPromptInvalidArgs, args["window"])
			}
			tenant := strings.TrimSpace(args["tenant"])
			var summary ApprovalsSummary
			var fetchErr error
			if src, ok := ctx.Value(approvalsSourceKey{}).(ApprovalsSummarySource); ok && src != nil {
				summary, fetchErr = src(ctx, window, tenant)
			}
			systemText := strings.Join([]string{
				"You are a Cordum governance analyst. You summarise approval activity for an operator weekly / daily stand-up.",
				"",
				"Structure:",
				"1. One-sentence headline: total approvals, balance pending vs resolved, any notable shift vs baseline.",
				"2. Bullet: top 3 rules firing approvals with their counts.",
				"3. Bullet: top approvers (by count).",
				"4. Bullet: anomalies — spike in denies, one agent dominating pending, approvals expiring without resolution.",
				"5. If data is missing, say 'data not available for this window' rather than guessing.",
			}, "\n")
			userText := approvalsDigestText(window, tenant, summary, fetchErr)
			return []PromptMessage{
				{Role: "system", Content: PromptBlock{Type: "text", Text: systemText}},
				{Role: "user", Content: PromptBlock{Type: "text", Text: userText}},
			}, "", nil
		},
	}
}

func approvalsDigestText(window, tenant string, s ApprovalsSummary, fetchErr error) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Summarise approvals for window=%s tenant=%s.\n\n", window, orDash(tenant))
	if fetchErr != nil {
		fmt.Fprintf(&sb, "digest_lookup: failed (%s)\n\nPaste the approvals digest into the conversation and I'll summarise from that.\n", fetchErr)
		return sb.String()
	}
	if s.Pending+s.Approved+s.Rejected+s.Expired == 0 && len(s.ByRule) == 0 {
		sb.WriteString("digest_lookup: no source wired\n\nPaste the approvals digest (counts by status + by rule) into the conversation and I'll summarise from that.\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "counts: pending=%d approved=%d rejected=%d expired=%d\n", s.Pending, s.Approved, s.Rejected, s.Expired)
	if len(s.ByRule) > 0 {
		sb.WriteString("by_rule:\n")
		for _, line := range topCountLines(s.ByRule, 5) {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	if len(s.Approvers) > 0 {
		sb.WriteString("top_approvers:\n")
		for _, line := range topCountLines(s.Approvers, 5) {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// isValidApprovalsWindow accepts simple duration shorthand: Nh, Nd up
// to 30d. Anything else rejects so the renderer can surface
// ErrPromptInvalidArgs to the client.
func isValidApprovalsWindow(w string) bool {
	if w == "" {
		return false
	}
	if !strings.HasSuffix(w, "h") && !strings.HasSuffix(w, "d") {
		return false
	}
	num := w[:len(w)-1]
	if num == "" {
		return false
	}
	n := 0
	for _, c := range num {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
		if n > 99999 {
			return false
		}
	}
	if n <= 0 {
		return false
	}
	if strings.HasSuffix(w, "d") && n > 30 {
		return false
	}
	if strings.HasSuffix(w, "h") && n > 30*24 {
		return false
	}
	return true
}

// topCountLines renders a stable "name: N" top-K list for embedding
// inside prompt text. Deterministic order so tests pin the output.
func topCountLines(m map[string]int, k int) []string {
	type kv struct {
		name  string
		count int
	}
	items := make([]kv, 0, len(m))
	for name, count := range m {
		items = append(items, kv{name: name, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].name < items[j].name
	})
	if k > len(items) {
		k = len(items)
	}
	out := make([]string, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, fmt.Sprintf("%s: %d", items[i].name, items[i].count))
	}
	return out
}

// ---------------------------------------------------------------------------
// policy_migration_helper
// ---------------------------------------------------------------------------

const policyMigrationHelperPromptName = "policy_migration_helper"

// PolicyGrammarDiffSource returns a human-readable grammar diff
// between two Cordum versions. Tests inject a fake; production wires
// the real changelog.
type PolicyGrammarDiffSource func(ctx context.Context, fromVersion, toVersion string) (string, error)

type grammarDiffKey struct{}

// WithPolicyGrammarDiffSource wires a diff fetcher into the request
// context for policy_migration_helper.
func WithPolicyGrammarDiffSource(ctx context.Context, src PolicyGrammarDiffSource) context.Context {
	if src == nil {
		return ctx
	}
	return context.WithValue(ctx, grammarDiffKey{}, src)
}

func policyMigrationHelperPrompt() PromptEntry {
	return PromptEntry{
		Descriptor: Prompt{
			Name:        policyMigrationHelperPromptName,
			Description: "Convert a Cordum policy bundle between grammar versions using a diff of grammar changes.",
			Arguments: []PromptArgument{
				{Name: "from_version", Description: "Current bundle version (e.g. 2024-09-01).", Required: true},
				{Name: "to_version", Description: "Target bundle version (e.g. 2025-03-01).", Required: true},
			},
		},
		Render: func(ctx context.Context, args map[string]string) ([]PromptMessage, string, error) {
			from := strings.TrimSpace(args["from_version"])
			to := strings.TrimSpace(args["to_version"])
			if from == to {
				return nil, "", fmt.Errorf("%w: from_version and to_version must differ", ErrPromptInvalidArgs)
			}
			var diff string
			var fetchErr error
			if src, ok := ctx.Value(grammarDiffKey{}).(PolicyGrammarDiffSource); ok && src != nil {
				diff, fetchErr = src(ctx, from, to)
			}
			systemText := strings.Join([]string{
				"You are a Cordum policy-migration helper. You translate a safety-policy YAML bundle from one Cordum grammar version to another.",
				"",
				"Your response must:",
				"1. Apply every grammar change in the diff exactly once per affected rule.",
				"2. Preserve every rule's id, decision, and reason fields unchanged unless the diff explicitly renames them.",
				"3. When a rule would lose information under the target grammar, call out the lossy field under a `# MIGRATION-REVIEW` comment rather than silently dropping it.",
				"4. Output the converted bundle wrapped in ```yaml fences.",
				"5. End with a one-line reminder that the operator must re-sign + simulate the bundle before promoting.",
			}, "\n")
			userText := migrationRequestText(from, to, diff, fetchErr)
			return []PromptMessage{
				{Role: "system", Content: PromptBlock{Type: "text", Text: systemText}},
				{Role: "user", Content: PromptBlock{Type: "text", Text: userText}},
			}, "", nil
		},
	}
}

func migrationRequestText(from, to, diff string, fetchErr error) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Convert my policy bundle from version %s → %s.\n\n", from, to)
	if fetchErr != nil {
		fmt.Fprintf(&sb, "grammar_diff_lookup: failed (%s)\nPaste the grammar change notes into the conversation along with the bundle before I can translate.\n", fetchErr)
		return sb.String()
	}
	if strings.TrimSpace(diff) == "" {
		sb.WriteString("grammar_diff_lookup: no source wired\n\nPaste the grammar change notes (or a pointer to the release changelog) into the conversation along with the bundle before I can translate.\n")
		return sb.String()
	}
	sb.WriteString("grammar_diff:\n")
	for _, line := range strings.Split(diff, "\n") {
		sb.WriteString("  ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\nNow paste the bundle to convert.\n")
	return sb.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
