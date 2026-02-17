package runtime

import (
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
)

// ValidationError describes a single CAP protocol validation failure.
type ValidationError = capsdk.ValidationError

// Sentinel validation errors.
var (
	ErrEmptyJobID   = capsdk.ErrEmptyJobID
	ErrEmptyTopic   = capsdk.ErrEmptyTopic
	ErrEmptyTraceID = capsdk.ErrEmptyTraceID
	ErrEmptySender  = capsdk.ErrEmptySender
)

// ValidateJobRequest checks semantic constraints on a JobRequest.
func ValidateJobRequest(req *agentv1.JobRequest) error {
	return capsdk.ValidateJobRequest(req)
}

// ValidateJobResult checks semantic constraints on a JobResult.
func ValidateJobResult(res *agentv1.JobResult) error {
	return capsdk.ValidateJobResult(res)
}
