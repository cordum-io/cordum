package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	coreschema "github.com/cordum/cordum/core/infra/schema"
)

var (
	ErrToolNotFound     = errors.New("mcp tool not found")
	ErrToolDisabled     = errors.New("mcp tool disabled")
	ErrResourceNotFound = errors.New("mcp resource not found")
	ErrResourceDisabled = errors.New("mcp resource disabled")
)

type toolEntry struct {
	tool    Tool
	handler ToolHandler
}

// ToolRegistry stores MCP tools and handlers.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]toolEntry

	cfgMu   sync.RWMutex
	cfgData map[string]any

	gateMu sync.RWMutex
	gate   ApprovalGate

	auditMu sync.RWMutex
	audit   DenyAuditor

	// scopeEnforce controls whether Call applies FilterForIdentity.
	// Default false so unit tests can register tools and invoke handlers
	// without wiring an identity into every ctx. Production callers
	// (gateway HTTP transport, cordum-mcp stdio) flip it on via
	// SetScopeEnforcement during setup.
	scopeEnforce atomic.Bool

	// cache memoises ListTools output by identity + config_version.
	cache *filterCache
}

// DenyAuditor is invoked by the tool registry when a tools/call is
// rejected by the scope filter. Implementations forward the event to
// the SIEM chain. Runs on the dispatch goroutine so implementations
// must be non-blocking; wrap expensive work in a goroutine if needed.
type DenyAuditor interface {
	ToolDenied(ctx context.Context, event DenyEvent)
}

// DenyEvent is the structured payload the auditor receives. Extra is
// optional and lets the auditor enrich the event (e.g. with tenant
// derived from ctx) before forwarding.
type DenyEvent struct {
	ToolName  string
	SubReason DenyReason
	AgentID   string
}

// SetDenyAuditor wires the auditor. Passing nil disables the audit
// call — this is the default for tests and for gateway-less deploys.
func (r *ToolRegistry) SetDenyAuditor(a DenyAuditor) {
	if r == nil {
		return
	}
	r.auditMu.Lock()
	r.audit = a
	r.auditMu.Unlock()
}

// SetScopeEnforcement toggles Call's scope filter. Production deploys
// (gateway HTTP transport, cordum-mcp stdio) MUST call this with true
// during setup — tests and isolated handler validators can leave it
// off so they don't have to carry an identity through ctx. Enforcement
// is orthogonal to ListTools, which always filters (its fail-closed
// behaviour is what keeps tool inventories from leaking to callers
// that lack an identity).
func (r *ToolRegistry) SetScopeEnforcement(on bool) {
	if r == nil {
		return
	}
	r.scopeEnforce.Store(on)
}

// ScopeEnforced reports whether Call currently applies the scope filter.
func (r *ToolRegistry) ScopeEnforced() bool {
	if r == nil {
		return false
	}
	return r.scopeEnforce.Load()
}

func (r *ToolRegistry) denyAuditor() DenyAuditor {
	if r == nil {
		return nil
	}
	r.auditMu.RLock()
	defer r.auditMu.RUnlock()
	return r.audit
}

// ApprovalGate is the gateway-level contract the MCP server uses to
// enforce RequiresApproval. The gateway provides an implementation that
// wraps the MCPApprovalStore: it either returns ApprovalDecisionAllow
// (pre-approval exists and was consumed), or ApprovalDecisionGated
// (fresh approval enqueued, tools/call must fail with -32099).
type ApprovalGate interface {
	// Check is invoked by ToolRegistry.Call when the tool has
	// RequiresApproval=true (or a runtime rule matches). Implementations
	// consult the pre-approval index, and when no valid pre-approval
	// exists enqueue a fresh approval and return a populated
	// *ApprovalRequired.
	//
	// Returning (nil, nil) means the call is authorised. Returning
	// (*ApprovalRequired, nil) means the call is gated; the server maps
	// it to JSON-RPC code -32099. Any non-nil error is a hard failure
	// that bubbles up as JSON-RPC -32603.
	Check(ctx context.Context, tool Tool, paramsJSON json.RawMessage) (*ApprovalRequired, error)
}

// ApprovalRequired is the structured payload the gate returns when a
// tools/call must wait for human approval. It is returned as an error
// value (see Error() below) so callers can use errors.As to switch on
// it without depending on a sentinel.
type ApprovalRequired struct {
	// ApprovalID is the freshly enqueued approval's ID. Clients use it
	// to poll /api/v1/approvals or to invoke `cordumctl mcp approve`.
	ApprovalID string `json:"approval_id"`
	// Reason is an operator-facing summary of why approval is required.
	Reason string `json:"reason,omitempty"`
	// Tool is the tool name the call targeted.
	Tool string `json:"tool"`
}

// Error satisfies the error interface so ApprovalRequired can flow
// through the regular ToolRegistry.Call return path. The server layer
// switches on this type via errors.As to emit JSON-RPC -32099.
func (a *ApprovalRequired) Error() string {
	if a == nil {
		return "mcp: approval required"
	}
	return fmt.Sprintf("mcp: approval required for %s (approval_id=%s)", a.Tool, a.ApprovalID)
}

// SetApprovalGate wires the gate. Passing nil disables the approval
// check — this is the default for tests and for gateway-less deploys
// (e.g. the stdio MCP server used as a developer tool).
func (r *ToolRegistry) SetApprovalGate(gate ApprovalGate) {
	if r == nil {
		return
	}
	r.gateMu.Lock()
	r.gate = gate
	r.gateMu.Unlock()
}

// approvalGate returns the current gate under read lock.
func (r *ToolRegistry) approvalGate() ApprovalGate {
	if r == nil {
		return nil
	}
	r.gateMu.RLock()
	defer r.gateMu.RUnlock()
	return r.gate
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]toolEntry),
		cache: newFilterCache(),
	}
}

// SetConfig updates config used for enable/disable lookups and scope
// policy overrides. Bumping the filter cache version here is what
// makes identity changes "take effect immediately" (see plan DoD).
func (r *ToolRegistry) SetConfig(cfg map[string]any) {
	if r == nil {
		return
	}
	r.cfgMu.Lock()
	r.cfgData = cloneConfigMap(cfg)
	r.cfgMu.Unlock()
	if r.cache != nil {
		r.cache.bumpVersion()
	}
}

// Register adds or replaces a tool handler.
func (r *ToolRegistry) Register(tool Tool, handler ToolHandler) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}
	tool.Name = strings.TrimSpace(tool.Name)
	if tool.Name == "" {
		return fmt.Errorf("%w: tool name is required", ErrInvalidParams)
	}
	if handler == nil {
		return fmt.Errorf("%w: tool handler is required", ErrInvalidParams)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = toolEntry{tool: tool, handler: handler}
	return nil
}

// List returns all enabled registered tools. It is kept for backward
// compatibility (status counters, unfiltered diagnostics). New
// on-the-wire callers should prefer ListTools(ctx) which applies the
// scope filter against the caller's AgentIdentity.
func (r *ToolRegistry) List() []Tool {
	return r.ListToolsUnfiltered()
}

// ListToolsUnfiltered returns every enabled registered tool regardless
// of identity. Intended for diagnostic surfaces such as the admin
// tool-catalogue page in the dashboard and the `cordumctl mcp tools
// list` admin command — callers MUST gate it with a role check.
func (r *ToolRegistry) ListToolsUnfiltered() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, entry := range r.tools {
		if !r.isToolEnabled(entry.tool.Name) {
			continue
		}
		out = append(out, r.effectiveTool(entry.tool))
	}
	return out
}

// ListTools resolves the AgentIdentity from ctx (set by transports via
// mcp.ContextWithIdentity) and returns the subset of enabled tools the
// identity is allowed to see. Fail-closed: when ctx has no identity
// the result is empty.
//
// Results are memoised per (identity fingerprint, config_version) with
// a 60s TTL. SetConfig bumps the version so runtime policy changes
// invalidate the cache immediately.
//
// Callers that want unfiltered output for admin/diagnostic surfaces
// must call ListToolsUnfiltered directly — this function intentionally
// has no escape hatch.
func (r *ToolRegistry) ListTools(ctx context.Context) []Tool {
	if r == nil {
		return nil
	}
	id := IdentityFromContext(ctx)
	if r.cache != nil {
		if cached, ok := r.cache.get(id); ok {
			return cached
		}
	}
	all := r.ListToolsUnfiltered()
	out := FilterForIdentity(all, id)
	if r.cache != nil {
		r.cache.put(id, out)
	}
	return out
}

// effectiveTool returns the tool descriptor after runtime-config
// overrides are merged in. This is the version that should be
// advertised to clients and matched against the scope filter — a
// runtime policy can tighten a tool's required risk tier or add
// required data classifications without a code change.
func (r *ToolRegistry) effectiveTool(tool Tool) Tool {
	return r.applyToolPolicyOverrides(tool)
}

// applyToolPolicyOverrides is the default passthrough. Runtime policy
// merging lives in policy_overrides.go (step 7) so this file stays
// focused on registry semantics.
func (r *ToolRegistry) applyToolPolicyOverrides(tool Tool) Tool {
	if r == nil {
		return tool
	}
	return r.mergeScopePolicyForTool(tool)
}

// Call executes a named enabled tool after optional JSON Schema validation.
func (r *ToolRegistry) Call(ctx context.Context, name string, params json.RawMessage) (*ToolCallResult, error) {
	if r == nil {
		return nil, fmt.Errorf("tool registry is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidParams)
	}
	r.mu.RLock()
	entry, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrToolNotFound
	}
	if !r.isToolEnabled(name) {
		return nil, ErrToolDisabled
	}
	// Scope filter runs FIRST — before approval gating, before schema
	// validation — so a principal that lacks the scope for a tool sees
	// a clean "not authorized" denial instead of leaking either the
	// approval workflow or the tool's input schema. Filter rejection
	// always produces a -32098 with the specific sub-reason so clients
	// can tell "tier too low" from "not on your allowlist".
	//
	// Enforcement is toggled off by default so isolated handler tests
	// don't have to thread an identity through ctx; production setup
	// (gateway HTTP transport + cordum-mcp stdio) enables it at boot.
	effectiveTool := r.effectiveTool(entry.tool)
	identity := IdentityFromContext(ctx)
	if r.scopeEnforce.Load() {
		if deny := EvaluateForIdentity(effectiveTool, identity); deny != DenyReasonNone {
			agentID := ""
			if identity != nil {
				agentID = identity.ID
			}
			err := &NotAuthorized{
				Tool:      entry.tool.Name,
				SubReason: deny,
				AgentID:   agentID,
			}
			if auditor := r.denyAuditor(); auditor != nil {
				auditor.ToolDenied(ctx, DenyEvent{
					ToolName:  entry.tool.Name,
					SubReason: deny,
					AgentID:   agentID,
				})
			}
			return nil, err
		}
	}
	// Approval gating runs before schema validation so an operator sees a
	// clean "approval required" response even on a malformed call — the
	// gate is about the intent to call, not the correctness of the args.
	// When the gate is unset (dev/test) the check is a no-op.
	//
	// effectiveApprovalForTool merges the tool's code-time metadata with
	// runtime config, so a flag flipped in /api/v1/config takes effect
	// on the very next call without restarting the server.
	effectiveGated, effectiveScope := r.effectiveApprovalForTool(entry.tool)
	if effectiveGated {
		effectiveTool := entry.tool
		effectiveTool.RequiresApproval = true
		if effectiveScope != "" {
			effectiveTool.ApprovalScope = effectiveScope
		}
		if gate := r.approvalGate(); gate != nil {
			gated, err := gate.Check(ctx, effectiveTool, params)
			if err != nil {
				return nil, err
			}
			if gated != nil {
				// Return as error so server.go can map via errors.As.
				gated.Tool = entry.tool.Name
				return nil, gated
			}
		}
	}
	if len(entry.tool.InputSchema) > 0 {
		parsed := map[string]any{}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &parsed); err != nil {
				return nil, fmt.Errorf("%w: invalid params JSON: %v", ErrInvalidParams, err)
			}
		}
		if err := coreschema.ValidateMap(entry.tool.InputSchema, parsed); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
	}
	started := time.Now()
	result, err := entry.handler(ctx, params)
	if err != nil {
		return nil, err
	}
	r.emitToolCallAudit(ctx, entry.tool, started, result)
	return result, nil
}

func (r *ToolRegistry) isToolEnabled(name string) bool {
	path := []string{"mcp", "tools", name, "enabled"}
	enabled, ok := r.lookupConfigBool(path...)
	if !ok {
		return true
	}
	return enabled
}

// effectiveApprovalForTool returns the RequiresApproval/ApprovalScope
// values that should apply to a tool after merging the registered code
// metadata with runtime config. Runtime config WINS over code —
// operators can flip a safe tool to require approval without a redeploy,
// which is the whole point of the runtime policy surface.
//
// Config shape (stored at /api/v1/config?scope=system&id=mcp_policy):
//
//	{
//	  "tools": [
//	    {"tool_name_pattern": "dangerous.*",
//	     "requires_approval": true,
//	     "approval_scope": "dangerous"}
//	  ]
//	}
//
// Rules are evaluated in order; the FIRST matching rule wins. A
// rule that sets requires_approval=false can explicitly turn off a
// code-gated tool (useful for CI bypass with a signed operator note).
func (r *ToolRegistry) effectiveApprovalForTool(tool Tool) (bool, string) {
	requires := tool.RequiresApproval
	scope := tool.ApprovalScope

	r.cfgMu.RLock()
	cfg := r.cfgData
	r.cfgMu.RUnlock()
	if cfg == nil {
		return requires, scope
	}
	rules, ok := cfg["tools"].([]any)
	if !ok {
		// Accept either `{"tools": [...]}` or `{"mcp_policy": {"tools": [...]}}`
		// so the same config payload can be served either as a root
		// document or embedded under its config ID.
		if root, rootOK := cfg["mcp_policy"].(map[string]any); rootOK {
			if inner, innerOK := root["tools"].([]any); innerOK {
				rules = inner
				ok = true
			}
		}
		if !ok {
			return requires, scope
		}
	}
	for _, raw := range rules {
		rule, isMap := raw.(map[string]any)
		if !isMap {
			continue
		}
		pattern, _ := rule["tool_name_pattern"].(string)
		if pattern == "" {
			continue
		}
		if !globMatch(pattern, tool.Name) {
			continue
		}
		if v, present := rule["requires_approval"]; present {
			if b, okB := v.(bool); okB {
				requires = b
			}
		}
		if v, present := rule["approval_scope"]; present {
			if s, okS := v.(string); okS {
				scope = s
			}
		}
		// First matching rule wins — more specific rules should be
		// placed earlier in the list by the operator.
		break
	}
	return requires, scope
}

// globMatch is a tiny star-glob matcher: `*` matches zero or more
// characters, any other character is literal. Go's path.Match is almost
// right but treats `/` specially, which tool names legitimately use
// (e.g. "fs/read").
func globMatch(pattern, s string) bool {
	// Fast path: exact match or pure-wildcard.
	if pattern == s {
		return true
	}
	if pattern == "*" {
		return true
	}
	return globMatchSlice([]rune(pattern), []rune(s))
}

func globMatchSlice(pattern, s []rune) bool {
	// Classic two-pointer star-glob recursion with memoisation via
	// shrinking the s slice. Patterns typical for MCP tools are short
	// (<40 chars) so O(n*m) is fine.
	pi, si := 0, 0
	starPi, starSi := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pattern) && pattern[pi] == '*':
			starPi = pi
			starSi = si
			pi++
		case pi < len(pattern) && pattern[pi] == s[si]:
			pi++
			si++
		case starPi != -1:
			pi = starPi + 1
			starSi++
			si = starSi
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

type resourceEntry struct {
	resource Resource
	handler  ResourceHandler
}

type templateEntry struct {
	template ResourceTemplate
	handler  ResourceHandler
	re       *regexp.Regexp
}

// ResourceRegistry stores MCP resources and URI templates.
type ResourceRegistry struct {
	mu         sync.RWMutex
	resources  map[string]resourceEntry
	templates  []templateEntry
	byTemplate map[string]templateEntry

	cfgMu   sync.RWMutex
	cfgData map[string]any
}

// NewResourceRegistry creates an empty resource registry.
func NewResourceRegistry() *ResourceRegistry {
	return &ResourceRegistry{
		resources:  make(map[string]resourceEntry),
		templates:  []templateEntry{},
		byTemplate: make(map[string]templateEntry),
	}
}

// SetConfig updates config used for enable/disable lookups.
func (r *ResourceRegistry) SetConfig(cfg map[string]any) {
	if r == nil {
		return
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	r.cfgData = cloneConfigMap(cfg)
}

// Register adds or replaces an exact resource URI handler.
func (r *ResourceRegistry) Register(resource Resource, handler ResourceHandler) error {
	if r == nil {
		return fmt.Errorf("resource registry is nil")
	}
	resource.URI = strings.TrimSpace(resource.URI)
	resource.Name = strings.TrimSpace(resource.Name)
	if resource.URI == "" {
		return fmt.Errorf("%w: resource uri is required", ErrInvalidParams)
	}
	if resource.Name == "" {
		return fmt.Errorf("%w: resource name is required", ErrInvalidParams)
	}
	if handler == nil {
		return fmt.Errorf("%w: resource handler is required", ErrInvalidParams)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resources[resource.URI] = resourceEntry{resource: resource, handler: handler}
	return nil
}

// RegisterTemplate adds or replaces a URI-template handler.
func (r *ResourceRegistry) RegisterTemplate(template ResourceTemplate, handler ResourceHandler) error {
	if r == nil {
		return fmt.Errorf("resource registry is nil")
	}
	template.URITemplate = strings.TrimSpace(template.URITemplate)
	template.Name = strings.TrimSpace(template.Name)
	if template.URITemplate == "" {
		return fmt.Errorf("%w: resource template uriTemplate is required", ErrInvalidParams)
	}
	if template.Name == "" {
		return fmt.Errorf("%w: resource template name is required", ErrInvalidParams)
	}
	if handler == nil {
		return fmt.Errorf("%w: resource template handler is required", ErrInvalidParams)
	}
	pattern, err := compileURITemplate(template.URITemplate)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	entry := templateEntry{
		template: template,
		handler:  handler,
		re:       pattern,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byTemplate[template.URITemplate]; ok {
		for idx := range r.templates {
			if r.templates[idx].template.URITemplate == existing.template.URITemplate {
				r.templates[idx] = entry
				r.byTemplate[template.URITemplate] = entry
				return nil
			}
		}
	}
	r.templates = append(r.templates, entry)
	r.byTemplate[template.URITemplate] = entry
	return nil
}

// List returns all enabled exact resources.
func (r *ResourceRegistry) List() []Resource {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Resource, 0, len(r.resources))
	for _, entry := range r.resources {
		if !r.isResourceEnabled(entry.resource.Name) {
			continue
		}
		out = append(out, entry.resource)
	}
	return out
}

// ListTemplates returns enabled resource URI templates.
func (r *ResourceRegistry) ListTemplates() []ResourceTemplate {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ResourceTemplate, 0, len(r.templates))
	for _, entry := range r.templates {
		if !r.isResourceEnabled(entry.template.Name) {
			continue
		}
		out = append(out, entry.template)
	}
	return out
}

// Read resolves a URI to an exact resource or matching template.
func (r *ResourceRegistry) Read(ctx context.Context, uri string) (*ResourceContents, error) {
	if r == nil {
		return nil, fmt.Errorf("resource registry is nil")
	}
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fmt.Errorf("%w: uri is required", ErrInvalidParams)
	}
	canonical := canonicalResourceURI(uri)

	r.mu.RLock()
	entry, ok := r.resources[uri]
	if !ok && canonical != "" && canonical != uri {
		entry, ok = r.resources[canonical]
	}
	if ok {
		r.mu.RUnlock()
		if !r.isResourceEnabled(entry.resource.Name) {
			return nil, ErrResourceDisabled
		}
		return entry.handler(ctx, uri)
	}
	templates := make([]templateEntry, len(r.templates))
	copy(templates, r.templates)
	r.mu.RUnlock()

	for _, tmpl := range templates {
		if !tmpl.re.MatchString(uri) && (canonical == "" || !tmpl.re.MatchString(canonical)) {
			continue
		}
		if !r.isResourceEnabled(tmpl.template.Name) {
			return nil, ErrResourceDisabled
		}
		return tmpl.handler(ctx, uri)
	}
	return nil, ErrResourceNotFound
}

func canonicalResourceURI(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return ""
	}
	base := parsed.Scheme + "://" + parsed.Host + parsed.Path
	return strings.TrimSpace(base)
}

func (r *ResourceRegistry) isResourceEnabled(name string) bool {
	path := []string{"mcp", "resources", name, "enabled"}
	enabled, ok := r.lookupConfigBool(path...)
	if !ok {
		return true
	}
	return enabled
}

func (r *ToolRegistry) lookupConfigBool(path ...string) (bool, bool) {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return lookupConfigBool(r.cfgData, path...)
}

func (r *ResourceRegistry) lookupConfigBool(path ...string) (bool, bool) {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return lookupConfigBool(r.cfgData, path...)
}

func lookupConfigBool(cfg map[string]any, path ...string) (bool, bool) {
	if len(path) == 0 || cfg == nil {
		return false, false
	}
	var cur any = cfg
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false, false
		}
		cur, ok = m[key]
		if !ok {
			return false, false
		}
	}
	switch v := cur.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
	}
	return false, false
}

func compileURITemplate(uriTemplate string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(uriTemplate); {
		ch := uriTemplate[i]
		if ch == '{' {
			end := strings.IndexByte(uriTemplate[i:], '}')
			if end <= 1 {
				return nil, fmt.Errorf("invalid uri template %q", uriTemplate)
			}
			end += i
			name := strings.TrimSpace(uriTemplate[i+1 : end])
			if name == "" {
				return nil, fmt.Errorf("invalid uri template %q", uriTemplate)
			}
			b.WriteString("([^/?#]+)")
			i = end + 1
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(ch)))
		i++
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func cloneConfigMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(typed))
		for k, child := range typed {
			cp[k] = cloneAny(child)
		}
		return cp
	case []any:
		cp := make([]any, len(typed))
		for i := range typed {
			cp[i] = cloneAny(typed[i])
		}
		return cp
	default:
		return typed
	}
}
