package extraction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/model"
)

func buildDedupeKey(record model.DecisionLogRecord, snapshot inputSnapshot) (string, error) {
	verdict, err := record.Verdict.DecisionLogWireValue()
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"topic":        strings.TrimSpace(record.Topic),
		"rule_id":      strings.TrimSpace(record.RuleID),
		"verdict":      verdict,
		"capabilities": append([]string(nil), snapshot.Capabilities...),
		"input_hash":   snapshot.InputHash,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func dedupeProjected(projected []projectedDecision) ([]model.EvalEntry, int) {
	if len(projected) == 0 {
		return []model.EvalEntry{}, 0
	}

	type selectedEntry struct {
		projectedDecision
	}

	selected := make(map[string]selectedEntry, len(projected))
	for _, item := range projected {
		existing, ok := selected[item.dedupeKey]
		if !ok || olderThan(item, existing.projectedDecision) {
			selected[item.dedupeKey] = selectedEntry{projectedDecision: item}
		}
	}

	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := selected[keys[i]].projectedDecision
		right := selected[keys[j]].projectedDecision
		if left.record.Timestamp != right.record.Timestamp {
			return left.record.Timestamp < right.record.Timestamp
		}
		return left.entry.SourceRef < right.entry.SourceRef
	})

	entries := make([]model.EvalEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, selected[key].entry)
	}
	return entries, len(projected) - len(entries)
}

func olderThan(left, right projectedDecision) bool {
	if left.record.Timestamp != right.record.Timestamp {
		return left.record.Timestamp < right.record.Timestamp
	}
	return left.entry.SourceRef < right.entry.SourceRef
}

func buildEvalEntryID(dedupeKey string) string {
	if len(dedupeKey) > 16 {
		dedupeKey = dedupeKey[:16]
	}
	return "audit-" + dedupeKey
}

func buildSourceRef(record model.DecisionLogRecord) string {
	return strings.TrimSpace(record.JobID) + ":" + decisionRecordID(record)
}

func decisionRecordID(record model.DecisionLogRecord) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(record.Tenant),
		strings.TrimSpace(record.JobID),
		strconv.FormatInt(record.Timestamp, 10),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

func dedupeWarnings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, warning := range in {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		if _, ok := seen[warning]; ok {
			continue
		}
		seen[warning] = struct{}{}
		out = append(out, warning)
	}
	return out
}
