package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// labelActionDescriptorJSON is the reserved Labels-map key the gateway
// uses to propagate the JSON-encoded ActionDescriptor across the gRPC
// boundary to the safety kernel. The `_` prefix is what the gateway's
// label-clean path strips before echoing labels back to clients, so the
// key never escapes the server-side request flow. Must stay in lockstep
// with safetykernel.LabelActionDescriptorJSON.
const labelActionDescriptorJSON = "_action.descriptor_json"

// stripReservedActionDescriptorLabel returns a copy of labels with the
// gateway-reserved descriptor key removed. Defends against a client
// who places `_action.descriptor_json` directly in their HTTP body's
// Labels map to bypass the gateway's authenticated descriptor path and
// inject attacker-controlled data into the kernel-side extractor.
// Returns the original map (no allocation) when the key is absent so
// the common case stays cheap.
func stripReservedActionDescriptorLabel(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	if _, hasReserved := labels[labelActionDescriptorJSON]; !hasReserved {
		return labels
	}
	out := make(map[string]string, len(labels)-1)
	for k, v := range labels {
		if k == labelActionDescriptorJSON {
			continue
		}
		out[k] = v
	}
	return out
}

// encodeActionDescriptorLabel marshals an ActionDescriptor for transport
// in a Labels map entry. Enforces the same serialized-bytes cap the
// gateway uses for tool arg payloads so an oversized descriptor cannot
// drown the policy request. Returns ("", nil) for a nil input so the
// caller can use the empty-string sentinel as "no descriptor to send."
func encodeActionDescriptorLabel(desc *config.ActionDescriptor) (string, error) {
	if desc == nil {
		return "", nil
	}
	encoded, err := json.Marshal(desc)
	if err != nil {
		return "", fmt.Errorf("marshal action descriptor: %w", err)
	}
	if len(encoded) > config.ActionArgsMaxSerializedBytes {
		return "", fmt.Errorf("action descriptor too large: %d bytes (cap %d)", len(encoded), config.ActionArgsMaxSerializedBytes)
	}
	return string(encoded), nil
}

// stripGatewayForwardedActionDescriptor clones req and removes the reserved
// descriptor label after the gateway-primary action-gate pipeline has allowed
// the action. The gateway has full HTTP auth/backend context; the Safety Kernel
// descriptor extractor remains defense-in-depth for direct gRPC callers.
func stripGatewayForwardedActionDescriptor(req *pb.PolicyCheckRequest) *pb.PolicyCheckRequest {
	if req == nil {
		return nil
	}
	if _, ok := req.GetLabels()[labelActionDescriptorJSON]; !ok {
		return req
	}
	out := proto.Clone(req).(*pb.PolicyCheckRequest)
	labels := make(map[string]string, len(req.GetLabels()))
	for k, v := range req.GetLabels() {
		if k == labelActionDescriptorJSON {
			continue
		}
		labels[k] = v
	}
	out.Labels = labels
	return out
}

// wireActionGatePipeline installs the production action-gate pipeline
// on the gateway server. Called once during RunWithAuth after the
// server fields (edgeStore, agentIdentityStore) are populated. The
// gateway is the primary enforcement surface: handlers_policy.go fires
// the pipeline before forwarding to the safety kernel, so a wired
// pipeline here turns the previously-dead actiongates_http.go and
// handlers_policy.go gate-firing branches into the live request path.
//
// Returns no error: gate construction itself never fails (nil deps
// degrade individual gates to fail-closed/service_unavailable). The
// function is idempotent — callers that re-invoke at config-reload time
// receive a fresh pipeline.
func (s *server) wireActionGatePipeline() {
	if s == nil {
		return
	}
	redisClient := s.redisClient()
	pipeline := actiongates.BuildProductionPipeline(actiongates.ProductionPipelineOptions{
		Approvals:                               edgeStoreApprovalLookup{store: s.edgeStore},
		Identities:                              gatewayMCPIdentityResolver{store: s.agentIdentityStore},
		ChainVerifier:                           newAuditChainApprovalVerifier(redisClient, s.auditChainer),
		DestructiveToolGlobs:                    destructiveToolGlobsFromEnv(),
		DestructiveMutationArgKeys:              destructiveMutationArgKeysFromEnv(),
		DestructiveMutationFieldGlobs:           destructiveMutationFieldGlobsFromEnv(),
		FailClosedDestructiveOnTaintLookupError: failClosedDestructiveTaintFromEnv(),
	})
	// actionGatePipeline is set once during boot before any handler can
	// observe it, so the field assignment needs no lock — Go's
	// initialization-before-use guarantee covers the publish.
	s.actionGatePipeline = pipeline
	slog.Info("gateway: action-gate pipeline wired",
		"gate_count", len(pipeline.Gates()),
		"approvals_backend", "edge.RedisStore",
		"mcp_identity_backend", "store.AgentIdentityStore",
		"audit_chain_verifier_backend", "core/audit.VerifyChain",
		"audit_chain_redis_available", redisClient != nil,
		"audit_chain_chainer_available", s.auditChainer != nil,
		"audit_chain_hmac_enabled", s.auditChainer != nil && s.auditChainer.HMACEnabled(),
	)
}

// destructiveToolGlobsFromEnv lets an operator override the MCPGate's
// destructive-tool glob set via CORDUM_MCP_DESTRUCTIVE_TOOL_GLOBS (comma-
// separated path.Match patterns). Empty/unset returns nil so the gate applies
// its built-in default set ({*delete*,*remove*,*archive*}); this is the
// gateway-side override seam the content-aware session-taint deny keys on.
func destructiveToolGlobsFromEnv() []string {
	return splitCommaEnv("CORDUM_MCP_DESTRUCTIVE_TOOL_GLOBS")
}

// destructiveMutationArgKeysFromEnv lets an operator override which string args
// are scanned for GraphQL mutation documents. Empty/unset returns nil so the
// gate applies its built-in defaults.
func destructiveMutationArgKeysFromEnv() []string {
	return splitCommaEnv("CORDUM_MCP_DESTRUCTIVE_MUTATION_ARG_KEYS")
}

// destructiveMutationFieldGlobsFromEnv lets an operator override the destructive
// GraphQL mutation field globs. Empty/unset returns nil so the gate applies its
// built-in defaults (delete/remove/archive mutation fields).
func destructiveMutationFieldGlobsFromEnv() []string {
	return splitCommaEnv("CORDUM_MCP_DESTRUCTIVE_MUTATION_GLOBS")
}

func failClosedDestructiveTaintFromEnv() bool {
	raw := strings.TrimSpace(os.Getenv("CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE"))
	switch strings.ToLower(raw) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func splitCommaEnv(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// edgeStoreApprovalLookup adapts edgecore.RedisStore to the
// actiongates.ApprovalLookup contract used by mutation + provenance
// gates. A nil store is treated as a miss (false, nil) per the
// ApprovalLookup contract — surface-level nil checks happen at wire
// time so the runtime never panics on a misconfigured deploy.
type edgeStoreApprovalLookup struct {
	store edgecore.Store
}

// LookupByApprovalRef delegates to edge.Store.GetApproval so authorization
// gates bind the caller-supplied approval_ref directly and retain the store's
// tenant check. Cross-tenant refs therefore return a clean miss instead of an
// approval from another tenant. This is an evaluation-time read only; approval
// consumption remains in the Edge ClaimApproval CAS path.
func (a edgeStoreApprovalLookup) LookupByApprovalRef(ctx context.Context, tenant, approvalRef string) (*edgecore.EdgeApproval, bool, error) {
	if a.store == nil {
		return nil, false, nil
	}
	return a.store.GetApproval(ctx, tenant, approvalRef)
}

// LookupByActionHash delegates to the underlying Redis store. The cast
// to the concrete *edgecore.RedisStore is necessary because the
// edgecore.Store interface does not expose LookupByActionHash (only
// the concrete store satisfies actiongates.ApprovalLookup). When the
// concrete type assertion fails, we return a miss so the caller
// degrades to the require-human / fail-closed path rather than
// panicking on a missing method.
func (a edgeStoreApprovalLookup) LookupByActionHash(ctx context.Context, tenant, actionHash string) (*edgecore.EdgeApproval, bool, error) {
	if a.store == nil {
		return nil, false, nil
	}
	redisStore, ok := a.store.(*edgecore.RedisStore)
	if !ok {
		return nil, false, nil
	}
	return redisStore.LookupByActionHash(ctx, tenant, actionHash)
}

// gatewayMCPIdentityResolver adapts the gateway's persisted agent
// identity store into the MCP-shaped AgentIdentity the action-gate
// MCPGate consumes. Reuses mcpIdentityFromStore so revoked/suspended
// identities are mapped to nil consistently with the existing MCP
// filter path.
type gatewayMCPIdentityResolver struct {
	store *store.AgentIdentityStore
}

// ResolveMCPIdentity looks up the agent in Redis and adapts the
// result. Returns (nil, nil) for a miss so the MCPGate's nil-identity
// fail-closed path takes over. A backend error propagates so the gate
// can fail closed with Code=service_unavailable.
//
// MCP actions pass their authenticated request tenant through to the
// AgentIdentityStore lookup so the store's tenant check guards against
// cross-tenant agent-ID reuse. The store's empty-tenant internal/system
// bypass is not used for normal MCP action evaluation.
func (r gatewayMCPIdentityResolver) ResolveMCPIdentity(ctx context.Context, tenant string, agentID string) (*mcp.AgentIdentity, error) {
	if r.store == nil {
		return nil, nil
	}
	identity, err := r.store.Get(ctx, tenant, agentID)
	if err != nil {
		return nil, err
	}
	return mcpIdentityFromStore(identity), nil
}
