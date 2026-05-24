package capsdk

const (
	SubjectSubmit        = "sys.job.submit"
	SubjectResult        = "sys.job.result"
	SubjectHeartbeat     = "sys.heartbeat"
	SubjectProgress      = "sys.job.progress"
	SubjectCancel        = "sys.job.cancel"
	SubjectDLQ           = "sys.job.dlq"
	SubjectWorkflowEvent = "sys.workflow.event"
	SubjectAlert         = "sys.alert"
	SubjectHandshake     = "sys.handshake"
	SubjectConfigChanged = "sys.config.changed"
	SubjectAuditExport   = "sys.audit.export"
	SubjectApprovalGate          = "sys.approval.gate"
	SubjectWorkflowApprovalGate = "job.cordum.approval-gate"

	// DefaultProtocolVersion matches CAP wire version 1.
	// Corresponds to CAP SDK v2.13.1 — wire protocol version remains 1.
	DefaultProtocolVersion = 1
)
