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
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]toolEntry),
	}
}

// SetConfig updates config used for enable/disable lookups.
func (r *ToolRegistry) SetConfig(cfg map[string]any) {
	if r == nil {
		return
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	r.cfgData = cloneConfigMap(cfg)
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

// List returns all enabled registered tools.
func (r *ToolRegistry) List() []Tool {
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
		out = append(out, entry.tool)
	}
	return out
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
	if len(entry.tool.InputSchema) > 0 {
		payload := any(map[string]any{})
		if len(params) > 0 {
			payload = params
		}
		if err := coreschema.ValidateMap(entry.tool.InputSchema, payload); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
	}
	return entry.handler(ctx, params)
}

func (r *ToolRegistry) isToolEnabled(name string) bool {
	path := []string{"mcp", "tools", name, "enabled"}
	enabled, ok := r.lookupConfigBool(path...)
	if !ok {
		return true
	}
	return enabled
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

// Read resolves a URI to an exact resource or matching template and invokes its handler.
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
	// Replace placeholders like {id} with a single segment matcher.
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
