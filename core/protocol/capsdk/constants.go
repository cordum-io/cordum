package capsdk

const (
	SubjectSubmit        = "sys.job.submit"
	SubjectResult        = "sys.job.result"
	SubjectHeartbeat     = "sys.heartbeat"
	SubjectProgress      = "sys.job.progress"
	SubjectCancel        = "sys.job.cancel"
	SubjectDLQ           = "sys.job.dlq"
	SubjectWorkflowEvent = "sys.workflow.event"
	SubjectAlert          = "sys.alert"
	SubjectHandshake      = "sys.handshake"
	SubjectConfigChanged  = "sys.config.changed"
	SubjectAuditExport   = "sys.audit.export"

	// DefaultProtocolVersion matches CAP wire version 1.
	// Corresponds to CAP SDK v2.5.2 — wire protocol version remains 1.
	DefaultProtocolVersion = 1
)
