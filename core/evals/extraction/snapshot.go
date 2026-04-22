package extraction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	maxSnapshotBytes = 8 * 1024
	maxMetadataKeys  = 16
	maxMetadataValue = 256
)

type inputSnapshot struct {
	Tenant       string            `json:"tenant"`
	Topic        string            `json:"topic"`
	Capabilities []string          `json:"capabilities,omitempty"`
	RiskTags     []string          `json:"risk_tags,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	InputHash    string            `json:"input_hash"`
}

func buildInputSnapshot(req any) (inputSnapshot, json.RawMessage, error) {
	jobReq, ok := req.(*pb.JobRequest)
	if !ok || jobReq == nil {
		return inputSnapshot{}, nil, fmt.Errorf("job request is nil")
	}

	snapshot := inputSnapshot{
		Tenant:       model.ExtractTenant(jobReq),
		Topic:        strings.TrimSpace(jobReq.GetTopic()),
		Capabilities: extractCapabilities(jobReq),
		RiskTags:     extractRiskTags(jobReq),
		Metadata:     extractMetadata(jobReq),
		AgentID:      extractAgentID(jobReq),
	}
	hash, err := computeInputHash(snapshot)
	if err != nil {
		return inputSnapshot{}, nil, err
	}
	snapshot.InputHash = hash

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return inputSnapshot{}, nil, fmt.Errorf("marshal input snapshot: %w", err)
	}
	if len(raw) > maxSnapshotBytes && len(snapshot.Metadata) > 0 {
		snapshot.Metadata = nil
		hash, err = computeInputHash(snapshot)
		if err != nil {
			return inputSnapshot{}, nil, err
		}
		snapshot.InputHash = hash
		raw, err = json.Marshal(snapshot)
		if err != nil {
			return inputSnapshot{}, nil, fmt.Errorf("marshal input snapshot: %w", err)
		}
	}
	if len(raw) > maxSnapshotBytes {
		return inputSnapshot{}, nil, fmt.Errorf("input snapshot exceeds %d bytes", maxSnapshotBytes)
	}
	return snapshot, json.RawMessage(raw), nil
}

func extractCapabilities(req *pb.JobRequest) []string {
	if req == nil || req.GetMeta() == nil {
		return nil
	}
	values := make([]string, 0, 1)
	if capability := strings.TrimSpace(req.GetMeta().GetCapability()); capability != "" {
		values = append(values, capability)
	}
	return uniqueSortedStrings(values)
}

func extractRiskTags(req *pb.JobRequest) []string {
	if req == nil || req.GetMeta() == nil {
		return nil
	}
	return uniqueSortedStrings(req.GetMeta().GetRiskTags())
}

func extractMetadata(req *pb.JobRequest) map[string]string {
	if req == nil {
		return nil
	}
	collected := make(map[string]string)
	addMetadata(collected, req.GetLabels())
	if meta := req.GetMeta(); meta != nil {
		addMetadata(collected, meta.GetLabels())
	}
	if len(collected) == 0 {
		return nil
	}

	keys := make([]string, 0, len(collected))
	for key := range collected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > maxMetadataKeys {
		keys = keys[:maxMetadataKeys]
	}

	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = collected[key]
	}
	return out
}

func addMetadata(dst map[string]string, values map[string]string) {
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if strings.EqualFold(key, "agent_id") || strings.HasPrefix(strings.ToLower(key), "_content.") {
			continue
		}
		if len(value) > maxMetadataValue {
			value = value[:maxMetadataValue]
		}
		dst[key] = value
	}
}

func extractAgentID(req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	if labels := req.GetLabels(); labels != nil {
		if agentID := strings.TrimSpace(labels["agent_id"]); agentID != "" {
			return agentID
		}
	}
	if meta := req.GetMeta(); meta != nil {
		if labels := meta.GetLabels(); labels != nil {
			return strings.TrimSpace(labels["agent_id"])
		}
	}
	return ""
}

func computeInputHash(snapshot inputSnapshot) (string, error) {
	payload := struct {
		Tenant       string            `json:"tenant"`
		Topic        string            `json:"topic"`
		Capabilities []string          `json:"capabilities,omitempty"`
		RiskTags     []string          `json:"risk_tags,omitempty"`
		Metadata     map[string]string `json:"metadata,omitempty"`
		AgentID      string            `json:"agent_id,omitempty"`
	}{
		Tenant:       snapshot.Tenant,
		Topic:        snapshot.Topic,
		Capabilities: snapshot.Capabilities,
		RiskTags:     snapshot.RiskTags,
		Metadata:     snapshot.Metadata,
		AgentID:      snapshot.AgentID,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal input hash payload: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func buildDecisionMetadata(record model.DecisionLogRecord) map[string]string {
	out := make(map[string]string, 2)
	if record.Timestamp > 0 {
		out["decision_ts"] = timeRFC3339(record.Timestamp)
	}
	if version := strings.TrimSpace(record.PolicyVersion); version != "" {
		out["policy_version"] = version
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func timeRFC3339(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.UnixMilli(ts).UTC().Format(time.RFC3339Nano)
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
