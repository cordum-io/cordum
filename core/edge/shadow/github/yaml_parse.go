package github

import (
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// workflowSpec is the §8.1-relevant projection of a GitHub Actions
// workflow YAML. We parse only the structural fields the detector
// cares about (env keys, action `uses:` values, runner labels) so the
// data minimization contract holds even if someone smuggles secrets
// into adjacent unmodelled fields.
type workflowSpec struct {
	Env  map[string]yaml.Node       `yaml:"env"`
	Jobs map[string]workflowSpecJob `yaml:"jobs"`
}

type workflowSpecJob struct {
	Env    map[string]yaml.Node `yaml:"env"`
	RunsOn yaml.Node            `yaml:"runs-on"`
	Steps  []workflowSpecStep   `yaml:"steps"`
}

type workflowSpecStep struct {
	Uses string               `yaml:"uses"`
	Run  string               `yaml:"run"`
	Env  map[string]yaml.Node `yaml:"env"`
}

// parseWorkflowYAML is a tolerant unmarshal — malformed YAML returns
// nil so the rest of the scan keeps going. We deliberately do NOT
// surface parse errors to the detector loop because GitHub will
// happily store half-typed workflows that go-actions itself refuses,
// and the scanner shouldn't fan-out audit noise for that.
func parseWorkflowYAML(content string) *workflowSpec {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	spec := &workflowSpec{}
	if err := yaml.Unmarshal([]byte(content), spec); err != nil {
		return nil
	}
	return spec
}

// AllUses returns every step `uses:` value across every job in the
// workflow. Used by the missing_cordum_attach + agent_action_used
// extractors. Empty `uses:` (i.e. `run:`-only steps) are skipped.
func (s *workflowSpec) AllUses() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, job := range s.Jobs {
		for _, step := range job.Steps {
			if u := strings.TrimSpace(step.Uses); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

// HasRunLeadToken reports whether any `run:` step starts with a known
// agent command. It inspects only the leading token of each non-empty
// line and never returns or persists shell arguments.
func (s *workflowSpec) HasRunLeadToken(tokens []string) bool {
	if s == nil || len(tokens) == 0 {
		return false
	}
	allowed := map[string]struct{}{}
	for _, token := range tokens {
		if t := strings.ToLower(strings.TrimSpace(token)); t != "" {
			allowed[t] = struct{}{}
		}
	}
	for _, job := range s.Jobs {
		for _, step := range job.Steps {
			if runScriptHasLeadToken(step.Run, allowed) {
				return true
			}
		}
	}
	return false
}

func runScriptHasLeadToken(script string, allowed map[string]struct{}) bool {
	for _, line := range strings.Split(script, "\n") {
		token := leadingShellToken(line)
		if _, ok := allowed[token]; ok {
			return true
		}
	}
	return false
}

func leadingShellToken(line string) string {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, "- ")
	if s == "" || strings.HasPrefix(s, "#") {
		return ""
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(fields[0], `"'`))
}

// AllEnvKeys returns the SORTED, DEDUPED set of env-var NAMES used
// across workflow/job/step scopes. VALUES ARE NEVER READ — only keys.
// The §5.2 data-minimization contract depends on this method never
// touching map values, so any future change to inline value handling
// must update the contract comment in detector.go.
func (s *workflowSpec) AllEnvKeys() []string {
	if s == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for k := range s.Env {
		seen[k] = struct{}{}
	}
	for _, job := range s.Jobs {
		for k := range job.Env {
			seen[k] = struct{}{}
		}
		for _, step := range job.Steps {
			for k := range step.Env {
				seen[k] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
