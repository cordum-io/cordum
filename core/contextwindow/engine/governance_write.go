package engine

import (
	"errors"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Named errors for shared-memory write governance. Tests assert these
// via errors.Is so callers branch deterministically on the failure
// mode rather than parsing strings.
var (
	// ErrSharedWriteMissingWriter is returned when a shared-memory
	// write whose WriteKind mutates policy/trust/directive state
	// (or whose PolicyStateMutation flag is true) arrives without
	// a non-empty WriterAgentId.
	ErrSharedWriteMissingWriter = errors.New("shared-memory write requires non-empty writer_agent_id")

	// ErrSharedWriteMissingProvenance is returned when a shared-
	// memory write that mutates policy/trust/directive state arrives
	// without provenance evidence (ProvenanceRef empty or
	// ProvenanceVerified=false). The evaluator at
	// core/governance/evaluator already denies the request before
	// reaching the service for the normal path; this check is
	// defense-in-depth for callers that bypass the gateway.
	ErrSharedWriteMissingProvenance = errors.New("shared-memory write requires verified provenance")

	// ErrSharedWriteTenantMismatch is returned when the writer's
	// TenantId is non-empty AND does not match a tenant identifier
	// derivable from MemoryId or TargetAgentId. Cross-tenant shared
	// writes are denied per the same-tenant invariant in the
	// governance evaluator.
	ErrSharedWriteTenantMismatch = errors.New("shared-memory write rejected: tenant mismatch between writer and target")
)

// validateGovernanceWrite enforces the multi-agent governance contract
// on UpdateMemory:
//
//   - Raw / chat / unspecified writes are backward-compatible — no
//     governance fields are required.
//   - Writes whose WriteKind is one of the shared-* kinds, OR whose
//     PolicyStateMutation flag is set, MUST carry a non-empty
//     WriterAgentId AND a non-empty ProvenanceRef AND
//     ProvenanceVerified=true. Anything missing → fail closed.
//   - When TenantId is set, it must match the tenant prefix on
//     MemoryId (memories are namespaced by tenant in the gateway)
//     and the tenant prefix on TargetAgentId when both are present.
//     This catches cross-tenant attacks where a child agent forges a
//     write to a memory owned by a different tenant's parent agent.
//
// Returns nil when the write is governance-compliant (or
// backward-compatible). Returns a typed sentinel for tests.
func validateGovernanceWrite(req *pb.UpdateMemoryRequest) error {
	if req == nil {
		return nil
	}
	wk := req.GetWriteKind()
	mutates := req.GetPolicyStateMutation() || isSharedWriteKind(wk)
	if !mutates {
		return nil
	}
	if strings.TrimSpace(req.GetWriterAgentId()) == "" {
		return ErrSharedWriteMissingWriter
	}
	if strings.TrimSpace(req.GetProvenanceRef()) == "" || !req.GetProvenanceVerified() {
		return ErrSharedWriteMissingProvenance
	}
	if err := checkTenantConsistency(req); err != nil {
		return err
	}
	return nil
}

// isSharedWriteKind reports whether the proto enum value designates a
// memory write that touches downstream agent policy/trust/directive
// state. Raw and chat writes are passive.
func isSharedWriteKind(k pb.MemoryWriteKind) bool {
	switch k {
	case pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_TRUST_STATE,
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_DIRECTIVE:
		return true
	default:
		return false
	}
}

// checkTenantConsistency verifies that the writer's TenantId matches
// the tenant prefix encoded in MemoryId / TargetAgentId, when those
// carry one. Memory IDs in Cordum are namespaced as
// "<tenant>/<memory-id>" by gateway convention; target_agent_id may
// follow the same shape. An empty TenantId is allowed only for
// private/non-mutating bootstrap-compatible writes; shared
// policy/trust/directive writes and PolicyStateMutation requests
// require an explicit tenant claim.
func checkTenantConsistency(req *pb.UpdateMemoryRequest) error {
	tenant := strings.TrimSpace(req.GetTenantId())
	memPrefix, memOK := tenantPrefix(req.GetMemoryId())
	targetPrefix, targetOK := tenantPrefix(req.GetTargetAgentId())
	if tenant == "" {
		// Fail closed when the caller offers tenant-scoped identifiers
		// (`t-victim/...`) but omits the tenant_id claim — otherwise a
		// shared-mutating write that bypasses gateway tenant injection
		// could target memories owned by another tenant.
		if memOK || targetOK {
			return ErrSharedWriteTenantMismatch
		}
		if isSharedWriteKind(req.GetWriteKind()) || req.GetPolicyStateMutation() {
			return ErrSharedWriteTenantMismatch
		}
		return nil
	}
	if memOK && memPrefix != tenant {
		return ErrSharedWriteTenantMismatch
	}
	if targetOK && targetPrefix != tenant {
		return ErrSharedWriteTenantMismatch
	}
	return nil
}

// tenantPrefix extracts the tenant component from a "<tenant>/<rest>"
// formatted identifier. Returns (prefix, true) when the identifier
// has the expected shape; (_, false) otherwise.
func tenantPrefix(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", false
	}
	idx := strings.IndexRune(id, '/')
	if idx <= 0 {
		return "", false
	}
	return id[:idx], true
}
