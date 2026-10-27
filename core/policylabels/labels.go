package policylabels

import "strings"

const (
	// PolicyAttachmentID carries the synthetic bundle key used to scope a
	// job/session-specific policy override at Safety Kernel evaluate time.
	PolicyAttachmentID = "policy.attachment_id"
)

// WorkflowContextLabel reports whether key carries a job's workflow linkage:
// workflow_id, run_id and step_id, which the workflow engine writes, and
// workflow, workflow_run_id and workflow.*, which the Safety Kernel also reads
// as the workflow policy scope. Only the workflow engine may set them.
func WorkflowContextLabel(key string) bool {
	switch key {
	case "run_id", "step_id":
		return true
	}
	return workflowScopeLabel(key)
}

// WithWorkflowScope returns a copy of labels whose workflow policy scope is
// workflowID, a request's WorkflowId (a field only the workflow engine sets).
// Workflow scope labels are dropped first, so a label alone never claims a
// workflow's policy scope.
func WithWorkflowScope(labels map[string]string, workflowID string) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		if !workflowScopeLabel(k) {
			out[k] = v
		}
	}
	if id := strings.TrimSpace(workflowID); id != "" {
		out["workflow_id"] = id
	}
	return out
}

func workflowScopeLabel(key string) bool {
	switch key {
	case "workflow_id", "workflow", "workflow_run_id":
		return true
	}
	return strings.HasPrefix(key, "workflow.")
}

// JobAttachmentID returns the synthetic bundle key for a Cordum job.
func JobAttachmentID(jobID string) string {
	return attachmentID("job", jobID)
}

// SessionAttachmentID returns the synthetic bundle key for an Edge session.
func SessionAttachmentID(sessionID string) string {
	return attachmentID("session", sessionID)
}

func attachmentID(scope, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return scope + "/" + id + "/policy"
}
