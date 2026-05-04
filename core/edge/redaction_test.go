package edge

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

const wantRedactionMarker = "<redacted>"

func TestRedactValueRedactsSensitiveKeysRecursively(t *testing.T) {
	sentinels := map[string]string{
		"authorization": "cordum_fake_bearer_secret_12345",
		"x_api_key":     "cordum_fake_x_api_key_12345",
		"api_key":       "cordum_fake_api_key_12345",
		"token":         "cordum_fake_token_12345",
		"password":      "cordum_fake_password_12345",
		"clientSecret":  "cordum_fake_client_secret_12345",
		"privateKey":    "cordum_fake_private_key_12345",
		"listPassword":  "cordum_fake_list_password_12345",
	}
	payload := map[string]any{
		"Authorization": "Bearer " + sentinels["authorization"],
		"X-API-Key":     sentinels["x_api_key"],
		"api_key":       sentinels["api_key"],
		"nested": map[string]any{
			"token":         sentinels["token"],
			"password":      sentinels["password"],
			"client_secret": sentinels["clientSecret"],
			"private_key":   sentinels["privateKey"],
			"safe":          "keep-me",
		},
		"list": []any{
			map[string]any{"Password": sentinels["listPassword"]},
			"keep-list",
		},
	}

	result, err := RedactValue(payload, RedactionOptions{})
	if err != nil {
		t.Fatalf("redact value returned error: %v", err)
	}
	if !result.Redacted || !result.Changed {
		t.Fatalf("expected redaction result to report changed and redacted")
	}

	root := requireMap(t, result.Value)
	assertMarkerAt(t, root, "Authorization")
	assertMarkerAt(t, root, "X-API-Key")
	assertMarkerAt(t, root, "api_key")

	nested := requireMap(t, root["nested"])
	for _, key := range []string{"token", "password", "client_secret", "private_key"} {
		assertMarkerAt(t, nested, key)
	}
	if nested["safe"] != "keep-me" {
		t.Fatalf("safe nested value was not preserved")
	}

	list := requireList(t, root["list"])
	listObject := requireMap(t, list[0])
	assertMarkerAt(t, listObject, "Password")
	if list[1] != "keep-list" {
		t.Fatalf("safe list value was not preserved")
	}

	assertFindingPaths(t, result.Findings,
		"/Authorization",
		"/X-API-Key",
		"/api_key",
		"/nested/token",
		"/nested/password",
		"/nested/client_secret",
		"/nested/private_key",
		"/list/0/Password",
	)
	assertNoSentinelLeaks(t, result.Value, sentinels)
	assertNoSentinelLeaks(t, result.Findings, sentinels)
}

func TestRedactValueRedactsKnownSecretPatternsInStrings(t *testing.T) {
	sentinels := map[string]string{
		"bearer":      "cordum_fake_bearer_pattern_12345",
		"secretRef":   "secret://vault/team/api-token",
		"envPassword": "cordum_fake_env_password_12345",
		"awsID":       "AKIAIOSFODNN7EXAMPLE",
		"awsSecret":   "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"pemBody":     "cordumfakeprivatekeybody12345",
		"gcpKey":      "AIzaSyD-CORDUMFakeGCPKeyValue1234567890123",
		"azureKey":    "CORDUMFAKEAZUREACCOUNTKEY1234567890==",
		"azureSig":    "cordumFakeAzureSignature1234567890",
	}
	payload := map[string]any{
		"plain":      "visible text",
		"bearer":     "Authorization: Bearer " + sentinels["bearer"],
		"secret_ref": sentinels["secretRef"],
		"env": strings.Join([]string{
			"SAFE_VALUE=visible",
			"PASSWORD=" + sentinels["envPassword"],
			"AWS_ACCESS_KEY_ID=" + sentinels["awsID"],
			"AWS_SECRET_ACCESS_KEY=" + sentinels["awsSecret"],
		}, "\n"),
		"pem": "-----BEGIN PRIVATE KEY-----\n" + sentinels["pemBody"] + "\n-----END PRIVATE KEY-----",
		"aws": "using " + sentinels["awsID"] + " and " + sentinels["awsSecret"],
		"gcp": `{"type":"service_account","api_key":"` + sentinels["gcpKey"] + `","private_key_id":"fake"}`,
		"azure": "DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=" + sentinels["azureKey"] +
			";EndpointSuffix=core.windows.net;SharedAccessSignature=sv=2023-01-03&sig=" + sentinels["azureSig"],
		"nested": []any{
			map[string]any{"header": "authorization: bearer " + sentinels["bearer"]},
			sentinels["secretRef"],
		},
	}

	result, err := RedactValue(payload, RedactionOptions{})
	if err != nil {
		t.Fatalf("redact value returned error: %v", err)
	}
	if !result.Redacted || !result.Changed {
		t.Fatalf("expected redaction result to report pattern redactions")
	}

	root := requireMap(t, result.Value)
	if root["plain"] != "visible text" {
		t.Fatalf("plain string was not preserved")
	}
	for _, key := range []string{"bearer", "secret_ref", "env", "pem", "aws", "gcp", "azure"} {
		assertStringContainsMarker(t, root[key])
	}
	nested := requireList(t, root["nested"])
	assertStringContainsMarker(t, requireMap(t, nested[0])["header"])
	assertStringContainsMarker(t, nested[1])

	assertFindingTypes(t, result.Findings,
		"bearer_token",
		"secret_ref",
		"env_secret",
		"private_key",
		"aws_credential",
		"gcp_credential",
		"azure_credential",
	)
	assertNoSentinelLeaks(t, result.Value, sentinels)
	assertNoSentinelLeaks(t, result.Findings, sentinels)
}

func TestRedactValueRedactsCommonEnvKeyAssignments(t *testing.T) {
	sentinels := map[string]string{
		"openai":    "cordum_fake_openai_secret_12345",
		"xAPI":      "cordum_fake_x_api_secret_12345",
		"github":    "cordum_fake_github_token_12345",
		"anthropic": "cordum_fake_anthropic_secret_12345",
		"cordum":    "cordum_fake_cordum_api_key_12345",
		"password":  "cordum_fake_service_password_12345",
	}
	envPayload := strings.Join([]string{
		"SAFE_VALUE=visible",
		"OPENAI_API_KEY=" + sentinels["openai"],
		"X_API_KEY=" + sentinels["xAPI"],
		"GITHUB_TOKEN=" + sentinels["github"],
		"ANTHROPIC_API_KEY=" + sentinels["anthropic"],
		"CORDUM_API_KEY=" + sentinels["cordum"],
		"SERVICE_PASSWORD=" + sentinels["password"],
	}, "\n")
	payload := map[string]any{
		"env":  envPayload,
		"safe": "visible",
	}

	result, err := RedactValue(payload, RedactionOptions{})
	if err != nil {
		t.Fatalf("redact value returned error: %v", err)
	}
	if !result.Redacted || !result.Changed {
		t.Fatalf("expected common env key assignments to be redacted")
	}
	root := requireMap(t, result.Value)
	assertStringContainsMarker(t, root["env"])
	if root["safe"] != "visible" {
		t.Fatalf("safe env-adjacent value was not preserved")
	}
	assertFindingTypes(t, result.Findings, "env_secret")
	assertFindingPaths(t, result.Findings, "/env")
	assertNoSentinelLeaks(t, result.Value, sentinels)
	assertNoSentinelLeaks(t, result.Findings, sentinels)
}

func TestRedactValueRedactsEmbeddedSecretReferences(t *testing.T) {
	sentinels := map[string]string{
		"credentials": "secret://vault/name",
		"envLine":     "secret://vault/cordum",
		"jsonish":     "secret://vault/json",
		"nested":      "secret://vault/list",
	}
	payload := map[string]any{
		"assignment": "credentials=" + sentinels["credentials"],
		"envLine":    "CORDUM_SECRET_REF=" + sentinels["envLine"],
		"jsonish":    `{"ref":"` + sentinels["jsonish"] + `"}`,
		"nested": []any{
			"prefix(" + sentinels["nested"] + ")",
		},
	}

	result, err := RedactValue(payload, RedactionOptions{})
	if err != nil {
		t.Fatalf("redact value returned error: %v", err)
	}
	if !result.Redacted || !result.Changed {
		t.Fatalf("expected embedded secret references to be redacted")
	}
	root := requireMap(t, result.Value)
	for _, key := range []string{"assignment", "envLine", "jsonish"} {
		assertStringContainsMarker(t, root[key])
	}
	nested := requireList(t, root["nested"])
	assertStringContainsMarker(t, nested[0])
	assertFindingTypes(t, result.Findings, "secret_ref")
	assertFindingPaths(t, result.Findings, "/assignment", "/envLine", "/jsonish", "/nested/0")
	assertNoSentinelLeaks(t, result.Value, sentinels)
	assertNoSentinelLeaks(t, result.Findings, sentinels)
}

func TestRedactValueRedactsQARegressionScalarStrings(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		sentinel    string
		findingType string
	}{
		{
			name:        "openai api key env",
			input:       "OPENAI_API_KEY=cordum_fake_openai_secret_12345",
			sentinel:    "cordum_fake_openai_secret_12345",
			findingType: "env_secret",
		},
		{
			name:        "x api key env",
			input:       "X_API_KEY=cordum_fake_x_api_secret_12345",
			sentinel:    "cordum_fake_x_api_secret_12345",
			findingType: "env_secret",
		},
		{
			name:        "github token env",
			input:       "GITHUB_TOKEN=cordum_fake_github_token_12345",
			sentinel:    "cordum_fake_github_token_12345",
			findingType: "env_secret",
		},
		{
			name:        "embedded secret ref",
			input:       "credentials=secret://vault/name",
			sentinel:    "secret://vault/name",
			findingType: "secret_ref",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := RedactValue(tc.input, RedactionOptions{})
			if err != nil {
				t.Fatalf("redact value returned error: %v", err)
			}
			if !result.Redacted || !result.Changed {
				t.Fatalf("expected scalar QA regression input to be redacted")
			}
			assertStringContainsMarker(t, result.Value)
			if len(result.Findings) != 1 {
				t.Fatalf("expected one finding for scalar QA regression input")
			}
			if result.Findings[0].Type != tc.findingType {
				t.Fatalf("finding type mismatch: got %q want %q", result.Findings[0].Type, tc.findingType)
			}
			assertNoSentinelLeaks(t, result.Value, map[string]string{tc.name: tc.sentinel})
			assertNoSentinelLeaks(t, result.Findings, map[string]string{tc.name: tc.sentinel})
		})
	}
}

func TestRedactJSONReturnsJSONCompatibleRedactedData(t *testing.T) {
	sentinels := map[string]string{
		"apiKey": "cordum_fake_json_api_key_12345",
		"bearer": "cordum_fake_json_bearer_12345",
	}
	input := []byte(`{"api_key":"` + sentinels["apiKey"] + `","headers":["Authorization: Bearer ` + sentinels["bearer"] + `"],"ok":"value"}`)

	result, err := RedactJSON(input, RedactionOptions{})
	if err != nil {
		t.Fatalf("redact json returned error: %v", err)
	}
	if !result.Redacted || !result.Changed {
		t.Fatalf("expected JSON redaction result to report changes")
	}
	root := requireMap(t, result.Value)
	assertMarkerAt(t, root, "api_key")
	headers := requireList(t, root["headers"])
	assertStringContainsMarker(t, headers[0])
	if root["ok"] != "value" {
		t.Fatalf("safe JSON value was not preserved")
	}
	if _, err := json.Marshal(result.Value); err != nil {
		t.Fatalf("redacted value is not JSON-compatible: %v", err)
	}
	assertNoSentinelLeaks(t, result.Value, sentinels)
	assertNoSentinelLeaks(t, result.Findings, sentinels)
}

func TestRedactValueHashesAreCanonicalAndModeSpecific(t *testing.T) {
	sentinels := map[string]string{
		"token": "cordum_fake_hash_token_12345",
	}
	first := map[string]any{
		"b": 2,
		"a": map[string]any{
			"token": sentinels["token"],
			"safe":  "same",
		},
	}
	second := map[string]any{
		"a": map[string]any{
			"safe":  "same",
			"token": sentinels["token"],
		},
		"b": 2,
	}

	firstResult, err := RedactValue(first, RedactionOptions{HashMode: RedactionHashBoth})
	if err != nil {
		t.Fatalf("redact first hash fixture: %v", err)
	}
	secondResult, err := RedactValue(second, RedactionOptions{HashMode: RedactionHashBoth})
	if err != nil {
		t.Fatalf("redact second hash fixture: %v", err)
	}

	assertSHA256Hash(t, firstResult.OriginalHash)
	assertSHA256Hash(t, firstResult.RedactedHash)
	if firstResult.OriginalHash != secondResult.OriginalHash {
		t.Fatalf("original hash was not stable across map insertion order")
	}
	if firstResult.RedactedHash != secondResult.RedactedHash {
		t.Fatalf("redacted hash was not stable across map insertion order")
	}
	if firstResult.OriginalHash == firstResult.RedactedHash {
		t.Fatalf("original and redacted hash modes should be distinct when redaction changes input")
	}

	changed := map[string]any{
		"b": 2,
		"a": map[string]any{
			"token": sentinels["token"],
			"safe":  "different",
		},
	}
	changedResult, err := RedactValue(changed, RedactionOptions{HashMode: RedactionHashBoth})
	if err != nil {
		t.Fatalf("redact changed hash fixture: %v", err)
	}
	if changedResult.RedactedHash == firstResult.RedactedHash {
		t.Fatalf("changing a non-redacted field did not change the redacted hash")
	}
	assertNoSentinelLeaks(t, firstResult.Value, sentinels)
}

func TestRedactBytesHashesOriginalBytesBeforeJSONParsing(t *testing.T) {
	compactJSON := []byte(`{"safe":"value","count":1}`)
	spacedJSON := []byte("{\n  \"count\": 1,\n  \"safe\": \"value\"\n}")

	compactResult, err := RedactBytes(compactJSON, RedactionOptions{HashMode: RedactionHashBoth})
	if err != nil {
		t.Fatalf("redact compact JSON bytes: %v", err)
	}
	spacedResult, err := RedactBytes(spacedJSON, RedactionOptions{HashMode: RedactionHashBoth})
	if err != nil {
		t.Fatalf("redact spaced JSON bytes: %v", err)
	}

	assertSHA256Hash(t, compactResult.OriginalHash)
	assertSHA256Hash(t, compactResult.RedactedHash)
	assertSHA256Hash(t, spacedResult.OriginalHash)
	assertSHA256Hash(t, spacedResult.RedactedHash)
	if compactResult.OriginalHash == spacedResult.OriginalHash {
		t.Fatalf("original byte hashes should differ when JSON byte formatting differs")
	}
	if compactResult.RedactedHash != spacedResult.RedactedHash {
		t.Fatalf("redacted canonical hashes should match for equivalent JSON values")
	}
	if compactResult.Redacted || spacedResult.Redacted {
		t.Fatalf("safe JSON byte inputs should not report redaction")
	}
}

func TestRedactBytesHashesOriginalTextBytes(t *testing.T) {
	byteResult, err := RedactBytes([]byte("plain text"), RedactionOptions{HashMode: RedactionHashOriginal})
	if err != nil {
		t.Fatalf("redact plain text bytes: %v", err)
	}
	valueResult, err := RedactValue("plain text", RedactionOptions{HashMode: RedactionHashOriginal})
	if err != nil {
		t.Fatalf("redact plain text value: %v", err)
	}
	assertSHA256Hash(t, byteResult.OriginalHash)
	if byteResult.OriginalHash == valueResult.OriginalHash {
		t.Fatalf("plain text bytes should hash the original byte slice, not JSON string encoding")
	}
}

func TestRedactValueBoundsHostilePayloadsWithoutRawBytes(t *testing.T) {
	sentinels := map[string]string{
		"deep": "cordum_fake_deep_secret_12345",
		"long": "cordum_fake_long_secret_12345",
	}
	payload := map[string]any{
		"deep": map[string]any{
			"level1": map[string]any{
				"level2": map[string]any{
					"token": sentinels["deep"],
				},
			},
		},
		"items": []any{"one", "two", "three", "four"},
		"long":  strings.Repeat("x", 40) + sentinels["long"],
	}

	result, err := RedactValue(payload, RedactionOptions{
		HashMode:       RedactionHashBoth,
		MaxDepth:       2,
		MaxItems:       2,
		MaxStringBytes: 16,
		MaxTotalBytes:  192,
	})
	if err != nil {
		t.Fatalf("redact hostile payload returned error: %v", err)
	}
	if !result.Truncated {
		t.Fatalf("expected hostile payload redaction to report truncation")
	}
	assertSHA256Hash(t, result.OriginalHash)
	assertSHA256Hash(t, result.RedactedHash)
	assertNoSentinelLeaks(t, result.Value, sentinels)
	data, err := json.Marshal(result.Value)
	if err != nil {
		t.Fatalf("marshal bounded redaction output: %v", err)
	}
	if !strings.Contains(string(data), "redacted:too_large") {
		t.Fatalf("bounded redaction output did not include a safe truncation placeholder")
	}
}

func TestRedactValueChecksSensitiveMapKeysBeforeTruncating(t *testing.T) {
	const secret = "cordum_fake_map_cutoff_secret_12345"
	payload := map[string]any{
		"aaa":                       "safe-a",
		"bbb":                       "safe-b",
		"zzz_aws_secret_access_key": secret,
	}

	result, err := RedactValue(payload, RedactionOptions{MaxItems: 2})
	if err != nil {
		t.Fatalf("redact map cutoff payload: %v", err)
	}
	got, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("redacted value type = %T, want map[string]any", result.Value)
	}
	if got["zzz_aws_secret_access_key"] != defaultRedactionMarker {
		t.Fatalf("sensitive key past lexical cutoff = %#v, want marker in %#v", got["zzz_aws_secret_access_key"], got)
	}
	assertNoSentinelLeaks(t, result.Value, map[string]string{"cutoff": secret})
	if !result.Truncated {
		t.Fatalf("expected truncation metadata for over-limit map")
	}
}

func TestRedactValueNonFiniteNumbersUseSafePlaceholder(t *testing.T) {
	payload := map[string]any{
		"nan":  math.NaN(),
		"inf":  math.Inf(1),
		"safe": 1.5,
	}

	result, err := RedactValue(payload, RedactionOptions{HashMode: RedactionHashBoth})
	if err != nil {
		t.Fatalf("redact non-finite numbers returned error: %v", err)
	}
	if !result.Redacted || !result.Truncated {
		t.Fatalf("non-finite redaction should report redacted and truncated")
	}
	root := requireMap(t, result.Value)
	if root["nan"] != "<redacted:non_finite>" || root["inf"] != "<redacted:non_finite>" {
		t.Fatalf("non-finite values were not replaced with safe placeholders")
	}
	if root["safe"] != 1.5 {
		t.Fatalf("finite float was not preserved")
	}
	assertSHA256Hash(t, result.OriginalHash)
	assertSHA256Hash(t, result.RedactedHash)
	assertFindingTypes(t, result.Findings, "non_finite")
}

func TestRedactBytesAndInvalidJSONNeverEchoRawInput(t *testing.T) {
	sentinels := map[string]string{
		"binary":      "cordum_fake_binary_secret_12345",
		"invalidJSON": "cordum_fake_invalid_json_secret_12345",
	}
	binary := append([]byte{0xff, 0xfe, 0x00}, []byte(sentinels["binary"])...)
	binaryResult, err := RedactBytes(binary, RedactionOptions{HashMode: RedactionHashOriginal})
	if err != nil {
		t.Fatalf("redact binary bytes returned error: %v", err)
	}
	if binaryResult.Value != "<redacted:binary>" {
		t.Fatalf("binary input was not replaced by the safe binary placeholder")
	}
	assertSHA256Hash(t, binaryResult.OriginalHash)
	assertNoSentinelLeaks(t, binaryResult.Value, sentinels)

	invalidJSON := []byte(`{"api_key":"` + sentinels["invalidJSON"] + `"`)
	_, err = RedactJSON(invalidJSON, RedactionOptions{HashMode: RedactionHashOriginal})
	if err == nil {
		t.Fatalf("expected invalid JSON redaction to return an error")
	}
	if strings.Contains(err.Error(), sentinels["invalidJSON"]) {
		t.Fatalf("invalid JSON error leaked synthetic sentinel")
	}
}

func TestRedactValueDoesNotMutateCallerInput(t *testing.T) {
	sentinels := map[string]string{
		"token":    "cordum_fake_mutation_token_12345",
		"password": "cordum_fake_mutation_password_12345",
	}
	original := map[string]any{
		"nested": map[string]any{"token": sentinels["token"]},
		"list": []any{
			map[string]any{"password": sentinels["password"]},
			"safe",
		},
	}
	before := mustMarshalForComparison(t, original)

	result, err := RedactValue(original, RedactionOptions{HashMode: RedactionHashRedacted})
	if err != nil {
		t.Fatalf("redact mutation fixture: %v", err)
	}
	after := mustMarshalForComparison(t, original)
	if string(after) != string(before) {
		t.Fatalf("redaction mutated caller-owned input")
	}
	assertSHA256Hash(t, result.RedactedHash)
	assertNoSentinelLeaks(t, result.Value, sentinels)
}

func TestRedactValueNilAndEmptyInputsAreDeterministic(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{name: "nil", value: nil},
		{name: "empty_map", value: map[string]any{}},
		{name: "empty_list", value: []any{}},
		{name: "empty_string", value: ""},
	}

	for _, tc := range cases {
		first, err := RedactValue(tc.value, RedactionOptions{HashMode: RedactionHashBoth})
		if err != nil {
			t.Fatalf("redact deterministic case %s: %v", tc.name, err)
		}
		second, err := RedactValue(tc.value, RedactionOptions{HashMode: RedactionHashBoth})
		if err != nil {
			t.Fatalf("redact deterministic case repeat %s: %v", tc.name, err)
		}
		if first.Redacted || first.Changed {
			t.Fatalf("empty deterministic case %s should not report redaction", tc.name)
		}
		assertSHA256Hash(t, first.OriginalHash)
		assertSHA256Hash(t, first.RedactedHash)
		if first.OriginalHash != second.OriginalHash || first.RedactedHash != second.RedactedHash {
			t.Fatalf("hashes for deterministic case %s were not stable", tc.name)
		}
	}
}

func requireMap(t *testing.T, value any) map[string]any {
	t.Helper()
	got, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("redacted value had unexpected type %T", value)
	}
	return got
}

func requireList(t *testing.T, value any) []any {
	t.Helper()
	got, ok := value.([]any)
	if !ok {
		t.Fatalf("redacted list had unexpected type %T", value)
	}
	return got
}

func assertMarkerAt(t *testing.T, values map[string]any, key string) {
	t.Helper()
	if values[key] != wantRedactionMarker {
		t.Fatalf("value at expected redacted key %q was not replaced with marker", key)
	}
}

func assertStringContainsMarker(t *testing.T, value any) {
	t.Helper()
	got, ok := value.(string)
	if !ok {
		t.Fatalf("redacted pattern value had unexpected type %T", value)
	}
	if !strings.Contains(got, wantRedactionMarker) {
		t.Fatalf("redacted pattern value did not contain marker")
	}
}

func assertFindingPaths(t *testing.T, findings []RedactionFinding, want ...string) {
	t.Helper()
	seen := make(map[string]bool, len(findings))
	for _, finding := range findings {
		seen[finding.Path] = true
		if finding.Type == "" {
			t.Fatalf("finding type was empty")
		}
	}
	for _, path := range want {
		if !seen[path] {
			t.Fatalf("missing redaction finding path %q", path)
		}
	}
}

func assertFindingTypes(t *testing.T, findings []RedactionFinding, want ...string) {
	t.Helper()
	seen := make(map[string]bool, len(findings))
	for _, finding := range findings {
		seen[finding.Type] = true
		if finding.Path == "" {
			t.Fatalf("finding path was empty")
		}
	}
	for _, findingType := range want {
		if !seen[findingType] {
			t.Fatalf("missing redaction finding type %q", findingType)
		}
	}
}

func assertNoSentinelLeaks(t *testing.T, value any, sentinels map[string]string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal redaction inspection value: %v", err)
	}
	text := string(data)
	for label, sentinel := range sentinels {
		if strings.Contains(text, sentinel) {
			t.Fatalf("redacted output leaked synthetic sentinel %s", label)
		}
	}
}

func assertSHA256Hash(t *testing.T, value string) {
	t.Helper()
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		t.Fatalf("hash did not use sha256 prefix")
	}
	encoded := strings.TrimPrefix(value, prefix)
	if len(encoded) != 64 {
		t.Fatalf("hash had unexpected hex length")
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		t.Fatalf("hash was not valid hex: %v", err)
	}
}

func mustMarshalForComparison(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal comparison value: %v", err)
	}
	return data
}

// EDGE-071 — SafePlaceholder MUST emit a JSON envelope that callers can
// persist on the redaction-error path WITHOUT carrying the original
// payload. The presence of the redaction_failed=true field is the
// contract by which downstream code (e.g. dashboard timeline,
// audit-event consumers) can detect that the value was a fail-closed
// placeholder rather than a successfully-redacted result.
func TestSafePlaceholderEnvelopeShape(t *testing.T) {
	got := SafePlaceholder("redactor_error", 4096, true)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("SafePlaceholder produced non-JSON output: %q (err=%v)", got, err)
	}
	if parsed["redacted"] != true {
		t.Errorf("envelope.redacted = %v, want true (got=%q)", parsed["redacted"], got)
	}
	if parsed["redaction_failed"] != true {
		t.Errorf("envelope.redaction_failed = %v, want true (got=%q)", parsed["redaction_failed"], got)
	}
	if parsed["reason"] != "redactor_error" {
		t.Errorf("envelope.reason = %v, want %q (got=%q)", parsed["reason"], "redactor_error", got)
	}
	if parsed["truncated"] != true {
		t.Errorf("envelope.truncated = %v, want true (got=%q)", parsed["truncated"], got)
	}
	// json.Unmarshal decodes numeric to float64 by default.
	if size, ok := parsed["size"].(float64); !ok || int(size) != 4096 {
		t.Errorf("envelope.size = %v, want 4096 (got=%q)", parsed["size"], got)
	}
}

// EDGE-071 — empty/negative inputs collapse to safe defaults so a caller
// that doesn't know the original size doesn't accidentally publish a
// nonsense envelope (negative size, blank reason).
func TestSafePlaceholderNormalizesInputs(t *testing.T) {
	got := SafePlaceholder("", -42, false)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("SafePlaceholder produced non-JSON output: %q (err=%v)", got, err)
	}
	if parsed["reason"] != "unknown" {
		t.Errorf("envelope.reason for empty input = %v, want %q", parsed["reason"], "unknown")
	}
	if size, ok := parsed["size"].(float64); !ok || int(size) != 0 {
		t.Errorf("envelope.size for negative input = %v, want 0", parsed["size"])
	}
}

// EDGE-071 — SafePlaceholder MUST NEVER carry the original payload.
// This is the data-loss-prevention invariant: the envelope is metadata
// only. The test asserts that a sensitive value passed as `reason`
// (callers should not do that, but defensive check) does NOT leak
// alongside structural metadata; i.e. the only place the reason string
// appears is the bounded reason field.
func TestSafePlaceholderNeverEmbedsRawPayload(t *testing.T) {
	// A caller that mistakenly passes a literal secret-shaped string as
	// `reason` would still embed it in the JSON envelope; SafePlaceholder
	// is not a redactor. The contract this test pins is structural:
	// SafePlaceholder writes EXACTLY the documented fields and no
	// inadvertent extras. If the envelope grows a `payload` or `value`
	// field in the future, this test fails — which is the desired
	// regression signal.
	got := SafePlaceholder("redactor_error", 1024, false)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("SafePlaceholder produced non-JSON output: %q (err=%v)", got, err)
	}
	allowed := map[string]struct{}{
		"redacted":         {},
		"redaction_failed": {},
		"reason":           {},
		"size":             {},
		"truncated":        {},
	}
	for key := range parsed {
		if _, ok := allowed[key]; !ok {
			t.Errorf("envelope contains disallowed field %q (got=%q)", key, got)
		}
	}
}
