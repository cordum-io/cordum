package capsdk

const (
	SubjectSubmit        = "sys.job.submit"
	SubjectResult        = "sys.job.result"
	SubjectHeartbeat     = "sys.heartbeat"
	SubjectProgress      = "sys.job.progress"
	SubjectCancel        = "sys.job.cancel"
	SubjectDLQ           = "sys.job.dlq"
	SubjectWorkflowEvent = "sys.workflow.event"

	// DefaultProtocolVersion matches CAP wire version 1.
	DefaultProtocolVersion = 1
)
