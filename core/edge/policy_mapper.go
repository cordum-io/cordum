package edge

import (
	"fmt"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// PolicyMappingOptions carries trusted caller context that is not derivable
// from an AgentActionEvent.
type PolicyMappingOptions struct {
	ActorID   string
	ActorType pb.ActorType
}

// MapEventToPolicyCheckRequest maps a classified Edge action to the existing
// Safety Kernel PolicyCheckRequest wire shape. Edge uses the job-prefixed
// EdgePolicyTopic because the current Safety Kernel accepts job.* topics; Edge
// dimensions are carried as labels and metadata rather than new CAP fields.
// The mapper trusts server/event metadata and classifier output, not client
// risk tags or reserved labels.
func MapEventToPolicyCheckRequest(event AgentActionEvent, classification ActionClassification, opts PolicyMappingOptions) (*pb.PolicyCheckRequest, error) {
	tenantID := strings.TrimSpace(event.TenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	principalID := strings.TrimSpace(event.PrincipalID)
	if principalID == "" {
		return nil, fmt.Errorf("principal_id is required")
	}
	if strings.TrimSpace(event.SessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(event.ExecutionID) == "" {
		return nil, fmt.Errorf("execution_id is required")
	}
	if strings.TrimSpace(event.EventID) == "" {
		return nil, fmt.Errorf("event_id is required")
	}
	if strings.TrimSpace(classification.ActionName) == "" {
		return nil, fmt.Errorf("action_name is required")
	}
	if strings.TrimSpace(classification.Capability) == "" {
		return nil, fmt.Errorf("capability is required")
	}

	labels := mapLabelsForPolicy(event, classification)
	actorID := strings.TrimSpace(opts.ActorID)
	if actorID == "" {
		actorID = principalID
	}
	actorType := opts.ActorType
	if actorType == pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		actorType = pb.ActorType_ACTOR_TYPE_SERVICE
	}
	riskTags := sortedUniqueStrings(classification.RiskTags)

	return &pb.PolicyCheckRequest{
		Topic:            EdgePolicyTopic,
		Tenant:           tenantID,
		PrincipalId:      principalID,
		Labels:           cloneStringMap(labels),
		Meta:             &pb.JobMetadata{TenantId: tenantID, ActorId: actorID, ActorType: actorType, Capability: strings.TrimSpace(classification.Capability), RiskTags: riskTags, Labels: cloneStringMap(labels)},
		InputContent:     cloneBytes(classification.InputContent),
		InputContentType: strings.TrimSpace(classification.InputContentType),
		InputSizeBytes:   classification.InputSizeBytes,
	}, nil
}

func mapLabelsForPolicy(event AgentActionEvent, classification ActionClassification) map[string]string {
	labels := make(map[string]string)
	for key, value := range event.Labels {
		putPolicyLabel(labels, key, value, false)
	}
	for key, value := range classification.Labels {
		putPolicyLabel(labels, key, value, true)
	}

	putPolicyLabel(labels, "edge.session_id", event.SessionID, true)
	putPolicyLabel(labels, "edge.execution_id", event.ExecutionID, true)
	putPolicyLabel(labels, "edge.event_id", event.EventID, true)
	putPolicyLabel(labels, "edge.layer", string(event.Layer), true)
	putPolicyLabel(labels, "edge.kind", string(event.Kind), true)
	putPolicyLabel(labels, "edge.action_name", classification.ActionName, true)
	if event.AgentProduct != "" {
		putPolicyLabel(labels, "agent.product", event.AgentProduct, true)
	}
	if event.Layer == LayerHook {
		putPolicyLabel(labels, "hook.event", string(event.Kind), true)
		putPolicyLabel(labels, "hook.tool_name", event.ToolName, true)
	}
	return labels
}

func putPolicyLabel(labels map[string]string, key, value string, trusted bool) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	if !trusted && isReservedPolicyLabel(key) {
		return
	}
	labels[key] = safeLabelValue(value, "unknown")
}

func isReservedPolicyLabel(key string) bool {
	for _, prefix := range []string{"edge.", "hook.", "mcp.", "llm.", "runtime.", "agent."} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
