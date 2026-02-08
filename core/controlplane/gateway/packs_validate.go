package gateway

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

	"github.com/cordum/cordum/core/infra/buildinfo"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	wf "github.com/cordum/cordum/core/workflow"
	"gopkg.in/yaml.v3"
)

func loadPackBundleFromReader(src io.Reader) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "cordum-pack-*")
	if err != nil {
		return "", func() {}, err
	}
	if err := extractTarGzReader(src, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", func() {}, err
	}
	root, err := findPackRoot(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", func() {}, err
	}
	return root, func() { _ = os.RemoveAll(tmpDir) }, nil
}

func loadPackManifest(dir string) (*packManifest, error) {
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
	var manifest packManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse pack.yaml: %w", err)
	}
	return &manifest, nil
}

func validatePackManifest(manifest *packManifest) error {
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
	if strings.TrimSpace(manifest.Metadata.Version) == "" {
		return errors.New("metadata.version required")
	}
	for _, topic := range manifest.Topics {
		if topic.Name == "" {
			return errors.New("topic name required")
		}
		if !strings.HasPrefix(topic.Name, "job."+id+".") {
			return fmt.Errorf("topic %q must be namespaced under job.%s.*", topic.Name, id)
		}
	}
	for _, res := range manifest.Resources.Schemas {
		if res.ID == "" || res.Path == "" {
			return errors.New("schema id and path required")
		}
		if !strings.HasPrefix(res.ID, id+"/") {
			return fmt.Errorf("schema id %q must be namespaced under %s/", res.ID, id)
		}
	}
	for _, res := range manifest.Resources.Workflows {
		if res.ID == "" || res.Path == "" {
			return errors.New("workflow id and path required")
		}
		if !strings.HasPrefix(res.ID, id+".") {
			return fmt.Errorf("workflow id %q must be namespaced under %s.", res.ID, id)
		}
	}
	return nil
}

func ensureProtocolCompatible(manifest *packManifest) error {
	if manifest.Compatibility.ProtocolVersion == 0 {
		return errors.New("compatibility.protocolVersion required")
	}
	if manifest.Compatibility.ProtocolVersion != capsdk.DefaultProtocolVersion {
		return fmt.Errorf("pack protocol version %d is not compatible with this server (requires version %d); rebuild your pack with a compatible capsdk version", manifest.Compatibility.ProtocolVersion, capsdk.DefaultProtocolVersion)
	}
	return nil
}

func ensureCoreVersionCompatible(minCoreVersion string) error {
	minCoreVersion = strings.TrimSpace(minCoreVersion)
	if minCoreVersion == "" {
		return nil
	}
	minParsed, ok := parseSemver(minCoreVersion)
	if !ok {
		return fmt.Errorf("invalid minCoreVersion %q", minCoreVersion)
	}
	coreParsed, ok := parseSemver(buildinfo.Version)
	if !ok {
		// Allow installs on dev/unknown builds; use --force to bypass explicitly.
		return nil
	}
	if compareSemver(coreParsed, minParsed) < 0 {
		return fmt.Errorf("core version %s does not satisfy minCoreVersion %s", buildinfo.Version, minCoreVersion)
	}
	return nil
}

func parseSemver(raw string) ([3]int, bool) {
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

func compareSemver(left, right [3]int) int {
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

func shouldSkipConfigOverlay(inactive bool, overlay packConfigOverlay) bool {
	if !inactive {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(overlay.Key), "pools")
}

func hasPoolOverlay(overlays []packAppliedConfigOverlay) bool {
	for _, overlay := range overlays {
		if strings.EqualFold(overlay.Key, "pools") {
			return true
		}
	}
	return false
}

func validateConfigPatch(key string, patch map[string]any, packID string, current any) error {
	switch strings.ToLower(key) {
	case "pools":
		return validatePoolsPatch(patch, packID, current)
	case "timeouts":
		return validateTimeoutsPatch(patch, packID)
	default:
		return fmt.Errorf("unsupported config overlay key %q", key)
	}
}

func validatePoolsPatch(patch map[string]any, packID string, current any) error {
	rawTopics := normalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("pools.topics must be a map")
		}
		for topic := range topics {
			if !strings.HasPrefix(topic, "job."+packID+".") {
				return fmt.Errorf("pools topic %q must be namespaced under job.%s.*", topic, packID)
			}
		}
	}
	rawPools := normalizeJSON(patch["pools"])
	if rawPools != nil {
		pools, ok := rawPools.(map[string]any)
		if !ok {
			return errors.New("pools.pools must be a map")
		}
		existingPools := extractPools(current)
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

func extractPools(current any) map[string]struct{} {
	out := map[string]struct{}{}
	currentMap, ok := normalizeJSON(current).(map[string]any)
	if !ok || currentMap == nil {
		return out
	}
	rawPools := normalizeJSON(currentMap["pools"])
	pools, ok := rawPools.(map[string]any)
	if !ok || pools == nil {
		return out
	}
	for name := range pools {
		out[name] = struct{}{}
	}
	return out
}

func validateTimeoutsPatch(patch map[string]any, packID string) error {
	if patch == nil {
		return nil
	}
	rawTopics := normalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("timeouts.topics must be a map")
		}
		for topic := range topics {
			if !strings.HasPrefix(topic, "job."+packID+".") {
				return fmt.Errorf("timeouts topic %q must be namespaced under job.%s.*", topic, packID)
			}
		}
	}
	rawWorkflows := normalizeJSON(patch["workflows"])
	if rawWorkflows != nil {
		workflows, ok := rawWorkflows.(map[string]any)
		if !ok {
			return errors.New("timeouts.workflows must be a map")
		}
		for wf := range workflows {
			if !strings.HasPrefix(wf, packID+".") {
				return fmt.Errorf("timeout workflow %q must be namespaced under %s.", wf, packID)
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

func loadSchemaFile(dir, relPath string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", err
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", err
	}
	schemaMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("schema file must be an object")
	}
	digest, err := hashValue(schemaMap)
	if err != nil {
		return nil, "", err
	}
	return schemaMap, digest, nil
}

func loadWorkflowFile(dir, relPath, id string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", err
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", err
	}
	workflowMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("workflow file must be an object")
	}
	if id != "" {
		workflowMap["id"] = id
	}
	normalized := normalizeWorkflowMap(workflowMap)
	digest, err := hashValue(normalized)
	if err != nil {
		return nil, "", err
	}
	return workflowMap, digest, nil
}

func normalizeWorkflowMap(workflow map[string]any) map[string]any {
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

func hashWorkflow(workflow map[string]any) (string, error) {
	return hashValue(normalizeWorkflowMap(workflow))
}

func workflowToMap(workflow *wf.Workflow) map[string]any {
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

func loadDataFile(path string) (any, error) {
	// #nosec G304 -- path is validated by safeJoin at call sites.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload any
	if json.Unmarshal(data, &payload) == nil {
		return normalizeJSON(payload), nil
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return normalizeJSON(payload), nil
}

func loadPatchFile(dir, relPath string) (any, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, err
	}
	return loadDataFile(path)
}

func normalizeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			out[k] = normalizeJSON(child)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, child := range v {
			key := fmt.Sprint(k)
			out[key] = normalizeJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = normalizeJSON(child)
		}
		return out
	default:
		return v
	}
}

func deepCopy(value any) any {
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

func mergePatch(current any, patch map[string]any) any {
	if patch == nil {
		return current
	}
	currentMap, _ := normalizeJSON(current).(map[string]any)
	if currentMap == nil {
		currentMap = map[string]any{}
	}
	for key, value := range patch {
		switch v := value.(type) {
		case nil:
			delete(currentMap, key)
		case map[string]any:
			currentMap[key] = mergePatch(currentMap[key], v)
		default:
			currentMap[key] = v
		}
	}
	return currentMap
}

func buildDeletePatch(patch map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range patch {
		switch v := value.(type) {
		case map[string]any:
			out[key] = buildDeletePatch(v)
		default:
			out[key] = nil
		}
	}
	return out
}

func hashValue(value any) (string, error) {
	encoded, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	buf := &strings.Builder{}
	if err := appendCanonical(buf, value); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func appendCanonical(buf *strings.Builder, value any) error {
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
			return err
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
		if err := appendCanonical(buf, m[k]); err != nil {
			return err
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
		if err := appendCanonical(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func isTarGz(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findPackRoot(dir string) (string, error) {
	if exists(filepath.Join(dir, "pack.yaml")) || exists(filepath.Join(dir, "pack.yml")) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 {
		return "", errors.New("pack.yaml not found in archive root")
	}
	if !entries[0].IsDir() {
		return "", errors.New("pack.yaml not found in archive root")
	}
	subdir := filepath.Join(dir, entries[0].Name())
	if exists(filepath.Join(subdir, "pack.yaml")) || exists(filepath.Join(subdir, "pack.yml")) {
		return subdir, nil
	}
	return "", errors.New("pack.yaml not found in archive")
}

func extractTarGzReader(src io.Reader, dest string) error {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
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
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			files++
			if files > maxPackFiles {
				return fmt.Errorf("pack archive exceeds max files (%d)", maxPackFiles)
			}
			if hdr.Size < 0 || hdr.Size > maxPackFileBytes {
				return fmt.Errorf("pack file too large: %s", hdr.Name)
			}
			totalSz += hdr.Size
			if totalSz > maxPackUncompressedBytes {
				return fmt.Errorf("pack archive exceeds max size (%d bytes)", maxPackUncompressedBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			// #nosec G304 -- target path is validated by safeJoin.
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func safeJoin(base, name string) (string, error) {
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
