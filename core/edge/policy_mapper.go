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
		if trustedPolicyAttachmentValue(event, key, value) {
			continue
		}
		putPolicyLabel(labels, key, value, false)
	}
	for key, value := range classification.Labels {
		putPolicyLabel(labels, key, value, true)
	}

	putTrustedPolicyAttachmentLabel(labels, event)
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
	// EDGE-069 — surface classifier completeness in the policy
	// labels so audit-evidence consumers (dashboard, SIEM, governance
	// timeline) can distinguish full classifications from partial.
	// Backward-compat: legacy callers may construct ActionClassification
	// before Complete existed. Use MissingFields as authoritative
	// fail-closed evidence; otherwise recompute required fields before
	// emitting classifier.complete.
	complete, missingFields := classificationCompletenessForPolicy(classification)
	if complete {
		putPolicyLabel(labels, "classifier.complete", "true", true)
	} else {
		putPolicyLabel(labels, "classifier.complete", "false", true)
		if len(missingFields) > 0 {
			putPolicyLabel(labels, "classifier.missing_fields", strings.Join(missingFields, ","), true)
		}
	}
	return labels
}

// classificationCompletenessForPolicy preserves the exported
// ActionClassification shape while making mapper labels compatible with
// legacy manual structs. Classifier-produced MissingFields are
// fail-closed; when absent, Complete=true is trusted, and zero-value
// Complete=false is treated as unknown/legacy and recomputed from the
// required fields.
func classificationCompletenessForPolicy(c ActionClassification) (bool, []string) {
	if len(c.MissingFields) > 0 {
		missing := sortedUniqueStrings(c.MissingFields)
		if len(missing) > 0 {
			return false, missing
		}
	}
	if c.Complete {
		return true, nil
	}
	return computeClassificationCompleteness(c)
}

func putTrustedPolicyAttachmentLabel(labels map[string]string, event AgentActionEvent) {
	attachmentID := strings.TrimSpace(event.Labels[LabelPolicyAttachmentID])
	if !trustedPolicyAttachmentValue(event, LabelPolicyAttachmentID, attachmentID) {
		return
	}
	putPolicyLabel(labels, LabelPolicyAttachmentID, attachmentID, true)
}

func trustedPolicyAttachmentValue(event AgentActionEvent, key, value string) bool {
	return strings.TrimSpace(key) == LabelPolicyAttachmentID &&
		strings.TrimSpace(value) == SessionPolicyAttachmentID(event.SessionID)
}

func putPolicyLabel(labels map[string]string, key, value string, trusted bool) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	if !trusted {
		if prefix, reserved := reservedPolicyLabelPrefix(key); reserved {
			// EDGE-069 — request body cannot inject classifier-owned
			// labels. Drop AND emit the metric so operators can see
			// when a client is mis-using the namespace.
			edgeRequestLabelsStrippedTotal.WithLabelValues(prefix).Inc()
			return
		}
	}
	labels[key] = safeLabelValue(value, "unknown")
}

// reservedPolicyLabelPrefixes is the canonical list of label-key
// prefixes OWNED by the classifier. Request-body labels with any of
// these prefixes are dropped at the trust boundary in
// `mapLabelsForPolicy` so a malicious or naive client cannot poison
// the policy input by setting `path.class`, `command.class`,
// `unknown.impact`, etc. Any classifier file that emits a NEW reserved
// namespace MUST also add the prefix here. See EDGE-069 +
// docs/edge/classifier-trust-boundary.md.
var reservedPolicyLabelPrefixes = []string{
	"edge.",
	"hook.",
	"mcp.",
	"llm.",
	"runtime.",
	"agent.",
	// EDGE-069 additions — close the path./command./unknown./action.
	// gap that let a request-body label downgrade its own
	// classification when the classifier did not emit a value for
	// that key (verified RED in classifier_trust_boundary_test.go
	// pre-fix).
	"path.",
	"command.",
	"unknown.",
	"action.",
	// Policy labels select privileged evaluation scopes; request-supplied
	// values are dropped unless re-added through a server-owned path.
	"policy.",
	// EDGE-069 step 5 — classifier completeness signal MUST be
	// classifier-owned: a malicious request must not set
	// classifier.complete=true to short-circuit the fail-closed path
	// for an actually-partial classification.
	"classifier.",
}

// reservedPolicyLabelPrefix reports whether key is in a reserved
// namespace and returns the matching prefix (without trailing dot)
// so callers can label observability metrics by namespace.
func reservedPolicyLabelPrefix(key string) (string, bool) {
	for _, prefix := range reservedPolicyLabelPrefixes {
		if strings.HasPrefix(key, prefix) {
			// Strip the trailing dot so the metric label is
			// "path" / "command" / "edge" — fewer tokens for
			// alert routing and dashboard breakdowns.
			return strings.TrimSuffix(prefix, "."), true
		}
	}
	return "", false
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
