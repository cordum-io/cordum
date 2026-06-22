package safetykernel

import (
	"context"
	"encoding/json"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
)

// TagDeriverFunc computes authoritative risk tags from job content.
// Parameters: topic, labels (including _content.* keys), raw payload bytes.
// Returns derived tags. A nil return means "no derivation" — fall back to client tags.
type TagDeriverFunc func(topic string, labels map[string]string, payload []byte) []string

// TagDeriverRegistry maps topics to server-side risk tag derivation functions.
// When a deriver is registered for a topic, its output replaces client-supplied
// risk_tags during policy evaluation. Topics without a deriver continue to use
// client-supplied tags (backward compatible).
type TagDeriverRegistry struct {
	mu       sync.RWMutex
	derivers map[string]TagDeriverFunc
}

// NewTagDeriverRegistry creates an empty registry.
func NewTagDeriverRegistry() *TagDeriverRegistry {
	return &TagDeriverRegistry{
		derivers: make(map[string]TagDeriverFunc),
	}
}

// Register adds a tag deriver for a specific topic. Overwrites any existing
// deriver for the same topic.
func (r *TagDeriverRegistry) Register(topic string, fn TagDeriverFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.derivers[topic] = fn
}

// Derive computes authoritative risk tags for the given topic and content.
// Returns (tags, true) if a deriver is registered and produced tags.
// Returns (nil, false) if no deriver is registered for this topic.
func (r *TagDeriverRegistry) Derive(topic string, labels map[string]string, payload []byte) ([]string, bool) {
	r.mu.RLock()
	fn, ok := r.derivers[topic]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	tags := fn(topic, labels, payload)
	if tags == nil {
		return nil, false
	}
	return tags, true
}

// HasDeriver returns true if a deriver is registered for the given topic.
func (r *TagDeriverRegistry) HasDeriver(topic string) bool {
	r.mu.RLock()
	_, ok := r.derivers[topic]
	r.mu.RUnlock()
	return ok
}

// Unregister removes the tag deriver for a specific topic.
func (r *TagDeriverRegistry) Unregister(topic string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.derivers, topic)
}

// Clear removes all registered derivers.
func (r *TagDeriverRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.derivers = make(map[string]TagDeriverFunc)
}

// Swap atomically replaces the entire deriver map. Evaluations in flight see
// either the old map or the new map, never a partially built intermediate.
func (r *TagDeriverRegistry) Swap(newDerivers map[string]TagDeriverFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.derivers = newDerivers
}

// --- Built-in derivers ---

// AmountThresholdDeriver creates a tag deriver that parses an "amount" field
// from the job payload (JSON) and derives risk tags based on configurable thresholds.
// Thresholds is a sorted-ascending list of (maxExclusive, tag) pairs. The last
// entry's tag is used when amount >= all thresholds.
type AmountThreshold struct {
	MaxExclusive float64
	Tag          string
}

// NewAmountThresholdDeriver returns a TagDeriverFunc that parses the amount from
// _content.payload_json label (or raw payload) and returns risk tags based on
// the provided thresholds. baseTags are always included in the output.
func NewAmountThresholdDeriver(thresholds []AmountThreshold, baseTags []string) TagDeriverFunc {
	return func(topic string, labels map[string]string, payload []byte) []string {
		amount, ok := extractAmount(labels, payload)
		if !ok {
			slog.Warn("tag deriver: could not extract amount from payload",
				"component", "safety", "topic", topic)
			// Fail-closed: when amount can't be determined, return the highest-risk tag.
			if len(thresholds) > 0 {
				tags := make([]string, len(baseTags), len(baseTags)+1)
				copy(tags, baseTags)
				return append(tags, thresholds[len(thresholds)-1].Tag)
			}
			return nil
		}

		// Fail-closed on invalid amounts: the workflow only routes amount > 0,
		// so 0 or negative values are invalid direct submissions.
		if amount <= 0 {
			slog.Warn("tag deriver: invalid amount (<=0), fail-closed",
				"component", "safety", "topic", topic, "amount", amount)
			if len(thresholds) > 0 {
				tags := make([]string, len(baseTags), len(baseTags)+1)
				copy(tags, baseTags)
				return append(tags, thresholds[len(thresholds)-1].Tag)
			}
			return nil
		}

		var derivedTag string
		for _, t := range thresholds {
			if amount < t.MaxExclusive {
				derivedTag = t.Tag
				break
			}
		}
		if derivedTag == "" && len(thresholds) > 0 {
			derivedTag = thresholds[len(thresholds)-1].Tag
		}

		tags := make([]string, len(baseTags), len(baseTags)+1)
		copy(tags, baseTags)
		if derivedTag != "" {
			tags = append(tags, derivedTag)
		}
		return tags
	}
}

// extractAmount tries to parse an amount value from the content labels or payload.
// Priority: _content.payload_json > raw payload > _content.prompt (if JSON).
func extractAmount(labels map[string]string, payload []byte) (float64, bool) {
	// Try _content.payload_json label first (set by HTTP submit path).
	if payloadJSON, ok := labels["_content.payload_json"]; ok && payloadJSON != "" {
		if amount, ok := parseAmountFromJSON([]byte(payloadJSON)); ok {
			return amount, true
		}
	}

	// Fall back to raw payload.
	if len(payload) > 0 {
		if amount, ok := parseAmountFromJSON(payload); ok {
			return amount, true
		}
	}

	// Fall back to _content.prompt if it looks like JSON (gRPC path may
	// only have prompt, not a structured payload).
	if prompt, ok := labels["_content.prompt"]; ok && prompt != "" {
		if amount, ok := parseAmountFromJSON([]byte(prompt)); ok {
			return amount, true
		}
	}

	return 0, false
}

// parseAmountFromJSON extracts a numeric "amount" field from a JSON object.
func parseAmountFromJSON(data []byte) (float64, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return 0, false
	}
	raw, ok := obj["amount"]
	if !ok {
		return 0, false
	}
	// Reject JSON null — it unmarshals to zero which would be misleading.
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return 0, false
	}
	// Try direct number unmarshal.
	var amount float64
	if err := json.Unmarshal(raw, &amount); err == nil {
		return amount, true
	}
	// Try string-encoded number.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

// MockBankTransferDeriver returns the built-in tag deriver for demo-mock-bank
// transfer topic. Thresholds: <100 → low, 100–299 → review, >=300 → blocked.
func MockBankTransferDeriver() TagDeriverFunc {
	return NewAmountThresholdDeriver(
		[]AmountThreshold{
			{MaxExclusive: 100, Tag: "low"},
			{MaxExclusive: 300, Tag: "review"},
			// Amount >= 300 falls through to last tag: "blocked"
			{MaxExclusive: 1<<53 - 1, Tag: "blocked"}, // sentinel upper bound
		},
		[]string{"finance", "transfer"},
	)
}

// --- Generic config-driven MCP-op risk-tag deriver (task-73922f13) ---

// configMCPOpDeriverName is the risk_tag_deriver value a topic uses to opt into
// the generic, config-driven MCP-op deriver (built per-topic from mcp_op_rules,
// not a static NamedDerivers entry).
const configMCPOpDeriverName = "mcp-op"

// MCPOpTagRule lets ANY MCP integration declare op->risk-tag rules as DATA in the
// system "topics" config — no per-integration Go in core. Schema for a topic that
// names the "mcp-op" deriver:
//
//	{
//	  "name": "job.<integration>.tool",
//	  "risk_tag_deriver": "mcp-op",
//	  "default_risk_tags": ["external-callback"],
//	  "mcp_op_rules": [
//	    {"op_glob": "all_monday_api", "labels": {"mutation": "true"}, "tags": ["destructive"]},
//	    {"op_glob": "*delete*", "tags": ["destructive"]}
//	  ]
//	}
//
// A rule MATCHES a job when its OpGlob (path.Match against the resolved op/tool
// name; empty OpGlob matches any op) matches AND every entry in Labels equals the
// job's label of that key (empty Labels impose no constraint). The deriver returns
// the deduped, sorted UNION of every matching rule's Tags plus default_risk_tags —
// always non-nil, so it REPLACES client-supplied risk_tags (anti-spoof) for the
// registered topic.
type MCPOpTagRule struct {
	OpGlob string            `json:"op_glob,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	Tags   []string          `json:"tags"`
}

// mcpOpNameLabelKeys are the conventional label keys consulted (in order) for the
// MCP op/tool name. Integrations/edge populate one of these; a JSON payload
// tool/op/name field is the fallback.
var mcpOpNameLabelKeys = []string{"mcp.tool_name", "mcp.op", "tool_name", "_content.tool_name", "op"}

// resolveMCPOpName extracts the MCP op/tool name from labels (preferred) or a
// JSON payload "tool"/"op"/"name" field. Returns "" when none is present (a rule
// with a non-empty OpGlob then simply will not match).
func resolveMCPOpName(labels map[string]string, payload []byte) string {
	for _, k := range mcpOpNameLabelKeys {
		if v := strings.TrimSpace(labels[k]); v != "" {
			return v
		}
	}
	for _, src := range [][]byte{[]byte(labels["_content.payload_json"]), payload} {
		if len(src) == 0 {
			continue
		}
		if op := jsonFirstStringField(src, "tool", "op", "name"); op != "" {
			return op
		}
	}
	return ""
}

// jsonFirstStringField returns the first non-empty string value among keys in a
// JSON object, or "" (also "" on non-object input or parse error — never panics).
func jsonFirstStringField(data []byte, keys ...string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return ""
	}
	for _, k := range keys {
		raw, ok := obj[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}

// mcpOpRuleMatches reports whether rule applies to the resolved op and labels.
// An invalid OpGlob pattern fails closed (no match) rather than erroring.
func mcpOpRuleMatches(rule MCPOpTagRule, op string, labels map[string]string) bool {
	if glob := strings.TrimSpace(rule.OpGlob); glob != "" {
		ok, err := path.Match(glob, op)
		if err != nil || !ok {
			return false
		}
	}
	for k, want := range rule.Labels {
		if labels[k] != want {
			return false
		}
	}
	return true
}

// NewConfigMCPOpDeriver returns a generic TagDeriverFunc built from declarative
// op->tag rules (no per-integration Go). It resolves the MCP op/tool name, ORs
// together every matching rule's tags with defaultTags, and returns the deduped,
// sorted result. The result is ALWAYS non-nil (even when empty) so it REPLACES
// client-supplied risk_tags for the registered topic (anti-spoof). Malformed or
// empty rules contribute nothing; it never panics.
func NewConfigMCPOpDeriver(rules []MCPOpTagRule, defaultTags []string) TagDeriverFunc {
	return func(topic string, labels map[string]string, payload []byte) []string {
		op := resolveMCPOpName(labels, payload)
		set := make(map[string]struct{})
		add := func(tags []string) {
			for _, t := range tags {
				if t = strings.TrimSpace(t); t != "" {
					set[t] = struct{}{}
				}
			}
		}
		add(defaultTags)
		for _, rule := range rules {
			if mcpOpRuleMatches(rule, op, labels) {
				add(rule.Tags)
			}
		}
		out := make([]string, 0, len(set))
		for t := range set {
			out = append(out, t)
		}
		sort.Strings(out)
		return out
	}
}

// NamedDerivers maps deriver names (as declared in pack manifests via
// riskTagDeriver) to factory functions. Packs reference these by name;
// the safety kernel resolves them at boot or when packs are installed.
var NamedDerivers = map[string]TagDeriverFunc{
	"amount-threshold": MockBankTransferDeriver(),
}

// registerBuiltinTagDerivers registers built-in tag derivers as a fallback.
// Called during server initialization. Derivers loaded from the topic registry
// (via loadTagDeriversFromTopics) take precedence.
func registerBuiltinTagDerivers(registry *TagDeriverRegistry) {
	registry.Register("job.demo-mock-bank.transfer", MockBankTransferDeriver())
}

// loadTagDeriversFromTopics authoritatively replaces all registry entries with
// derivers from the current topic registrations. Builds a complete new map
// and atomically swaps it in so concurrent evaluations never see an empty or
// partially built registry. Built-in fallback derivers are included in the new
// map. Returns the number of derivers registered from the topic registry.
func loadTagDeriversFromTopics(registry *TagDeriverRegistry, topics []topicDeriverEntry) int {
	// Build the new map offline — no lock held, no concurrent visibility.
	newDerivers := make(map[string]TagDeriverFunc)
	// Include built-in fallback derivers.
	newDerivers["job.demo-mock-bank.transfer"] = MockBankTransferDeriver()

	count := 0
	for _, t := range topics {
		name := strings.TrimSpace(t.DeriverName)
		if name == "" {
			continue
		}
		var fn TagDeriverFunc
		if name == configMCPOpDeriverName {
			// Generic config-driven deriver: built per-topic from its declared
			// rules (NamedDerivers entries are static/config-less).
			fn = NewConfigMCPOpDeriver(t.MCPOpRules, t.DefaultTags)
		} else {
			named, ok := NamedDerivers[name]
			if !ok {
				slog.Warn("tag deriver: unknown named deriver in topic registry",
					"component", "safety", "topic", t.TopicName, "deriver", name)
				continue
			}
			fn = named
		}
		newDerivers[t.TopicName] = fn
		count++
		slog.Info("tag deriver: registered from topic registry",
			"component", "safety", "topic", t.TopicName, "deriver", name)
	}

	// Atomically swap the entire map — concurrent evaluations see either the
	// old complete map or the new complete map, never an empty intermediate.
	registry.Swap(newDerivers)
	return count
}

// topicDeriverEntry pairs a topic name with a deriver name for loading from
// external sources (topic registry, config service).
type topicDeriverEntry struct {
	TopicName   string
	DeriverName string
	// MCPOpRules + DefaultTags carry the generic "mcp-op" deriver's config from
	// the topic registration so the deriver is constructed per-topic at load time.
	MCPOpRules  []MCPOpTagRule
	DefaultTags []string
}

// topicRegistration mirrors the JSON structure of a topic registry entry.
// Only the fields needed for deriver loading are included.
type topicRegistration struct {
	Name           string `json:"name"`
	RiskTagDeriver string `json:"risk_tag_deriver"`
	// MCPOpRules + DefaultRiskTags configure the generic "mcp-op" deriver for
	// this topic (ignored by other derivers); supplied as data by a pack/operator.
	MCPOpRules      []MCPOpTagRule `json:"mcp_op_rules,omitempty"`
	DefaultRiskTags []string       `json:"default_risk_tags,omitempty"`
}

// loadTopicDeriverEntries reads the topic registry from the config service and
// extracts topic→deriver mappings for topics that declare a riskTagDeriver.
func loadTopicDeriverEntries(ctx context.Context, cfgSvc *configsvc.Service) ([]topicDeriverEntry, error) {
	doc, err := cfgSvc.Get(ctx, configsvc.ScopeSystem, "topics")
	if err != nil {
		if isRedisNil(err) {
			return nil, nil // no topic registry yet
		}
		return nil, err
	}
	if doc == nil || len(doc.Data) == 0 {
		return nil, nil
	}

	var entries []topicDeriverEntry
	for _, v := range doc.Data {
		raw, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var reg topicRegistration
		if err := json.Unmarshal(raw, &reg); err != nil {
			continue
		}
		if reg.Name != "" && reg.RiskTagDeriver != "" {
			entries = append(entries, topicDeriverEntry{
				TopicName:   reg.Name,
				DeriverName: reg.RiskTagDeriver,
				MCPOpRules:  reg.MCPOpRules,
				DefaultTags: reg.DefaultRiskTags,
			})
		}
	}
	return entries, nil
}

func isRedisNil(err error) bool {
	return err != nil && (err == redis.Nil || strings.Contains(err.Error(), "redis: nil"))
}
