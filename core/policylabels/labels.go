package policylabels

import "strings"

const (
	// PolicyAttachmentID carries the synthetic bundle key used to scope a
	// job/session-specific policy override at Safety Kernel evaluate time.
	PolicyAttachmentID = "policy.attachment_id"
)

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
