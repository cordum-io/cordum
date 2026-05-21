package safetykernel

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// LabelActionDescriptorJSON is the reserved Labels-map key the gateway
// uses to propagate a JSON-encoded ActionDescriptor across the gRPC
// boundary. The `_` prefix is what the gateway's existing label-strip
// (helpers.go injectContentLabels + clean loop) treats as "system-only"
// so this key never leaks back to clients in label echo responses.
const LabelActionDescriptorJSON = "_action.descriptor_json"

// wireActionGatePipeline installs the action-layer gate pipeline,
// request→descriptor extractor, and audit sink on the safety-kernel
// server. It is invoked from RunWithEntitlements once after the server
// is constructed but before grpcServer.Serve, so the registered handler
// observes a non-nil pipeline on the very first request.
//
// Kernel-side wiring is intentionally defense-in-depth: the gateway
// path enforces the primary action-gate decisions with full backend
// dependencies (ApprovalLookup, ChainVerifier). The kernel boot path
// runs without direct access to those stores, so destructive verbs and
// MCP calls reaching the kernel via a direct gRPC client (bypassing
// the gateway) fail closed at the mutation/MCP/provenance gates. The
// tenant/file/url gates remain fully functional because they need no
// backend lookups. Non-destructive non-MCP traffic with a populated
// ActionDescriptor still benefits from cross-tenant, sensitive-path,
// and metadata-service enforcement at the kernel.
//
// Returns an error when pipeline construction fails so the caller can
// fail-closed the entire boot — never start a kernel with no enforcement.
func wireActionGatePipeline(srv *server, auditExporter audit.AuditSender) error {
	if srv == nil {
		return nil
	}
	pipeline := actiongates.BuildProductionPipeline(actiongates.ProductionPipelineOptions{})
	if pipeline == nil {
		return errPipelineConstructionFailed
	}
	srv.SetActionGatePipeline(pipeline)
	srv.SetActionDescriptorExtractor(actionDescriptorFromRequest)
	srv.SetActionGateAuditSink(newKernelActionGateAuditSink(auditExporter))
	slog.Info("safety-kernel: action-gate pipeline wired",
		"gates", pipelineGateIDs(pipeline),
		"audit_sink_attached", auditExporter != nil,
	)
	return nil
}

// pipelineGateIDs returns a slice of gate IDs for log/observability use.
// Reads the pipeline's gates via the public Gates() accessor so the wire
// file does not poke at private state.
func pipelineGateIDs(p *actiongates.Pipeline) []string {
	gates := p.Gates()
	out := make([]string, 0, len(gates))
	for _, g := range gates {
		out = append(out, g.ID())
	}
	return out
}

// errPipelineConstructionFailed is the sentinel error returned when the
// production pipeline factory returns nil. NewPipeline filters nil gates
// but always returns a non-nil *Pipeline today; the guard exists to keep
// the call site's fail-closed contract explicit.
var errPipelineConstructionFailed = pipelineWireError("pipeline construction returned nil")

type pipelineWireError string

func (e pipelineWireError) Error() string { return string(e) }

// actionDescriptorFromRequest is the production extractor wired on the
// kernel server. It reads the gateway-supplied JSON encoding of
// ActionDescriptor from the reserved Labels key. Returns nil when the
// label is absent (caller skips the gate pipeline) or the payload is
// malformed (defense-in-depth: malformed descriptors do not bypass
// gates, but they also do not silently corrupt the input).
func actionDescriptorFromRequest(_ context.Context, req *pb.PolicyCheckRequest) *config.ActionDescriptor {
	if req == nil {
		return nil
	}
	raw, ok := req.GetLabels()[LabelActionDescriptorJSON]
	if !ok || raw == "" {
		return nil
	}
	if len(raw) > config.ActionArgsMaxSerializedBytes {
		slog.Warn("safety-kernel: action descriptor label exceeds size cap; dropping",
			"size", len(raw),
			"cap", config.ActionArgsMaxSerializedBytes,
		)
		return nil
	}
	var desc config.ActionDescriptor
	if err := json.Unmarshal([]byte(raw), &desc); err != nil {
		slog.Warn("safety-kernel: action descriptor label malformed; dropping",
			"err", err,
		)
		return nil
	}
	return &desc
}

func requestHasActionDescriptorLabel(req *pb.PolicyCheckRequest) bool {
	if req == nil {
		return false
	}
	_, ok := req.GetLabels()[LabelActionDescriptorJSON]
	return ok
}

// newKernelActionGateAuditSink returns an audit sink that records the
// SIEMEvent into the kernel's audit exporter. A nil exporter degrades
// to a slog-only sink so a misconfigured deployment still surfaces the
// gate fires in stderr; production SHOULD wire a real exporter.
func newKernelActionGateAuditSink(exporter audit.AuditSender) func(ctx context.Context, event audit.SIEMEvent) {
	if exporter == nil {
		return func(_ context.Context, event audit.SIEMEvent) {
			slog.Warn("safety-kernel: action-gate fired with no audit exporter wired",
				"gate", event.Action,
				"decision", event.Decision,
				"tenant", event.TenantID,
				"job_id", event.JobID,
			)
		}
	}
	return func(_ context.Context, event audit.SIEMEvent) {
		exporter.Send(event)
	}
}
