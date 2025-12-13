package templates

import "sync"

// Template defines the structure for a workflow template.
type Template struct {
	Metadata   TemplateMetadata    `json:"metadata" yaml:"metadata"`
	Preview    TemplatePreview     `json:"preview" yaml:"preview"`
	Config     TemplateConfig      `json:"config" yaml:"config"`     // Default configuration for the workflow
	Workflow   any                 `json:"workflow" yaml:"workflow"` // The actual workflow definition (can be YAML or JSON)
	Parameters []TemplateParameter `json:"parameters" yaml:"parameters"`
}

// TemplateMetadata holds general information about the template.
type TemplateMetadata struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Category    string   `json:"category" yaml:"category"`
	Tags        []string `json:"tags" yaml:"tags"`
	Author      string   `json:"author" yaml:"author"`
	Version     string   `json:"version" yaml:"version"`
	Visibility  string   `json:"visibility" yaml:"visibility"` // "public_official", "org_shared", "team_private", "personal"
}

// TemplatePreview holds information for UI display before application.
type TemplatePreview struct {
	Steps               int      `json:"steps" yaml:"steps"`
	EstimatedDuration   string   `json:"estimated_duration" yaml:"estimated_duration"`
	ModelsUsed          []string `json:"models_used" yaml:"models_used"`
	EstimatedCostPerRun string   `json:"estimated_cost_per_run" yaml:"estimated_cost_per_run"`
}

// TemplateConfig holds default EffectiveConfig categories for the template.
// This is a subset of config.EffectiveConfig relevant to templates.
type TemplateConfig struct {
	Safety any `json:"safety,omitempty" yaml:"safety,omitempty"`
	Budget any `json:"budget,omitempty" yaml:"budget,omitempty"`
	Retry  any `json:"retry,omitempty" yaml:"retry,omitempty"`
	Models any `json:"models,omitempty" yaml:"models,omitempty"`
	// Add other relevant configs as needed
}

// TemplateParameter defines a user-configurable parameter for the template.
type TemplateParameter struct {
	Name        string              `json:"name" yaml:"name"`
	Type        string              `json:"type" yaml:"type"` // e.g., "number", "boolean", "select"
	Default     any                 `json:"default" yaml:"default"`
	Min         any                 `json:"min,omitempty" yaml:"min,omitempty"`
	Max         any                 `json:"max,omitempty" yaml:"max,omitempty"`
	Options     []map[string]string `json:"options,omitempty" yaml:"options,omitempty"` // For "select" type
	Description string              `json:"description" yaml:"description"`
}

// TemplateStore manages templates. (Placeholder for in-memory store)
type TemplateStore struct {
	mu        sync.RWMutex
	templates map[string]Template // id -> template
}

// NewTemplateStore creates a new in-memory template store.
func NewTemplateStore() *TemplateStore {
	return &TemplateStore{
		templates: make(map[string]Template),
	}
}

// AddTemplate adds a new template to the store.
func (ts *TemplateStore) AddTemplate(t Template) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.templates[t.Metadata.Name] = t // Using name as ID for simplicity
}

// GetTemplate retrieves a template by name.
func (ts *TemplateStore) GetTemplate(name string) (Template, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	t, ok := ts.templates[name]
	return t, ok
}

// ListTemplates lists all templates in the store.
func (ts *TemplateStore) ListTemplates() []Template {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	list := make([]Template, 0, len(ts.templates))
	for _, t := range ts.templates {
		list = append(list, t)
	}
	return list
}

// Dummy templates to pre-populate the store
func (ts *TemplateStore) Prepopulate() {
	ts.AddTemplate(Template{
		Metadata: TemplateMetadata{
			Name:        "Standard Code Review",
			Description: "Comprehensive code review with security scanning",
			Category:    "development",
			Tags:        []string{"code", "security", "review"},
			Author:      "coretex",
			Version:     "1.0",
			Visibility:  "public_official",
		},
		Preview: TemplatePreview{
			Steps:               5,
			EstimatedDuration:   "2-5 minutes",
			ModelsUsed:          []string{"gpt-4", "llama-3"},
			EstimatedCostPerRun: "$0.15 - $0.50",
		},
		Config: TemplateConfig{
			Safety: map[string]any{
				"pii_detection_enabled": true,
				"pii_action":            "redact",
				"injection_detection":   true,
			},
			Budget: map[string]any{
				"per_workflow_max_usd": 1.00,
			},
			Retry: map[string]any{
				"max_retries":     2,
				"initial_backoff": "1s",
			},
		},
		Workflow: map[string]any{
			"timeout": "10m",
			"steps": []any{
				map[string]any{"name": "Fetch Repo", "action": "git_clone"},
				map[string]any{"name": "Static Analysis", "action": "code_scan"},
				map[string]any{"name": "LLM Review", "action": "llm_review"},
				map[string]any{"name": "PII Scan", "action": "pii_scan"},
				map[string]any{"name": "Generate Report", "action": "report_gen"},
			},
		},
		Parameters: []TemplateParameter{
			{Name: "max_files", Type: "number", Default: 100, Min: 1, Max: 500, Description: "Maximum number of files to review"},
			{Name: "include_tests", Type: "boolean", Default: true, Description: "Include test file review"},
			{Name: "security_level", Type: "select", Default: "standard", Options: []map[string]string{{"value": "basic", "label": "Basic"}, {"value": "standard", "label": "Standard"}, {"value": "thorough", "label": "Thorough"}}, Description: "Security scanning depth"},
		},
	})

	ts.AddTemplate(Template{
		Metadata: TemplateMetadata{
			Name:        "Simple Chatbot",
			Description: "A basic chatbot workflow for customer support",
			Category:    "customer_support",
			Tags:        []string{"chat", "llm"},
			Author:      "coretex",
			Version:     "1.0",
			Visibility:  "public_official",
		},
		Preview: TemplatePreview{
			Steps:               3,
			EstimatedDuration:   "10-30 seconds",
			ModelsUsed:          []string{"llama-3"},
			EstimatedCostPerRun: "$0.01 - $0.05",
		},
		Config: TemplateConfig{
			Models: map[string]any{
				"default_model": "llama-3",
			},
		},
		Workflow: map[string]any{
			"timeout": "1m",
			"steps": []any{
				map[string]any{"name": "Receive Message", "action": "receive_msg"},
				map[string]any{"name": "LLM Response", "action": "llm_chat"},
				map[string]any{"name": "Send Response", "action": "send_msg"},
			},
		},
		Parameters: []TemplateParameter{
			{Name: "model_temperature", Type: "number", Default: 0.7, Min: 0.0, Max: 1.0, Description: "Creativity of the model"},
		},
	})
}
