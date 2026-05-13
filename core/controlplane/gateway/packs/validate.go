package packs

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/validation"
	"github.com/cordum/cordum/core/infra/buildinfo"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	wf "github.com/cordum/cordum/core/workflow"
	"gopkg.in/yaml.v3"
)

// LoadPackBundleFromReader extracts a tar.gz pack bundle and returns the root directory.
func LoadPackBundleFromReader(src io.Reader) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "cordum-pack-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp dir: %w", err)
	}
	if err := ExtractTarGzReader(src, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", func() {}, fmt.Errorf("extract tar.gz: %w", err)
	}
	root, err := FindPackRoot(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", func() {}, fmt.Errorf("find pack root: %w", err)
	}
	return root, func() { _ = os.RemoveAll(tmpDir) }, nil
}

// LoadPackManifest reads and parses pack.yaml from the given directory.
func LoadPackManifest(dir string) (*PackManifest, error) {
	paths := []string{
		filepath.Join(dir, "pack.yaml"),
		filepath.Join(dir, "pack.yml"),
	}
	var data []byte
	var err error
	for _, path := range paths {
		// #nosec G304 -- pack manifest is read from the extracted bundle directory.
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("pack.yaml not found: %w", err)
	}
	var manifest PackManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse pack.yaml: %w", err)
	}
	return &manifest, nil
}

// PackAliasPattern bounds the alias namespace: leading lower-case letter,
// followed by lower-case alnum / hyphen / underscore, up to 31 chars total.
// Tight on purpose — prevents namespace squatting by forcing distinct,
// auditable identifiers per alias declaration.
var PackAliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,30}$`)

// MaxPackAliases caps the number of alias entries a single pack may
// declare. Eight is enough for legitimate "this pack owns sibling
// namespaces" cases without enabling broad namespace fan-out.
const MaxPackAliases = 8

// ValidatePackAliases enforces the alias regex + cap. Returns nil when
// the slice is empty (back-compat for packs that omit metadata.aliases).
func ValidatePackAliases(aliases []string) error {
	if len(aliases) == 0 {
		return nil
	}
	if len(aliases) > MaxPackAliases {
		return fmt.Errorf("metadata.aliases: at most %d entries allowed, got %d", MaxPackAliases, len(aliases))
	}
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return errors.New("metadata.aliases: empty alias not allowed")
		}
		if !PackAliasPattern.MatchString(alias) {
			return fmt.Errorf("metadata.aliases: %q must match %s", alias, PackAliasPattern.String())
		}
		if _, dup := seen[alias]; dup {
			return fmt.Errorf("metadata.aliases: duplicate entry %q", alias)
		}
		seen[alias] = struct{}{}
	}
	return nil
}

// ValidateAliasOwnership rejects namespace hijacks before a pack is installed.
// Alias syntax alone is not enough: an alias becomes authority over
// job.<alias>.* topics, so it must not collide with an installed pack id,
// another pack's alias, or topics already recorded under that namespace.
// The current pack id is ignored to allow idempotent upgrades/reinstalls.
func ValidateAliasOwnership(packID string, aliases []string, topics []PackTopic, installed map[string]PackRecord) error {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return errors.New("metadata.id required")
	}
	candidateNamespaces := map[string]struct{}{packID: {}}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if alias == packID {
			return fmt.Errorf("metadata.aliases: %q duplicates metadata.id", alias)
		}
		candidateNamespaces[alias] = struct{}{}
	}

	for namespace, owner := range installedNamespaceOwners(installed, packID) {
		if _, requested := candidateNamespaces[namespace]; requested {
			return fmt.Errorf("metadata.aliases: namespace %q is already owned by installed pack %q", namespace, owner)
		}
	}

	prefixes := PackTopicPrefixes(packID, aliases)
	candidateTopics := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		name := strings.TrimSpace(topic.Name)
		if name != "" {
			candidateTopics[name] = struct{}{}
		}
	}
	for key, record := range installed {
		owner := packRecordOwnerID(key, record)
		if owner == "" || owner == packID {
			continue
		}
		for _, topic := range record.Manifest.Topics {
			name := strings.TrimSpace(topic.Name)
			if name == "" {
				continue
			}
			if _, exact := candidateTopics[name]; exact || HasAnyPrefix(name, prefixes) {
				return fmt.Errorf("topic %q is already owned by installed pack %q", name, owner)
			}
		}
	}
	return nil
}

func installedNamespaceOwners(installed map[string]PackRecord, currentPackID string) map[string]string {
	owners := map[string]string{}
	for key, record := range installed {
		owner := packRecordOwnerID(key, record)
		if owner == "" || owner == currentPackID {
			continue
		}
		addNamespaceOwner(owners, strings.TrimSpace(key), owner)
		addNamespaceOwner(owners, strings.TrimSpace(record.ID), owner)
		addNamespaceOwner(owners, strings.TrimSpace(record.Manifest.Metadata.ID), owner)
		for _, alias := range record.Manifest.Metadata.Aliases {
			addNamespaceOwner(owners, strings.TrimSpace(alias), owner)
		}
	}
	return owners
}

func packRecordOwnerID(key string, record PackRecord) string {
	if id := strings.TrimSpace(record.ID); id != "" {
		return id
	}
	if id := strings.TrimSpace(record.Manifest.Metadata.ID); id != "" {
		return id
	}
	return strings.TrimSpace(key)
}

func addNamespaceOwner(owners map[string]string, namespace, owner string) {
	if namespace == "" || owner == "" {
		return
	}
	if _, exists := owners[namespace]; !exists {
		owners[namespace] = owner
	}
}

// PackTopicPrefixes returns the set of "job.<x>." prefixes a pack may
// use for its topics. Always includes "job.<id>."; each declared alias
// adds "job.<alias>.". The slice is small (<=9 entries given
// MaxPackAliases) so linear-scan membership is fine.
func PackTopicPrefixes(id string, aliases []string) []string {
	prefixes := make([]string, 0, 1+len(aliases))
	prefixes = append(prefixes, "job."+id+".")
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		prefixes = append(prefixes, "job."+alias+".")
	}
	return prefixes
}

// HasAnyPrefix reports whether s starts with any prefix in the slice.
func HasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// FormatPrefixList formats a slice of "job.<x>." prefixes for error
// messages. Single → "job.<x>.*"; multiple → "job.<a>.* or job.<b>.* ..."
// so operators can see exactly which namespaces are allowed.
func FormatPrefixList(prefixes []string) string {
	parts := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		parts[i] = prefix + "*"
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " or ")
}

// ValidatePackManifest checks that the manifest has all required fields.
func ValidatePackManifest(manifest *PackManifest) error {
	if manifest == nil {
		return errors.New("pack manifest required")
	}
	id := strings.TrimSpace(manifest.Metadata.ID)
	if id == "" {
		return errors.New("metadata.id required")
	}
	idPattern := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !idPattern.MatchString(id) {
		return fmt.Errorf("metadata.id must match %s", idPattern.String())
	}
	if err := ValidatePackAliases(manifest.Metadata.Aliases); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Metadata.Version) == "" {
		return errors.New("metadata.version required")
	}
	topicPrefixes := PackTopicPrefixes(id, manifest.Metadata.Aliases)
	for _, topic := range manifest.Topics {
		if topic.Name == "" {
			return errors.New("topic name required")
		}
		if !HasAnyPrefix(topic.Name, topicPrefixes) {
			return fmt.Errorf("topic %q must be namespaced under %s", topic.Name, FormatPrefixList(topicPrefixes))
		}
	}
	schemaIDs := make(map[string]struct{}, len(manifest.Resources.Schemas))
	for _, res := range manifest.Resources.Schemas {
		if res.ID == "" || res.Path == "" {
			return errors.New("schema id and path required")
		}
		if !strings.HasPrefix(res.ID, id+"/") {
			return fmt.Errorf("schema id %q must be namespaced under %s/", res.ID, id)
		}
		schemaIDs[res.ID] = struct{}{}
	}
	for _, topic := range manifest.Topics {
		for _, schemaID := range []string{
			strings.TrimSpace(topic.InputSchemaID),
			strings.TrimSpace(topic.OutputSchemaID),
		} {
			if schemaID == "" {
				continue
			}
			if _, ok := schemaIDs[schemaID]; !ok {
				return fmt.Errorf("topic %s references unknown schema %s", topic.Name, schemaID)
			}
		}
	}
	for _, res := range manifest.Resources.Workflows {
		if res.ID == "" || res.Path == "" {
			return errors.New("workflow id and path required")
		}
		if !strings.HasPrefix(res.ID, id+".") {
			return fmt.Errorf("workflow id %q must be namespaced under %s", res.ID, id)
		}
	}
	return nil
}

// EnsureProtocolCompatible verifies the pack's protocol version matches.
func EnsureProtocolCompatible(manifest *PackManifest) error {
	if manifest.Compatibility.ProtocolVersion == 0 {
		return errors.New("compatibility.protocolVersion required")
	}
	if manifest.Compatibility.ProtocolVersion != capsdk.DefaultProtocolVersion {
		return fmt.Errorf("pack protocol version %d is not compatible with this server (requires version %d); rebuild your pack with a compatible capsdk version", manifest.Compatibility.ProtocolVersion, capsdk.DefaultProtocolVersion)
	}
	return nil
}

// EnsureCoreVersionCompatible checks the minimum core version requirement.
func EnsureCoreVersionCompatible(minCoreVersion string) error {
	minCoreVersion = strings.TrimSpace(minCoreVersion)
	if minCoreVersion == "" {
		return nil
	}
	minParsed, ok := ParseSemver(minCoreVersion)
	if !ok {
		return fmt.Errorf("invalid minCoreVersion %q", minCoreVersion)
	}
	coreParsed, ok := ParseSemver(buildinfo.Version)
	if !ok {
		// Allow installs on dev/unknown builds; use --force to bypass explicitly.
		return nil
	}
	if CompareSemver(coreParsed, minParsed) < 0 {
		return fmt.Errorf("core version %s does not satisfy minCoreVersion %s", buildinfo.Version, minCoreVersion)
	}
	return nil
}

// ParseSemver parses a semver-like version string into [major, minor, patch].
func ParseSemver(raw string) ([3]int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return [3]int{}, false
	}
	raw = strings.TrimPrefix(raw, "v")
	raw = strings.SplitN(raw, "-", 2)[0]
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			out[i] = 0
			continue
		}
		if parts[i] == "" {
			return [3]int{}, false
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// CompareSemver compares two [major, minor, patch] tuples.
func CompareSemver(left, right [3]int) int {
	for i := 0; i < 3; i++ {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

// ShouldSkipConfigOverlay returns true if the overlay should be skipped.
func ShouldSkipConfigOverlay(inactive bool, overlay PackConfigOverlay) bool {
	if !inactive {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(overlay.Key), "pools")
}

// HasPoolOverlay returns true if any applied overlay targets the "pools" config key.
func HasPoolOverlay(overlays []PackAppliedConfigOverlay) bool {
	for _, overlay := range overlays {
		if strings.EqualFold(overlay.Key, "pools") {
			return true
		}
	}
	return false
}

// ValidateConfigPatch dispatches validation based on the config key.
//
// packAliases is the optional list of namespace identifiers a pack owns
// in addition to packID; pass nil for back-compat with callers that
// don't yet thread aliases.
func ValidateConfigPatch(key string, patch map[string]any, packID string, packAliases []string, current any) error {
	switch strings.ToLower(key) {
	case "pools":
		return ValidatePoolsPatch(patch, packID, packAliases, current)
	case "timeouts":
		return ValidateTimeoutsPatch(patch, packID, packAliases)
	default:
		return fmt.Errorf("unsupported config overlay key %q", key)
	}
}

// ValidatePoolsPatch validates a pools config overlay. packAliases is
// the optional list of additional namespace identifiers a pack owns;
// topics under `job.<alias>.*` are accepted when listed there.
func ValidatePoolsPatch(patch map[string]any, packID string, packAliases []string, current any) error {
	topicPrefixes := PackTopicPrefixes(packID, packAliases)
	rawTopics := NormalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("pools.topics must be a map")
		}
		for topic := range topics {
			if !HasAnyPrefix(topic, topicPrefixes) {
				return fmt.Errorf("pools topic %q must be namespaced under %s", topic, FormatPrefixList(topicPrefixes))
			}
		}
	}
	rawPools := NormalizeJSON(patch["pools"])
	if rawPools != nil {
		pools, ok := rawPools.(map[string]any)
		if !ok {
			return errors.New("pools.pools must be a map")
		}
		existingPools := ExtractPools(current)
		for poolName := range pools {
			if _, ok := existingPools[poolName]; ok {
				continue
			}
			if !strings.HasPrefix(poolName, packID) {
				return fmt.Errorf("pool %q must be prefixed with %s", poolName, packID)
			}
		}
	}
	for key := range patch {
		if key != "topics" && key != "pools" {
			return fmt.Errorf("unsupported pools overlay key %q", key)
		}
	}
	return nil
}

// ExtractPools returns the set of existing pool names from config.
func ExtractPools(current any) map[string]struct{} {
	out := map[string]struct{}{}
	currentMap, ok := NormalizeJSON(current).(map[string]any)
	if !ok || currentMap == nil {
		return out
	}
	rawPools := NormalizeJSON(currentMap["pools"])
	pools, ok := rawPools.(map[string]any)
	if !ok || pools == nil {
		return out
	}
	for name := range pools {
		out[name] = struct{}{}
	}
	return out
}

// ValidateTimeoutsPatch validates a timeouts config overlay. packAliases
// is the optional list of additional namespace identifiers; pass nil for
// back-compat with callers that don't yet thread aliases.
func ValidateTimeoutsPatch(patch map[string]any, packID string, packAliases []string) error {
	if patch == nil {
		return nil
	}
	topicPrefixes := PackTopicPrefixes(packID, packAliases)
	rawTopics := NormalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("timeouts.topics must be a map")
		}
		for topic := range topics {
			if !HasAnyPrefix(topic, topicPrefixes) {
				return fmt.Errorf("timeouts topic %q must be namespaced under %s", topic, FormatPrefixList(topicPrefixes))
			}
		}
	}
	rawWorkflows := NormalizeJSON(patch["workflows"])
	if rawWorkflows != nil {
		workflows, ok := rawWorkflows.(map[string]any)
		if !ok {
			return errors.New("timeouts.workflows must be a map")
		}
		for wf := range workflows {
			if !strings.HasPrefix(wf, packID+".") {
				return fmt.Errorf("timeout workflow %q must be namespaced under %s", wf, packID)
			}
		}
	}
	for key := range patch {
		if key != "topics" && key != "workflows" {
			return fmt.Errorf("unsupported timeouts overlay key %q", key)
		}
	}
	return nil
}

// LoadSchemaFile reads and hashes a JSON/YAML schema file.
func LoadSchemaFile(dir, relPath string) (map[string]any, string, error) {
	path, err := SafeJoin(dir, relPath)
	if err != nil {
		return nil, "", fmt.Errorf("load schema file %s: %w", relPath, err)
	}
	payload, err := LoadDataFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("load schema file %s: %w", relPath, err)
	}
	schemaMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("schema file must be an object")
	}
	digest, err := HashValue(schemaMap)
	if err != nil {
		return nil, "", fmt.Errorf("hash schema file %s: %w", relPath, err)
	}
	return schemaMap, digest, nil
}

func validateWorkflowStepMap(steps map[string]any) error {
	return validation.WorkflowStepMap(steps)
}

// LoadWorkflowFile reads, validates, and hashes a workflow file.
func LoadWorkflowFile(dir, relPath, id string) (map[string]any, string, error) {
	path, err := SafeJoin(dir, relPath)
	if err != nil {
		return nil, "", fmt.Errorf("load workflow file %s: %w", relPath, err)
	}
	payload, err := LoadDataFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("load workflow file %s: %w", relPath, err)
	}
	workflowMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("workflow file must be an object")
	}
	if id != "" {
		workflowMap["id"] = id
	}
	if rawSteps, ok := workflowMap["steps"]; ok {
		steps, ok := rawSteps.(map[string]any)
		if !ok {
			return nil, "", errors.New("workflow steps must be an object")
		}
		if err := validateWorkflowStepMap(steps); err != nil {
			return nil, "", fmt.Errorf("validate workflow steps in %s: %w", relPath, err)
		}
	}
	normalized := NormalizeWorkflowMap(workflowMap)
	digest, err := HashValue(normalized)
	if err != nil {
		return nil, "", fmt.Errorf("hash workflow file %s: %w", relPath, err)
	}
	return workflowMap, digest, nil
}

// NormalizeWorkflowMap strips volatile fields (timestamps) from a workflow map.
func NormalizeWorkflowMap(workflow map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range workflow {
		switch k {
		case "created_at", "updated_at":
			continue
		default:
			out[k] = v
		}
	}
	return out
}

// HashWorkflow returns the SHA-256 digest of a normalized workflow map.
func HashWorkflow(workflow map[string]any) (string, error) {
	return HashValue(NormalizeWorkflowMap(workflow))
}

// WorkflowToMap converts a Workflow struct to a generic map.
func WorkflowToMap(workflow *wf.Workflow) map[string]any {
	if workflow == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(workflow)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// LoadDataFile reads a JSON or YAML file and returns the normalized content.
func LoadDataFile(path string) (any, error) {
	// #nosec G304 -- path is validated by SafeJoin at call sites.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read data file: %w", err)
	}
	var payload any
	if json.Unmarshal(data, &payload) == nil {
		return NormalizeJSON(payload), nil
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse data file as yaml: %w", err)
	}
	return NormalizeJSON(payload), nil
}

// LoadPatchFile reads a config overlay patch file.
func LoadPatchFile(dir, relPath string) (any, error) {
	path, err := SafeJoin(dir, relPath)
	if err != nil {
		return nil, fmt.Errorf("load patch file %s: %w", relPath, err)
	}
	payload, err := LoadDataFile(path)
	if err != nil {
		return nil, fmt.Errorf("load patch file %s: %w", relPath, err)
	}
	return payload, nil
}

// NormalizeJSON recursively converts map[any]any (from YAML) to map[string]any.
func NormalizeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			out[k] = NormalizeJSON(child)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, child := range v {
			key := fmt.Sprint(k)
			out[key] = NormalizeJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = NormalizeJSON(child)
		}
		return out
	default:
		return v
	}
}

// DeepCopy performs a JSON round-trip deep copy.
func DeepCopy(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}

// MergePatch applies a JSON Merge Patch to the current value.
func MergePatch(current any, patch map[string]any) any {
	if patch == nil {
		return current
	}
	currentMap, _ := NormalizeJSON(current).(map[string]any)
	if currentMap == nil {
		currentMap = map[string]any{}
	}
	for key, value := range patch {
		switch v := value.(type) {
		case nil:
			delete(currentMap, key)
		case map[string]any:
			currentMap[key] = MergePatch(currentMap[key], v)
		default:
			currentMap[key] = v
		}
	}
	return currentMap
}

// BuildDeletePatch creates a patch that deletes all keys from the original.
func BuildDeletePatch(patch map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range patch {
		switch v := value.(type) {
		case map[string]any:
			out[key] = BuildDeletePatch(v)
		default:
			out[key] = nil
		}
	}
	return out
}

// HashValue returns the SHA-256 hex digest of the canonical JSON encoding.
func HashValue(value any) (string, error) {
	encoded, err := CanonicalJSON(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical json: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalJSON encodes a value to deterministic JSON with sorted keys.
func CanonicalJSON(value any) ([]byte, error) {
	buf := &strings.Builder{}
	if err := AppendCanonical(buf, value); err != nil {
		return nil, fmt.Errorf("build canonical json: %w", err)
	}
	return []byte(buf.String()), nil
}

// AppendCanonical writes a value to the builder in canonical JSON format.
func AppendCanonical(buf *strings.Builder, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case map[string]any:
		return appendCanonicalMap(buf, v)
	case []any:
		return appendCanonicalSlice(buf, v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal json value: %w", err)
		}
		buf.Write(encoded)
		return nil
	}
}

func appendCanonicalMap(buf *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(k)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		if err := AppendCanonical(buf, m[k]); err != nil {
			return fmt.Errorf("append canonical map value for key %s: %w", k, err)
		}
	}
	buf.WriteByte('}')
	return nil
}

func appendCanonicalSlice(buf *strings.Builder, items []any) error {
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := AppendCanonical(buf, item); err != nil {
			return fmt.Errorf("append canonical slice item at index %d: %w", i, err)
		}
	}
	buf.WriteByte(']')
	return nil
}

// IsTarGz returns true if the file name ends with .tgz or .tar.gz.
func IsTarGz(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
}

// Exists returns true if the path exists on disk.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FindPackRoot locates the directory containing pack.yaml in an extracted archive.
func FindPackRoot(dir string) (string, error) {
	if Exists(filepath.Join(dir, "pack.yaml")) || Exists(filepath.Join(dir, "pack.yml")) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read pack directory: %w", err)
	}
	if len(entries) != 1 {
		return "", errors.New("pack.yaml not found in archive root")
	}
	if !entries[0].IsDir() {
		return "", errors.New("pack.yaml not found in archive root")
	}
	subdir := filepath.Join(dir, entries[0].Name())
	if Exists(filepath.Join(subdir, "pack.yaml")) || Exists(filepath.Join(subdir, "pack.yml")) {
		return subdir, nil
	}
	return "", errors.New("pack.yaml not found in archive")
}

// ExtractTarGzReader extracts a gzipped tar stream into the destination directory.
func ExtractTarGzReader(src io.Reader, dest string) error {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("create gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var (
		files   int
		totalSz int64
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}
		target, err := SafeJoin(dest, hdr.Name)
		if err != nil {
			return fmt.Errorf("validate tar entry path %s: %w", hdr.Name, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil { // #nosec -- target path is validated by SafeJoin.
				return fmt.Errorf("extract tar.gz mkdir: %w", err)
			}
		case tar.TypeReg:
			files++
			if files > MaxPackFiles {
				return fmt.Errorf("pack archive exceeds max files (%d)", MaxPackFiles)
			}
			if hdr.Size < 0 || hdr.Size > MaxPackFileBytes {
				return fmt.Errorf("pack file too large: %s", hdr.Name)
			}
			totalSz += hdr.Size
			if totalSz > MaxPackUncompressedBytes {
				return fmt.Errorf("pack archive exceeds max size (%d bytes)", MaxPackUncompressedBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil { // #nosec -- target path is validated by SafeJoin.
				return fmt.Errorf("extract tar.gz mkdir: %w", err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec -- target path is validated by SafeJoin.
			if err != nil {
				return fmt.Errorf("extract tar.gz create file: %w", err)
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
				_ = out.Close()
				return fmt.Errorf("extract tar.gz copy: %w", err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("extract tar.gz close: %w", err)
			}
		default:
			return fmt.Errorf("pack archive contains disallowed entry type %d: %s", hdr.Typeflag, hdr.Name)
		}
	}
}

// SafeJoin securely joins a base directory with a relative path.
func SafeJoin(base, name string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(name))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute archive path: %s", name)
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	return target, nil
}
