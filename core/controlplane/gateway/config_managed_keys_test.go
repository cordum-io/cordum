package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
)

func managedCfgProfiles(t *testing.T, run func(*testing.T, *server, ed25519.PrivateKey)) {
	t.Helper()
	for _, profile := range []string{"production", "development"} {
		t.Run(profile, func(t *testing.T) {
			key := setPackSignatureProfile(t, profile)
			s, _, _ := newTestGateway(t)
			packMatrixOK(t, s.configSvc.Set(context.Background(), &configsvc.Document{
				Scope: "system", ScopeID: "default", Data: map[string]any{"pools": map[string]any{}},
			}))
			run(t, s, key)
		})
	}
}

func managedCfgPayload(t *testing.T, id string, data map[string]any) []byte {
	t.Helper()
	return packMatrixJSON(t, map[string]any{"scope": "system", "scope_id": id, "data": data})
}

func managedCfgRefuse(t *testing.T, s *server, id string, drive func() *httptest.ResponseRecorder) {
	t.Helper()
	before := snapshotPackKeyspace(t, s)
	auditBefore, err := s.loadPolicyAudit(context.Background())
	packMatrixOK(t, err)
	resp := drive()
	var body struct{ Code, Error string }
	if resp.Body.Len() != 0 {
		packMatrixOK(t, json.Unmarshal(resp.Body.Bytes(), &body))
	}
	if resp.Code != 400 || body.Code != "CONFIG_KEY_FORBIDDEN" || !strings.HasPrefix(body.Error, "config.managed_key: ") {
		t.Errorf("refusal: status=%d body=%+v", resp.Code, body)
	}
	after := snapshotPackKeyspace(t, s)
	if diff := diffPackKeyspace(before, after); !reflect.DeepEqual(diff, []string{"cfg:system:policy_audit"}) {
		t.Errorf("unexpected state change: %v", diff)
	}
	for _, key := range []string{"cfg:system:policy", "cfg:system:packs"} {
		old, existed := before[key]
		current, exists := after[key]
		if existed != exists || old != current {
			t.Errorf("raw document changed: %s", key)
		}
	}
	audit, err := s.loadPolicyAudit(context.Background())
	packMatrixOK(t, err)
	if len(audit) != len(auditBefore)+1 || audit[0].Action != "config.set.rejected" || audit[0].ResourceID != "system/"+id {
		t.Errorf("unexpected audit: %+v", audit)
	}
	if _, installed := before["cfg:system:packs"]; !installed {
		assertPackAbsent(t, s)
	}
}

func managedCfgWriteRefused(t *testing.T, s *server, id string, data map[string]any) {
	t.Helper()
	managedCfgRefuse(t, s, id, func() *httptest.ResponseRecorder {
		return packMatrixRequest(s.handleSetConfig, "POST", "/api/v1/config", managedCfgPayload(t, id, data))
	})
}

func managedCfgEntry(id string) map[string]any {
	if id == "packs" {
		return map[string]any{"installed": map[string]any{matrixPackID: packs.PackRecord{
			ID: matrixPackID, Verification: &packs.PackRecordVerification{Signed: true, KID: "matrix-pub"},
		}}}
	}
	return map[string]any{"bundles": map[string]any{matrixPackID + "/safety": map[string]string{
		"content": "rules: []", "version": "1.0.0", "sha256": "claimed", "installed_at": "2026-01-01T00:00:00Z",
	}}}
}

func managedCfgInstall(t *testing.T, s *server, key ed25519.PrivateKey) {
	t.Helper()
	resp := uploadPackSignatureBundle(t, s, buildPackSignatureBundle(t, "signed", key))
	if resp.Code != 200 {
		t.Fatalf("install: status=%d body=%s", resp.Code, resp.Body.String())
	}
	assertPackFragmentSignatureVerifies(t, s, matrixPackID, "safety")
}

func TestManagedConfigKeys_PolicyBundleWriteRefusedWithoutStateChange(t *testing.T) {
	managedCfgProfiles(t, func(t *testing.T, s *server, _ ed25519.PrivateKey) {
		managedCfgWriteRefused(t, s, "policy", managedCfgEntry("policy"))
	})
}

func TestManagedConfigKeys_PackRegistryWriteRefusedWithoutStateChange(t *testing.T) {
	managedCfgProfiles(t, func(t *testing.T, s *server, _ ed25519.PrivateKey) {
		managedCfgWriteRefused(t, s, "packs", managedCfgEntry("packs"))
	})
}

func TestManagedConfigKeys_PutRouteRefusedLikePost(t *testing.T) {
	managedCfgProfiles(t, func(t *testing.T, s *server, _ ed25519.PrivateKey) {
		mux := http.NewServeMux()
		packMatrixOK(t, s.registerRoutes(mux))
		for _, method := range []string{"POST", "PUT"} {
			for _, id := range []string{"policy", "packs"} {
				t.Run(method+"/"+id, func(t *testing.T) {
					managedCfgRefuse(t, s, id, func() *httptest.ResponseRecorder {
						return packMatrixRequest(mux.ServeHTTP, method, "/api/v1/config", managedCfgPayload(t, id, managedCfgEntry(id)))
					})
				})
			}
		}
	})
}

func TestManagedConfigKeys_InstalledPackChangeOrRemovalRefused(t *testing.T) {
	managedCfgProfiles(t, func(t *testing.T, s *server, key ed25519.PrivateKey) {
		managedCfgInstall(t, s, key)
		for _, change := range []string{"content", "signature", "fragment", "null", "verification", "registry"} {
			t.Run(change, func(t *testing.T) {
				id, data := managedCfgChanged(t, s, change)
				managedCfgWriteRefused(t, s, id, data)
				assertPackFragmentSignatureVerifies(t, s, matrixPackID, "safety")
			})
		}
	})
}

func managedCfgChanged(t *testing.T, s *server, change string) (string, map[string]any) {
	t.Helper()
	id, field := "policy", "bundles"
	if change == "verification" || change == "registry" {
		id, field = "packs", "installed"
	}
	doc, err := s.configSvc.Get(context.Background(), "system", id)
	packMatrixOK(t, err)
	entries := doc.Data[field].(map[string]any)
	switch change {
	case "content":
		entries[matrixPackID+"/safety"].(map[string]any)["content"] = "rules: []"
	case "signature":
		delete(entries[matrixPackID+"/safety"].(map[string]any), "_signature")
	case "fragment":
		delete(entries, matrixPackID+"/safety")
	case "null":
		doc.Data[field] = nil
	case "verification":
		entries[matrixPackID].(map[string]any)["verification"] = map[string]any{"signed": false}
	case "registry":
		delete(entries, matrixPackID)
	default:
		t.Fatalf("unexpected change: %s", change)
	}
	return id, doc.Data
}

func TestManagedConfigKeys_StaleEchoAfterServerInstallRefused(t *testing.T) {
	managedCfgProfiles(t, func(t *testing.T, s *server, key ed25519.PrivateKey) {
		packMatrixOK(t, s.configSvc.Set(context.Background(), &configsvc.Document{
			Scope: "system", ScopeID: "policy", Data: map[string]any{"bundles": map[string]any{}},
		}))
		stale := packMatrixRequest(s.handleGetConfig, "GET", "/api/v1/config?scope=system&scope_id=policy&envelope=true", nil)
		if stale.Code != 200 {
			t.Fatalf("read: status=%d", stale.Code)
		}
		managedCfgInstall(t, s, key)
		managedCfgRefuse(t, s, "policy", func() *httptest.ResponseRecorder {
			return packMatrixRequest(s.handleSetConfig, "POST", "/api/v1/config", stale.Body.Bytes())
		})
		assertPackFragmentSignatureVerifies(t, s, matrixPackID, "safety")
	})
}

func TestManagedConfigKeys_UnchangedEchoAndOtherDocumentsAllowed(t *testing.T) {
	managedCfgProfiles(t, func(t *testing.T, s *server, key ed25519.PrivateKey) {
		managedCfgInstall(t, s, key)
		for _, id := range []string{"policy", "packs"} {
			doc, err := s.configSvc.Get(context.Background(), "system", id)
			packMatrixOK(t, err)
			before := packMatrixJSON(t, doc.Data)
			doc.Data["extra"] = "kept"
			managedCfgAllow(t, s, managedCfgPayload(t, id, doc.Data))
			stored, err := s.configSvc.Get(context.Background(), "system", id)
			packMatrixOK(t, err)
			if stored.Data["extra"] != "kept" {
				t.Error("extra key not stored")
			}
			delete(stored.Data, "extra")
			if string(before) != string(packMatrixJSON(t, stored.Data)) {
				t.Error("echo changed managed state")
			}
			managedCfgAllow(t, s, managedCfgPayload(t, id, map[string]any{"another": true}))
		}
		managedCfgAllow(t, s, []byte(`{"dashboard_setting":true}`))
		for _, scope := range []string{"org", "System"} {
			managedCfgAllow(t, s, packMatrixJSON(t, map[string]any{"scope": scope, "scope_id": "default", "data": managedCfgEntry("policy")}))
		}
		managedCfgAllow(t, s, managedCfgPayload(t, " policy", managedCfgEntry("policy")))
		assertPackFragmentSignatureVerifies(t, s, matrixPackID, "safety")
	})
}

func managedCfgAllow(t *testing.T, s *server, body []byte) {
	t.Helper()
	resp := packMatrixRequest(s.handleSetConfig, "POST", "/api/v1/config", body)
	if resp.Code != 204 {
		t.Fatalf("write: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestSameConfigJSON(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"key order and number types", map[string]any{"a": 1, "b": true}, json.RawMessage(`{"b":true,"a":1.0}`), true},
		{"different numbers", 1, 2, false},
		{"null", nil, json.RawMessage(`null`), true},
		{"empty is not null", map[string]any{}, nil, false},
		{"array order", []int{1, 2}, []int{2, 1}, false},
		{"unreadable left", func() {}, nil, false},
		{"unreadable right", nil, func() {}, false},
		{"invalid JSON", json.RawMessage(`{`), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameConfigJSON(tc.a, tc.b); got != tc.want {
				t.Errorf("sameConfigJSON = %v, want %v", got, tc.want)
			}
		})
	}
}
