package edge

import (
	"strings"

	"github.com/cordum/cordum/core/policylabels"
)

const (
	// LabelPolicyAttachmentID carries the synthetic bundle key used to scope a
	// job/session-specific policy override at Safety Kernel evaluate time.
	LabelPolicyAttachmentID = policylabels.PolicyAttachmentID
)

// JobPolicyAttachmentID returns the synthetic bundle key for a Cordum job.
func JobPolicyAttachmentID(jobID string) string {
	return policylabels.JobAttachmentID(jobID)
}

// SessionPolicyAttachmentID returns the synthetic bundle key for an Edge session.
func SessionPolicyAttachmentID(sessionID string) string {
	return policylabels.SessionAttachmentID(sessionID)
}

// WithPolicyAttachmentLabel returns a label copy with attachmentID pinned.
func WithPolicyAttachmentLabel(labels Labels, attachmentID string) Labels {
	out := cloneLabels(labels)
	attachmentID = strings.TrimSpace(attachmentID)
	if attachmentID != "" {
		out[LabelPolicyAttachmentID] = attachmentID
	}
	return out
}
