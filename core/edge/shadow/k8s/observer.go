package k8s

import "github.com/cordum/cordum/core/audit"

// Observer is the metrics + audit emission contract the Kubernetes
// shadow detector depends on. Production wiring backs this with a
// prometheus.Counter for findings + the shared audit.AuditSender;
// tests substitute a spy implementation that captures calls.
//
// The detector calls RecordFindingEmit exactly once per persisted
// finding (after store.CreateFinding succeeds) with bounded labels per
// design doc §13: signal is one of the §7.1 enum values, risk is one
// of low|medium|high|critical. tenant / cluster_id / namespace /
// workload_name are NEVER passed as labels — they are high-cardinality
// and live in the persisted finding + audit event payload instead.
type Observer interface {
	RecordFindingEmit(signal, risk string)
	EmitAudit(event audit.SIEMEvent)
}

// NoopObserver is the safe default for tests + production code paths
// that have not yet wired observability. Both methods are pure no-ops.
type NoopObserver struct{}

// NewNoopObserver returns an Observer that swallows every call.
func NewNoopObserver() Observer { return NoopObserver{} }

// RecordFindingEmit satisfies Observer.
func (NoopObserver) RecordFindingEmit(string, string) {}

// EmitAudit satisfies Observer.
func (NoopObserver) EmitAudit(audit.SIEMEvent) {}
