package gateway

import (
	"errors"
	"fmt"
	"regexp"

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
