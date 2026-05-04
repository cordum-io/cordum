package edge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	defaultRedactionMarker         = "<redacted>"
	defaultRedactionMaxDepth       = 32
	defaultRedactionMaxItems       = 1024
	defaultRedactionMaxStringBytes = 8 * 1024
	defaultRedactionMaxTotalBytes  = MaxInputRedactedBytes
	binaryRedactionPlaceholder     = "<redacted:binary>"
	tooLargeRedactionPlaceholder   = "<redacted:too_large>"
	nonFiniteRedactionPlaceholder  = "<redacted:non_finite>"

	// MaxRedactionInputBytes is the upper bound on raw input accepted by
	// edge redaction call sites BEFORE the value reaches the redactor.
	// EDGE-071: the redactor itself caps via defaultRedactionMaxTotalBytes
	// (= MaxInputRedactedBytes = 64 KiB), but call sites must enforce a
	// hard ceiling first as a memory-safety net so an attacker cannot
	// force the redactor to allocate against an arbitrarily large value.
	// Inputs above this cap MUST be truncated to the cap and STILL
	// scanned (never skip-scan-on-oversize); when the call site cannot
	// safely return a partial scan result, return SafePlaceholder or the
	// site's documented placeholder string.
	MaxRedactionInputBytes = 1 * 1024 * 1024 // 1 MiB
)

// RedactionHashMode controls which stable hashes are returned with a redaction
// result. Hashes are formatted as "sha256:<hex>".
type RedactionHashMode string

const (
	// RedactionHashDefault computes both original and redacted hashes.
	RedactionHashDefault RedactionHashMode = ""
	// RedactionHashNone disables hash computation.
	RedactionHashNone RedactionHashMode = "none"
	// RedactionHashOriginal computes only the original input hash.
	RedactionHashOriginal RedactionHashMode = "original"
	// RedactionHashRedacted computes only the redacted output hash.
	RedactionHashRedacted RedactionHashMode = "redacted"
	// RedactionHashBoth computes both original and redacted hashes.
	RedactionHashBoth RedactionHashMode = "both"
)

// RedactionOptions configures the generic Edge redactor. Zero values are safe
// defaults suitable for event input and artifact metadata.
type RedactionOptions struct {
	HashMode       RedactionHashMode
	MaxDepth       int
	MaxItems       int
	MaxStringBytes int
	MaxTotalBytes  int
	Marker         string
}

// RedactionResult is JSON-compatible redaction output plus metadata. Findings
// identify the type and path of each secret-like value without carrying values.
type RedactionResult struct {
	Value        any                `json:"value"`
	Changed      bool               `json:"changed"`
	Redacted     bool               `json:"redacted"`
	Truncated    bool               `json:"truncated"`
	Findings     []RedactionFinding `json:"findings,omitempty"`
	OriginalHash string             `json:"original_hash,omitempty"`
	RedactedHash string             `json:"redacted_hash,omitempty"`
}

// RedactionFinding identifies a redaction decision without storing the
// sensitive value that triggered it.
type RedactionFinding struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// RedactValue returns a JSON-compatible copy of value and redaction metadata.
func RedactValue(value any, opts RedactionOptions) (RedactionResult, error) {
	normalized := normalizeRedactionOptions(opts)
	state := &redactionState{}
	redacted := redactAny(value, "", 0, normalized, state)
	result := RedactionResult{
		Value:     redacted,
		Changed:   state.changed,
		Redacted:  state.redacted,
		Truncated: state.truncated,
		Findings:  state.findings,
	}
	if exceedsTotalLimit(redacted, normalized.maxTotalBytes) {
		result.Value = tooLargeRedactionPlaceholder
		result.Changed = true
		result.Redacted = true
		result.Truncated = true
		result.Findings = append(result.Findings, RedactionFinding{Type: "too_large", Path: ""})
	}
	if err := applyHashOptions(&result, value, result.Value, normalized); err != nil {
		return RedactionResult{}, err
	}
	return result, nil
}

// RedactJSON parses and redacts a JSON payload without including raw payload
// bytes in any returned error.
func RedactJSON(data []byte, opts RedactionOptions) (RedactionResult, error) {
	if len(data) == 0 {
		return RedactValue(nil, opts)
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return RedactionResult{}, fmt.Errorf("edge redaction: parse json: %w", err)
	}
	return RedactValue(payload, opts)
}

// SafePlaceholder returns a JSON-encoded fail-closed marker that callers
// MUST persist on the redaction-error path instead of the raw payload.
//
// EDGE-071: redaction is a data-loss-prevention boundary. The dangerous
// pattern is "log error, then persist raw payload as fallback" — that
// leaks the very secret the redactor was supposed to mask. Every Edge
// redaction call site whose error path needs a structured envelope must
// return SafePlaceholder; sites that document a string placeholder
// (e.g. <redacted>) keep that contract.
//
// The envelope intentionally does NOT carry the original payload — only
// metadata describing why redaction failed and how big the input was.
// `truncated` records whether the call site truncated to
// MaxRedactionInputBytes before invoking the redactor.
func SafePlaceholder(reason string, originalSize int, truncated bool) string {
	if reason == "" {
		reason = "unknown"
	}
	if originalSize < 0 {
		originalSize = 0
	}
	envelope := struct {
		Redacted        bool   `json:"redacted"`
		RedactionFailed bool   `json:"redaction_failed"`
		Reason          string `json:"reason"`
		Size            int    `json:"size"`
		Truncated       bool   `json:"truncated"`
	}{
		Redacted:        true,
		RedactionFailed: true,
		Reason:          reason,
		Size:            originalSize,
		Truncated:       truncated,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		// json.Marshal of a fixed-shape struct cannot fail in practice;
		// the fallback exists so callers never persist raw payload even
		// on this unreachable path.
		return `{"redacted":true,"redaction_failed":true,"reason":"marshal_error","size":0,"truncated":false}`
	}
	return string(encoded)
}

// RedactBytes redacts arbitrary bytes. Binary input is replaced by a safe
// placeholder so callers never need to log or persist the raw bytes.
func RedactBytes(data []byte, opts RedactionOptions) (RedactionResult, error) {
	normalized := normalizeRedactionOptions(opts)
	if !utf8.Valid(data) {
		result := RedactionResult{
			Value:     binaryRedactionPlaceholder,
			Changed:   true,
			Redacted:  true,
			Truncated: true,
			Findings:  []RedactionFinding{{Type: "binary", Path: ""}},
		}
		if err := applyHashOptions(&result, data, result.Value, normalized); err != nil {
			return RedactionResult{}, err
		}
		return result, nil
	}
	if json.Valid(data) {
		result, err := RedactJSON(data, opts)
		if err != nil {
			return RedactionResult{}, err
		}
		if err := overrideOriginalHashWithBytes(&result, data, normalized); err != nil {
			return RedactionResult{}, err
		}
		return result, nil
	}
	result, err := RedactValue(string(data), opts)
	if err != nil {
		return RedactionResult{}, err
	}
	if err := overrideOriginalHashWithBytes(&result, data, normalized); err != nil {
		return RedactionResult{}, err
	}
	return result, nil
}

type normalizedRedactionOptions struct {
	hashMode       RedactionHashMode
	maxDepth       int
	maxItems       int
	maxStringBytes int
	maxTotalBytes  int
	marker         string
}

func normalizeRedactionOptions(opts RedactionOptions) normalizedRedactionOptions {
	normalized := normalizedRedactionOptions{
		hashMode:       opts.HashMode,
		maxDepth:       opts.MaxDepth,
		maxItems:       opts.MaxItems,
		maxStringBytes: opts.MaxStringBytes,
		maxTotalBytes:  opts.MaxTotalBytes,
		marker:         opts.Marker,
	}
	if normalized.hashMode == RedactionHashDefault {
		normalized.hashMode = RedactionHashBoth
	}
	if normalized.maxDepth <= 0 {
		normalized.maxDepth = defaultRedactionMaxDepth
	}
	if normalized.maxItems <= 0 {
		normalized.maxItems = defaultRedactionMaxItems
	}
	if normalized.maxStringBytes <= 0 {
		normalized.maxStringBytes = defaultRedactionMaxStringBytes
	}
	if normalized.maxTotalBytes <= 0 {
		normalized.maxTotalBytes = defaultRedactionMaxTotalBytes
	}
	if normalized.marker == "" {
		normalized.marker = defaultRedactionMarker
	}
	return normalized
}

type redactionState struct {
	changed   bool
	redacted  bool
	truncated bool
	findings  []RedactionFinding
}

func (s *redactionState) record(path string, findingType string, redacted bool, truncated bool) {
	s.changed = true
	if redacted {
		s.redacted = true
	}
	if truncated {
		s.truncated = true
	}
	s.findings = append(s.findings, RedactionFinding{Type: findingType, Path: path})
}

var (
	bearerPattern     = regexp.MustCompile(`(?i)\b(?:authorization\s*:\s*)?bearer\s+[A-Za-z0-9._~+/=-]+`)
	privateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	envSecretPattern  = regexp.MustCompile(`(?im)^\s*[A-Z0-9_-]*(?:API[_-]?KEY|ACCESS[_-]?KEY|TOKEN|SECRET|PASSWORD|PASSWD|PRIVATE[_-]?KEY|CLIENT[_-]?SECRET|CREDENTIALS?)[A-Z0-9_-]*\s*=`)
	awsKeyPattern     = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	gcpKeyPattern     = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`)
	apiTokenPattern   = regexp.MustCompile(`\b(?:sk|ghp|github_pat)_[A-Za-z0-9_=-]{12,}\b|\bsk-[A-Za-z0-9_-]{12,}\b`)
	azurePattern      = regexp.MustCompile(`(?i)(?:AccountKey|SharedAccessSignature|sig)=`)
)

func redactAny(value any, path string, depth int, opts normalizedRedactionOptions, state *redactionState) any {
	if depth > opts.maxDepth {
		state.record(path, "too_large", true, true)
		return tooLargeRedactionPlaceholder
	}

	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return redactString(v, path, opts, state)
	case bool:
		return v
	case int:
		return v
	case int8:
		return v
	case int16:
		return v
	case int32:
		return v
	case int64:
		return v
	case uint:
		return v
	case uint8:
		return v
	case uint16:
		return v
	case uint32:
		return v
	case uint64:
		return v
	case float32:
		return redactFloat(float64(v), path, state)
	case float64:
		return redactFloat(v, path, state)
	case json.Number:
		return v
	case []byte:
		state.record(path, "binary", true, true)
		return binaryRedactionPlaceholder
	case map[string]any:
		return redactStringAnyMap(v, path, depth, opts, state)
	case map[string]string:
		asAny := make(map[string]any, len(v))
		for key, child := range v {
			asAny[key] = child
		}
		return redactStringAnyMap(asAny, path, depth, opts, state)
	case []any:
		return redactSlice(v, path, depth, opts, state)
	case []string:
		asAny := make([]any, len(v))
		for i, child := range v {
			asAny[i] = child
		}
		return redactSlice(asAny, path, depth, opts, state)
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		return redactReflectMap(rv, path, depth, opts, state)
	case reflect.Slice, reflect.Array:
		children := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			children[i] = rv.Index(i).Interface()
		}
		return redactSlice(children, path, depth, opts, state)
	case reflect.String:
		return redactString(rv.String(), path, opts, state)
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return redactFloat(rv.Float(), path, state)
	}

	compatible, err := jsonCompatibleCopy(value)
	if err != nil {
		state.record(path, "unsupported", true, true)
		return fmt.Sprintf("<redacted:unsupported:%T>", value)
	}
	return compatible
}

func redactStringAnyMap(values map[string]any, path string, depth int, opts normalizedRedactionOptions, state *redactionState) map[string]any {
	out := make(map[string]any, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	selectedKeys, truncated := redactionMapKeys(keys, opts.maxItems)
	if truncated {
		state.record(joinPath(path, "_truncated"), "too_large", true, true)
		out["_truncated"] = tooLargeRedactionPlaceholder
	}
	for _, key := range selectedKeys {
		childPath := joinPath(path, key)
		if _, ok := sensitiveKeyType(key); ok {
			state.record(childPath, "sensitive_key", true, false)
			out[key] = opts.marker
			continue
		}
		out[key] = redactAny(values[key], childPath, depth+1, opts, state)
	}
	return out
}

func redactReflectMap(values reflect.Value, path string, depth int, opts normalizedRedactionOptions, state *redactionState) map[string]any {
	out := make(map[string]any, values.Len())
	keys := values.MapKeys()
	stringKeys := make([]string, 0, len(keys))
	keyLookup := make(map[string]reflect.Value, len(keys))
	for _, key := range keys {
		stringKey := fmt.Sprint(key.Interface())
		stringKeys = append(stringKeys, stringKey)
		keyLookup[stringKey] = key
	}

	selectedKeys, truncated := redactionMapKeys(stringKeys, opts.maxItems)
	if truncated {
		state.record(joinPath(path, "_truncated"), "too_large", true, true)
		out["_truncated"] = tooLargeRedactionPlaceholder
	}
	for _, stringKey := range selectedKeys {
		childPath := joinPath(path, stringKey)
		if _, ok := sensitiveKeyType(stringKey); ok {
			state.record(childPath, "sensitive_key", true, false)
			out[stringKey] = opts.marker
			continue
		}
		out[stringKey] = redactAny(values.MapIndex(keyLookup[stringKey]).Interface(), childPath, depth+1, opts, state)
	}
	return out
}

func redactionMapKeys(keys []string, maxItems int) ([]string, bool) {
	sort.Strings(keys)
	if len(keys) <= maxItems {
		return keys, false
	}
	sensitive := make([]string, 0)
	regular := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := sensitiveKeyType(key); ok {
			sensitive = append(sensitive, key)
			continue
		}
		regular = append(regular, key)
	}
	selected := make([]string, 0, maxItems)
	selected = appendRedactionMapKeys(selected, sensitive, maxItems)
	selected = appendRedactionMapKeys(selected, regular, maxItems)
	sort.Strings(selected)
	return selected, true
}

func appendRedactionMapKeys(dst, src []string, maxItems int) []string {
	for _, key := range src {
		if len(dst) >= maxItems {
			return dst
		}
		dst = append(dst, key)
	}
	return dst
}

func redactSlice(values []any, path string, depth int, opts normalizedRedactionOptions, state *redactionState) []any {
	limit := len(values)
	if limit > opts.maxItems {
		limit = opts.maxItems
	}

	capacity := limit
	if len(values) > opts.maxItems && capacity < math.MaxInt {
		capacity++
	}

	out := make([]any, 0, capacity)
	for i := 0; i < limit; i++ {
		out = append(out, redactAny(values[i], joinPath(path, strconv.Itoa(i)), depth+1, opts, state))
	}
	if len(values) > opts.maxItems {
		state.record(joinPath(path, strconv.Itoa(limit)), "too_large", true, true)
		out = append(out, tooLargeRedactionPlaceholder)
	}
	return out
}

func redactString(value string, path string, opts normalizedRedactionOptions, state *redactionState) any {
	if !utf8.ValidString(value) {
		state.record(path, "binary", true, true)
		return binaryRedactionPlaceholder
	}
	if len([]byte(value)) > opts.maxStringBytes {
		state.record(path, "too_large", true, true)
		return tooLargeRedactionPlaceholder
	}
	if findingType, ok := secretStringType(value); ok {
		state.record(path, findingType, true, false)
		return opts.marker
	}
	return value
}

func redactFloat(value float64, path string, state *redactionState) any {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		state.record(path, "non_finite", true, true)
		return nonFiniteRedactionPlaceholder
	}
	return value
}

func sensitiveKeyType(key string) (string, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), " ", "_"))
	switch normalized {
	case "authorization", "x_api_key", "api_key", "apikey", "token", "access_token", "refresh_token",
		"password", "passwd", "pwd", "client_secret", "private_key", "secret", "credential", "credentials",
		"aws_access_key_id", "aws_secret_access_key", "azure_client_secret", "google_api_key":
		return "sensitive_key", true
	default:
		if strings.HasSuffix(normalized, "_token") ||
			strings.HasSuffix(normalized, "_secret") ||
			strings.Contains(normalized, "_secret_") ||
			strings.HasSuffix(normalized, "_password") ||
			strings.HasSuffix(normalized, "_api_key") ||
			strings.Contains(normalized, "_api_key_") ||
			strings.HasSuffix(normalized, "_access_key") ||
			strings.Contains(normalized, "_access_key_") ||
			strings.HasSuffix(normalized, "_private_key") {
			return "sensitive_key", true
		}
		return "", false
	}
}

func secretStringType(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "secret://"):
		return "secret_ref", true
	case privateKeyPattern.MatchString(value):
		return "private_key", true
	case envSecretPattern.MatchString(value):
		return "env_secret", true
	case bearerPattern.MatchString(value):
		return "bearer_token", true
	case awsKeyPattern.MatchString(value):
		return "aws_credential", true
	case gcpKeyPattern.MatchString(value):
		return "gcp_credential", true
	case azurePattern.MatchString(value):
		return "azure_credential", true
	case apiTokenPattern.MatchString(value):
		return "api_key", true
	default:
		return "", false
	}
}

func joinPath(parent string, segment string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
	if parent == "" {
		return "/" + escaped
	}
	return parent + "/" + escaped
}

func exceedsTotalLimit(value any, maxBytes int) bool {
	if maxBytes <= 0 {
		return false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return true
	}
	return len(data) > maxBytes
}

func jsonCompatibleCopy(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func applyHashOptions(result *RedactionResult, original any, redacted any, opts normalizedRedactionOptions) error {
	switch opts.hashMode {
	case RedactionHashNone:
		return nil
	case RedactionHashOriginal:
		hash, err := stableSHA256(original)
		if err != nil {
			return fmt.Errorf("edge redaction: hash original: %w", err)
		}
		result.OriginalHash = hash
	case RedactionHashRedacted:
		hash, err := stableSHA256(redacted)
		if err != nil {
			return fmt.Errorf("edge redaction: hash redacted: %w", err)
		}
		result.RedactedHash = hash
	case RedactionHashBoth:
		originalHash, err := stableSHA256(original)
		if err != nil {
			return fmt.Errorf("edge redaction: hash original: %w", err)
		}
		redactedHash, err := stableSHA256(redacted)
		if err != nil {
			return fmt.Errorf("edge redaction: hash redacted: %w", err)
		}
		result.OriginalHash = originalHash
		result.RedactedHash = redactedHash
	default:
		return fmt.Errorf("edge redaction: unsupported hash mode %q", opts.hashMode)
	}
	return nil
}

func overrideOriginalHashWithBytes(result *RedactionResult, data []byte, opts normalizedRedactionOptions) error {
	switch opts.hashMode {
	case RedactionHashOriginal, RedactionHashBoth:
		rawHash, err := stableSHA256(data)
		if err != nil {
			return fmt.Errorf("edge redaction: hash original bytes: %w", err)
		}
		result.OriginalHash = rawHash
	}
	return nil
}

func stableSHA256(value any) (string, error) {
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	default:
		normalized := hashCompatibleCopy(v)
		var err error
		data, err = json.Marshal(normalized)
		if err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func hashCompatibleCopy(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return v
	case bool:
		return v
	case int:
		return v
	case int8:
		return v
	case int16:
		return v
	case int32:
		return v
	case int64:
		return v
	case uint:
		return v
	case uint8:
		return v
	case uint16:
		return v
	case uint32:
		return v
	case uint64:
		return v
	case float32:
		return hashCompatibleFloat(float64(v))
	case float64:
		return hashCompatibleFloat(v)
	case json.Number:
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = hashCompatibleCopy(child)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = child
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = hashCompatibleCopy(child)
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = child
		}
		return out
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			out[fmt.Sprint(key.Interface())] = hashCompatibleCopy(rv.MapIndex(key).Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = hashCompatibleCopy(rv.Index(i).Interface())
		}
		return out
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return hashCompatibleFloat(rv.Float())
	}

	compatible, err := jsonCompatibleCopy(value)
	if err != nil {
		return fmt.Sprintf("<redacted:unsupported:%T>", value)
	}
	return compatible
}

func hashCompatibleFloat(value float64) any {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nonFiniteRedactionPlaceholder
	}
	return value
}
