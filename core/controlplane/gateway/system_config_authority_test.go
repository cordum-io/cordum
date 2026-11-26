package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

const (
	systemConfigPath   = "/api/v1/config"
	systemConfigMarker = "system-config-tenant-b-marker"
	systemConfigTopic  = "job.system-config-b"
)

type systemConfigTarget struct{ name, scope, id string }

var systemConfigTargets = []systemConfigTarget{
	{"flat", "system", "default"},
	{"default", "system", "default"},
	{"topics", "system", "topics"},
	{"workers", "system", "workers"},
	{"packs", "system", "packs"},
	{"pack_catalogs", "system", "pack_catalogs"},
	{"policy_audit", "system", "policy_audit"},
	{"policy_snapshots", "system", "policy_snapshots"},
	{"unlisted", "system", "system-config-extra"},
	{"redaction", "gateway", "mcp.argument_redaction"},
	{"strict", "packs", "strict_mode"},
	{"case", "System", "default"},
	{"space", " org ", "default"},
}

var systemConfigReads = []struct{ name, query string }{
	{"default", ""},
	{"system", "?scope=system"},
	{"topics", "?scope_id=topics"},
	{"envelope", "?scope=system&scope_id=topics&envelope=true"},
	{"workers", "?scope=system&scope_id=workers"},
	{"packs", "?scope=system&scope_id=packs"},
	{"pack_catalogs", "?scope=system&scope_id=pack_catalogs"},
	{"policy", "?scope=system&scope_id=policy"},
	{"policy_audit", "?scope=system&scope_id=policy_audit"},
	{"policy_snapshots", "?scope=system&scope_id=policy_snapshots"},
	{"unlisted", "?scope=system&scope_id=system-config-extra"},
	{"effective", "/effective"},
	{"effective-org", "/effective?org_id={tenant}"},
}

func TestSystemConfigPlatformAuthority(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		for _, target := range systemConfigTargets {
			t.Run(method+"/"+target.name, func(t *testing.T) {
				sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
				before, mark := systemConfigState(t, sw), sw.publishMark()
				rr := sw.as(t, sw.tenantA, method, systemConfigPath, systemConfigBody(target))
				systemConfigUnchanged(t, sw, before, mark)
				assertCrossTenantRefused(t, rr, method+" "+target.name, systemConfigMarker, systemConfigTopic)
			})
			if target.name != "strict" {
				t.Run("platform/"+method+"/"+target.name, func(t *testing.T) {
					sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
					systemConfigWriteAllowed(t, sw, systemPolicyPlatform, method, target)
				})
			}
		}
	}
	systemConfigReadCases(t)
	systemConfigControlCases(t)
}

func systemConfigReadCases(t *testing.T) {
	for _, tc := range systemConfigReads {
		t.Run("read/"+tc.name, func(t *testing.T) {
			sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
			path := systemConfigPath + strings.ReplaceAll(tc.query, "{tenant}", sw.tenantA.tenantID)
			rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
			assertCrossTenantRefused(t, rr, path, systemConfigMarker, systemConfigTopic)
		})
		t.Run("platform/read/"+tc.name, func(t *testing.T) {
			sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
			path := systemConfigPath + strings.ReplaceAll(tc.query, "{tenant}", systemPolicyPlatform.tenantID)
			rr := sw.as(t, systemPolicyPlatform, http.MethodGet, path, "")
			systemPolicyOK(t, rr, path)
			if !strings.Contains(rr.Body.String(), systemConfigMarker) {
				t.Error("platform read is missing B's marker")
			}
			if (tc.name == "topics" || tc.name == "envelope") && !strings.Contains(rr.Body.String(), systemConfigTopic) {
				t.Error("platform read is missing B's topic")
			}
		})
	}
}

func systemConfigControlCases(t *testing.T) {
	t.Run("spanning", func(t *testing.T) {
		sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
		systemConfigWriteAllowed(t, sw, systemPolicySpanning, http.MethodPut, systemConfigTargets[0])
	})
	t.Run("default-fallback", func(t *testing.T) {
		platform := crossTenantSweepTenant{"default", systemPolicyPlatform.apiKey, systemPolicyPlatform.principalID}
		sw := systemConfigSweep(t, "", platform)
		systemConfigWriteAllowed(t, sw, platform, http.MethodPut, systemConfigTargets[0])
	})
	t.Run("auth-off", func(t *testing.T) {
		sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
		sw.server.auth, sw.handler = nil, sw.mux
		systemConfigWriteAllowed(t, sw, sw.tenantA, http.MethodPut, systemConfigTargets[0])
	})
	t.Run("own-org", func(t *testing.T) {
		sw := systemConfigSweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
		target := systemConfigTarget{"org", "org", sw.tenantA.tenantID}
		systemPolicyOK(t, sw.as(t, sw.tenantA, http.MethodPut, systemConfigPath, systemConfigBody(target)), "own org")
		doc, err := sw.server.configSvc.Get(context.Background(), configsvc.ScopeOrg, sw.tenantA.tenantID)
		if err != nil || doc.Data["system_config_value"] != "changed" {
			t.Fatalf("own org: doc=%+v err=%v", doc, err)
		}
	})
}

func systemConfigBody(target systemConfigTarget) string {
	if target.name == "flat" {
		return `{"system_config_value":"changed"}`
	}
	return fmt.Sprintf(`{"scope":%q,"scope_id":%q,"data":{"system_config_value":"changed"}}`, target.scope, target.id)
}

func systemConfigSweep(t *testing.T, gatewayTenant string, platform crossTenantSweepTenant) *crossTenantSweep {
	t.Helper()
	sw := systemPolicySweep(t, gatewayTenant, platform)
	// Pool seeding uses B's explicit platform grant; B's API key stays tenant-bound.
	admin := &auth.AuthContext{Role: "admin", Tenant: sw.tenantB.tenantID, AllowCrossTenant: true}
	for _, seed := range []struct{ path, body string }{
		{"/api/v1/pools/system-config-b", fmt.Sprintf(`{"description":%q}`, systemConfigMarker)},
		{"/api/v1/pools/system-config-b/topics/" + systemConfigTopic, ""},
	} {
		req := httptest.NewRequest(http.MethodPut, seed.path, strings.NewReader(seed.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", sw.tenantB.tenantID)
		rr := httptest.NewRecorder()
		sw.mux.ServeHTTP(rr, withAuth(req, admin))
		systemPolicyOK(t, rr, "seed "+seed.path)
	}
	systemPolicyOK(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/topics",
		fmt.Sprintf(`{"name":%q,"pool":"system-config-b"}`, systemConfigTopic)), "seed B topic")
	for _, target := range append(systemConfigTargets, systemConfigTarget{"policy", "system", "policy"}) {
		if target.name == "flat" || target.name == "strict" {
			continue
		}
		if err := sw.server.configSvc.SetWithRetry(context.Background(), configsvc.Scope(target.scope), target.id, 3, func(doc *configsvc.Document) error {
			doc.Data["system_config_value"] = systemConfigMarker
			return nil
		}); err != nil {
			t.Fatalf("seed %s: %v", target.name, err)
		}
	}
	if err := sw.server.jobStore.Client().Set(context.Background(), "cfg:packs:strict_mode", "true", 0).Err(); err != nil {
		t.Fatal(err)
	}
	return sw
}

type systemConfigValue struct {
	raw string
	doc *configsvc.Document
}

func systemConfigState(t *testing.T, sw *crossTenantSweep) map[string]systemConfigValue {
	t.Helper()
	state := make(map[string]systemConfigValue)
	for _, target := range append(systemConfigTargets, systemConfigTarget{"policy", "system", "policy"}) {
		key := "cfg:" + target.scope + ":" + target.id
		raw, err := sw.server.jobStore.Client().Get(context.Background(), key).Result()
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		value := systemConfigValue{raw: raw}
		if target.name != "strict" {
			value.doc, err = sw.server.configSvc.Get(context.Background(), configsvc.Scope(target.scope), target.id)
			if err != nil {
				t.Fatalf("read document %s: %v", key, err)
			}
		}
		state[key] = value
	}
	return state
}

func systemConfigUnchanged(t *testing.T, sw *crossTenantSweep, before map[string]systemConfigValue, mark int) {
	t.Helper()
	after := systemConfigState(t, sw)
	for key, value := range before {
		if value.raw != after[key].raw {
			t.Errorf("%s Redis bytes changed", key)
		}
		if !reflect.DeepEqual(value.doc, after[key].doc) {
			t.Errorf("%s document data or revision changed", key)
		}
	}
	if got := sw.publishedSince(mark); len(got) != 0 {
		t.Errorf("published %d messages, want none", len(got))
	}
}

func systemConfigWriteAllowed(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant, method string, target systemConfigTarget) {
	t.Helper()
	key := "cfg:" + target.scope + ":" + target.id
	before := systemConfigState(t, sw)[key]
	systemPolicyOK(t, sw.as(t, who, method, systemConfigPath, systemConfigBody(target)), "write "+target.name)
	after := systemConfigState(t, sw)[key]
	if before.raw == after.raw || after.doc.Revision <= before.doc.Revision || after.doc.Data["system_config_value"] != "changed" {
		t.Errorf("%s write did not update bytes, revision and data", key)
	}
}
