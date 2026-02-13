package gateway

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	wf "github.com/cordum/cordum/core/workflow"
)

const maxWorkflowStepIDLen = 64

var workflowStepIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateWorkflowStepID(stepID string) error {
	if stepID == "" {
		return errors.New("workflow step id required")
	}
	if len(stepID) > maxWorkflowStepIDLen {
		return fmt.Errorf("workflow step id %q exceeds %d characters", stepID, maxWorkflowStepIDLen)
	}
	if !workflowStepIDPattern.MatchString(stepID) {
		return fmt.Errorf("workflow step id %q must match %s", stepID, workflowStepIDPattern.String())
	}
	return nil
}

func validateWorkflowSteps(steps map[string]wf.Step) error {
	for id := range steps {
		if err := validateWorkflowStepID(id); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowStepMap(steps map[string]any) error {
	for id := range steps {
		if err := validateWorkflowStepID(id); err != nil {
			return err
		}
	}
	return nil
}

// validateDAG checks for circular dependencies and dangling references in a step graph.
// Uses DFS with three-color marking: 0=unvisited, 1=in-progress, 2=done.
func validateDAG(steps map[string]wf.Step) error {
	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS path
		black = 2 // fully processed
	)
	color := make(map[string]int, len(steps))

	// Check for dangling references first.
	for id, step := range steps {
		for _, dep := range step.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if _, ok := steps[dep]; !ok {
				return fmt.Errorf("step %q depends on non-existent step %q", id, dep)
			}
		}
	}

	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		if color[id] == black {
			return nil
		}
		if color[id] == gray {
			// Build cycle description from path.
			cycle := append(path, id)
			return fmt.Errorf("circular dependency detected: %s", strings.Join(cycle, " -> "))
		}
		color[id] = gray
		step := steps[id]
		for _, dep := range step.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if err := visit(dep, append(path, id)); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}

	for id := range steps {
		if color[id] == white {
			if err := visit(id, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateDAGPtr is the same as validateDAG but for pointer-based step maps.
func validateDAGPtr(steps map[string]*wf.Step) error {
	flat := make(map[string]wf.Step, len(steps))
	for id, s := range steps {
		if s != nil {
			flat[id] = *s
		}
	}
	return validateDAG(flat)
}
