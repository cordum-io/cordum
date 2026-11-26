package config

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// deprecatedSchemaOnly lists the only schema properties allowed to have no
// struct field. These three rules[].constraints keys were added to the schema
// on purpose (task-e4e9489c) and documented, but no struct field or CAP proto
// field carries them, so nothing enforces them. Rejecting them now could stop
// a Safety Kernel from starting on a policy that followed the docs, so the
// schema keeps accepting them and marks them deprecated. Do not add to this
// list: every other schema-only property is drift and fails the test.
var deprecatedSchemaOnly = map[string]bool{
	"$.rules[].constraints.max_output_bytes":     true,
	"$.rules[].constraints.allowed_destinations": true,
	"$.rules[].constraints.redact_patterns":      true,
}

// TestSafetyPolicySchemaMatchesStruct keeps safety_policy.schema.json and the
// SafetyPolicy struct tree in lock-step. ParseSafetyPolicy validates against
// the schema and then yaml.Unmarshal's into the struct, which silently drops
// keys the struct lacks. So a schema-only property is accepted and then
// ignored (it looks enforced but is not), and a struct-only field can never
// be set because the schema rejects it. Either drift direction fails here,
// apart from the deprecatedSchemaOnly keys.
func TestSafetyPolicySchemaMatchesStruct(t *testing.T) {
	raw, err := configSchemaFS.ReadFile(safetyPolicySchemaFile)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	defs, _ := root["definitions"].(map[string]any)

	var walk func(path string, typ reflect.Type, node map[string]any)
	walk = func(path string, typ reflect.Type, node map[string]any) {
		if ref, _ := node["$ref"].(string); ref != "" {
			def, ok := defs[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any)
			if !ok {
				t.Fatalf("%s: unresolvable $ref %q", path, ref)
			}
			node = def
		}
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			items, ok := node["items"].(map[string]any)
			if !ok {
				t.Errorf("%s: schema has no items for %s", path, typ)
				return
			}
			walk(path+"[]", typ.Elem(), items)
		case reflect.Map:
			values, ok := node["additionalProperties"].(map[string]any)
			if !ok {
				t.Errorf("%s: schema has no additionalProperties schema for %s", path, typ)
				return
			}
			walk(path+".*", typ.Elem(), values)
		case reflect.Struct:
			if node["additionalProperties"] != false {
				t.Errorf("%s: schema must set additionalProperties: false (unknown keys are silently dropped by the parser)", path)
			}
			props, _ := node["properties"].(map[string]any)
			fields := map[string]reflect.Type{}
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				if !f.IsExported() {
					continue
				}
				name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
				if name == "-" {
					continue
				}
				if strings.Contains(opts, "inline") {
					t.Fatalf("%s.%s: inline yaml fields are not supported by this check", path, f.Name)
				}
				if name == "" {
					name = strings.ToLower(f.Name) // yaml.v3 default key
				}
				fields[name] = f.Type
			}
			for _, key := range sortedKeys(fields) {
				prop, ok := props[key].(map[string]any)
				if !ok {
					t.Errorf("%s.%s: %s field missing from the schema, so the schema rejects a key the parser supports", path, key, typ.Name())
					continue
				}
				walk(path+"."+key, fields[key], prop)
			}
			for _, key := range sortedKeys(props) {
				if _, ok := fields[key]; !ok && !deprecatedSchemaOnly[path+"."+key] {
					t.Errorf("%s.%s: schema property has no %s field, so it is accepted and then silently dropped", path, key, typ.Name())
				}
			}
		}
	}
	walk("$", reflect.TypeOf(SafetyPolicy{}), root)
}

// TestSafetyPolicyParsesFormerlyRejectedFields covers struct fields the schema
// used to reject, which made them impossible to set through any parsed policy.
func TestSafetyPolicyParsesFormerlyRejectedFields(t *testing.T) {
	policy, err := ParseSafetyPolicy([]byte(`
version: "1"
require_human:
  min_severity_for_deny: high
  min_confidence_for_deny: 0.8
  downgrade_when_prompt_only: true
tenants:
  default:
    mcp:
      gateway_enabled: true
      allowed_upstreams: ["tenant-tools"]
      upstream_servers:
        - name: tenant-tools
          transport: http
          endpoint: https://mcp.example.com/sse
          enabled: true
rules:
  - id: direct-or-delegated
    match:
      topics: ["job.*"]
      delegation:
        max_depth: 2
        delegation_required: false
    decision: allow
`))
	if err != nil {
		t.Fatalf("ParseSafetyPolicy: %v", err)
	}
	if got, want := policy.RequireHuman, (RequireHumanThreshold{MinSeverityForDeny: "high", MinConfidenceForDeny: 0.8, DowngradeWhenPromptOnly: true}); got != want {
		t.Errorf("RequireHuman = %+v; want %+v", got, want)
	}
	mcp := policy.Tenants["default"].MCP
	if !mcp.GatewayEnabled || len(mcp.AllowedUpstreams) != 1 || len(mcp.UpstreamServers) != 1 || mcp.UpstreamServers[0].Endpoint != "https://mcp.example.com/sse" {
		t.Errorf("tenant MCP gateway fields not parsed: %+v", mcp)
	}
	// They are reserved (not enforced); the kernel warns about them on load.
	if got := policy.TenantsSettingReservedMCPGatewayFields(); len(got) != 1 || got[0] != "default" {
		t.Errorf("TenantsSettingReservedMCPGatewayFields = %v; want [default]", got)
	}
	if got := (&SafetyPolicy{Tenants: map[string]TenantPolicy{"t": {}}}).TenantsSettingReservedMCPGatewayFields(); got != nil {
		t.Errorf("TenantsSettingReservedMCPGatewayFields without the fields = %v; want nil", got)
	}
	if req := policy.Rules[0].Match.Delegation.DelegationRequired; req == nil || *req {
		t.Errorf("Delegation.DelegationRequired = %v; want explicit false", req)
	}
	if _, err := ParseSafetyPolicy([]byte("version: \"1\"\nrequire_human:\n  min_confidence_for_deny: 1.5\n")); err == nil {
		t.Error("require_human.min_confidence_for_deny 1.5 accepted; want schema rejection (range 0..1)")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
