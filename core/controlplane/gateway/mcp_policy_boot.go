package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/mcp"
	"github.com/redis/go-redis/v9"
)

// mcpPolicyServerName is the logical MCP server identifier the gateway
// stamps on every ActionDescriptor.Server emitted by the policy gate.
// Kept stable across the consume + mint paths so the EDGE-103
// approval-hold flow's CanonicalActionHash binds to the same key
// regardless of whether the call came in on the inbound bridge or the
// `_approval_ref` resume path. Tests + docs reference this verbatim;
// changing it is a contract break.
const mcpPolicyServerName = "cordum.builtin"

// edgeStoreEventEmitter adapts a production *edgecore.RedisStore (or any
// edge.Store implementation) to the narrow mcp.EventEmitter contract the
// EDGE-102 policy gate consumes. The adapter drops the AppendEvent
// return value (the assigned seq is not needed by the policy layer) and
// surfaces just the error, fail-closing on a nil store so a misconfigured
// gateway boot never silently degrades to a no-op emitter.
type edgeStoreEventEmitter struct {
	store edge.Store
}

func (e edgeStoreEventEmitter) Emit(ctx context.Context, event *edge.AgentActionEvent) error {
	if e.store == nil {
		return errors.New("edge store unavailable")
	}
	if event == nil {
		return errors.New("emit nil event")
	}
	_, err := e.store.AppendEvent(ctx, *event)
	return err
}

// productionArtifactStore adapts the gateway's artifacts.Store (today's
// production backend: Redis with content-addressed pointers) to the
// mcp.ArtifactStore contract the policy gate consumes for oversized
// redacted payloads. The adapter computes a SHA-256 over the payload
// the gate handed in so the returned ArtifactPointer carries the same
// canonical digest the dashboard's evidence-export bundler reads when
// dereferencing the artifact later.
//
// Failure modes propagate verbatim — the gate's materializeRedactedPayload
// helper fails closed on a non-nil error, so a transient artifact-store
// outage produces an `mcp.tool.failed` event with reason=service_unavailable
// instead of silently inlining the oversized payload into a Redis event.
type productionArtifactStore struct {
	store artifacts.Store
}

func (p productionArtifactStore) Put(ctx context.Context, req mcp.ArtifactPutRequest) (*edge.ArtifactPointer, error) {
	if p.store == nil {
		return nil, errors.New("artifact store unavailable")
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	uri, err := p.store.Put(ctx, req.Payload, artifacts.Metadata{
		ContentType: contentType,
		SizeBytes:   int64(len(req.Payload)),
		Retention:   artifacts.RetentionStandard,
		Labels: map[string]string{
			"artifact_type": string(req.Type),
			"tenant":        req.TenantID,
			"session_id":    req.SessionID,
			"execution_id":  req.ExecutionID,
			"event_id":      req.EventID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("artifact store put: %w", err)
	}
	sum := sha256.Sum256(req.Payload)
	return &edge.ArtifactPointer{
		ArtifactType:   req.Type,
		URI:            uri,
		SHA256:         hex.EncodeToString(sum[:]),
		TenantID:       req.TenantID,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		RetentionClass: edge.RetentionClassStandard,
		RedactionLevel: edge.RedactionLevelStandard,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

// attachMCPPolicyDepsNamed is attachMCPPolicyDeps with an explicit gate
// server-name. serverName=="" falls back to mcpPolicyServerName ("cordum.builtin");
// when the gateway fronts a registered upstream the caller passes the upstream's
// name (e.g. "cordum.monday") so the action-gate, approval-hold, and audit
// attribute every decision to that upstream.
func (s *server) attachMCPPolicyDepsNamed(mcpServer *mcp.MCPServer, gate *gatewayApprovalGate, serverName string) (*mcp.MCPServer, string) {
	if s == nil || mcpServer == nil {
		return mcpServer, "server or mcp_server nil"
	}
	if s.actionGatePipeline == nil {
		return mcpServer, "actionGatePipeline nil"
	}
	if s.edgeStore == nil {
		return mcpServer, "edgeStore nil"
	}
	if s.artifactStore == nil {
		return mcpServer, "artifactStore nil"
	}
	name := serverName
	if name == "" {
		name = mcpPolicyServerName
	}
	emitter := edgeStoreEventEmitter{store: s.edgeStore}
	artifactStore := productionArtifactStore{store: s.artifactStore}
	var redisClient redis.Cmdable
	if s.jobStore != nil {
		redisClient = s.jobStore.Client()
	}
	policyDeps := BuildMCPPolicyDeps(s.actionGatePipeline, gate, emitter, artifactStore, redisClient)
	mcpServer = mcpServer.WithPolicyGate(name, policyDeps)

	mcpServer = mcpServer.WithApprovalHold(mcp.ApprovalHoldDeps{
		Store:          s.edgeStore,
		PolicySnapshot: s.mcpPolicySnapshotFunc(),
		ServerName:     name,
	})
	return mcpServer, ""
}

// mcpPolicySnapshotFunc returns a closure the WithApprovalHold consume
// path calls to compute the current PolicySnapshot used in the
// ApprovalClaimRequest. The snapshot is the bundle-updated-at timestamp
// returned by loadPolicyBundles — it changes monotonically when the
// active policy bundle rotates, so a consume against a stale snapshot
// surfaces as `policy_mismatch` from the Edge approval store. Empty
// string on a config-load error fails closed at the validation layer
// (ApprovalClaimRequest rejects empty snapshots).
func (s *server) mcpPolicySnapshotFunc() func(ctx context.Context) string {
	return func(ctx context.Context) string {
		_, snapshot, err := s.loadPolicyBundles(ctx)
		if err != nil {
			return ""
		}
		return snapshot
	}
}
