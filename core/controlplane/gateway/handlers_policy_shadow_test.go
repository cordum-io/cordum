package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/policyshadow"
)

// roleEnforcingAuth is the test auth that turns requireRole into a
// real gate: if the request's AuthContext has a role not in the
// allowed list, it returns an error (which handlers map to 403).
type roleEnforcingAuth struct{}

func (roleEnforcingAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	if ac := auth.FromRequest(r); ac != nil {
		return ac, nil
	}
	return nil, errors.New("unauthorized")
}
func (roleEnforcingAuth) AuthenticateGRPC(context.Context) (*auth.AuthContext, error) {
	return &auth.AuthContext{Tenant: "default"}, nil
}
func (roleEnforcingAuth) RequireRole(r *http.Request, roles ...string) error {
	ac := auth.FromRequest(r)
	if ac == nil {
		return errors.New("auth required")
	}
	for _, want := range roles {
		if strings.EqualFold(ac.Role, want) {
			return nil
		}
	}
	return errors.New("role denied")
}
func (roleEnforcingAuth) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	if strings.TrimSpace(requested) != "" {
		return requested, nil
	}
	return fallback, nil
}
func (roleEnforcingAuth) RequireTenantAccess(*http.Request, string) error { return nil }
func (roleEnforcingAuth) ResolvePrincipal(_ *http.Request, requested string) (string, error) {
	return requested, nil
}

const shadowTestValidYAML = "version: \"1\"\nrules: []\n"
const shadowTestReplacementYAML = "version: \"2\"\nrules: []\n"

// seedActiveBundle writes a minimal active bundle so the shadow
// handlers have something to target. Shadow activation itself doesn't
// require an active bundle today, but the DoD phrases the feature in
// terms of "shadow for bundle X" so it's worth anchoring the tests to
// a real bundle.
func seedActiveBundle(t *testing.T, s *server, bundleID string) {
	t.Helper()
	// Policy signing is strict by default; go around the PUT handler and
	// seed configsvc directly so tests don't need Ed25519 keys.
	doc, err := getConfigDoc(context.Background(), s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if err != nil || doc == nil {
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(packs.PolicyConfigScope),
			ScopeID: packs.PolicyConfigID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	bundles, _ := doc.Data[packs.PolicyConfigKey].(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	bundles[bundleID] = map[string]any{
		"content":    shadowTestValidYAML,
		"enabled":    true,
		"created_at": "2026-04-18T00:00:00Z",
		"updated_at": "2026-04-18T00:00:00Z",
	}
	doc.Data[packs.PolicyConfigKey] = bundles
	if err := s.configSvc.Set(context.Background(), doc); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
}

// shadowPostBody returns the JSON body the POST handler expects with
// YAML content inlined correctly.
func shadowPostBody(content string) string {
	out, _ := json.Marshal(map[string]any{"content": content})
	return string(out)
}

func shadowRequest(method, bundleID, tenant, body string) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/policy/bundles/"+bundleID+"/shadow", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", tenant)
	authCtx := &auth.AuthContext{Role: "admin", Tenant: tenant}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	req.SetPathValue("id", bundleID)
	return req
}

func shadowRequestRole(method, bundleID, tenant, role, body string) *http.Request {
	req := shadowRequest(method, bundleID, tenant, body)
	authCtx := &auth.AuthContext{Role: role, Tenant: tenant}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
}

func TestPolicyShadow_ActivateGetDelete(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	sender := &testAuditSender{}
	s.auditExporter = sender

	bundleID := "secops/shadow-a"
	seedActiveBundle(t, s, bundleID)

	// Activate.
	postRec := httptest.NewRecorder()
	s.handlePutPolicyShadow(postRec, shadowRequest(http.MethodPost, bundleID, "default", shadowPostBody(shadowTestValidYAML)))
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200: %s", postRec.Code, postRec.Body.String())
	}
	var activated policyshadow.ShadowPolicy
	if err := json.Unmarshal(postRec.Body.Bytes(), &activated); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if activated.ShadowBundleID == "" {
		t.Fatalf("expected ShadowBundleID populated: %+v", activated)
	}

	// GET returns it.
	getRec := httptest.NewRecorder()
	s.handleGetPolicyShadow(getRec, shadowRequest(http.MethodGet, bundleID, "default", ""))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", getRec.Code, getRec.Body.String())
	}
	var fetched policyshadow.ShadowPolicy
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if fetched.ShadowBundleID != activated.ShadowBundleID {
		t.Fatalf("GET returned mismatched ShadowBundleID: got %q want %q", fetched.ShadowBundleID, activated.ShadowBundleID)
	}

	// DELETE 204.
	deleteRec := httptest.NewRecorder()
	s.handleDeletePolicyShadow(deleteRec, shadowRequest(http.MethodDelete, bundleID, "default", ""))
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204: %s", deleteRec.Code, deleteRec.Body.String())
	}

	// Subsequent GET 404.
	get2 := httptest.NewRecorder()
	s.handleGetPolicyShadow(get2, shadowRequest(http.MethodGet, bundleID, "default", ""))
	if get2.Code != http.StatusNotFound {
		t.Fatalf("subsequent GET status = %d, want 404", get2.Code)
	}
}

func TestPolicyShadow_ReplaceOnSecondActivate(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	sender := &testAuditSender{}
	s.auditExporter = sender

	bundleID := "secops/shadow-replace"
	seedActiveBundle(t, s, bundleID)

	// First activation.
	rec := httptest.NewRecorder()
	s.handlePutPolicyShadow(rec, shadowRequest(http.MethodPost, bundleID, "default", shadowPostBody(shadowTestValidYAML)))
	if rec.Code != http.StatusOK {
		t.Fatalf("first POST: %d %s", rec.Code, rec.Body.String())
	}
	var first policyshadow.ShadowPolicy
	_ = json.Unmarshal(rec.Body.Bytes(), &first)

	// Second activation replaces.
	rec2 := httptest.NewRecorder()
	s.handlePutPolicyShadow(rec2, shadowRequest(http.MethodPost, bundleID, "default", shadowPostBody(shadowTestReplacementYAML)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second POST: %d %s", rec2.Code, rec2.Body.String())
	}
	var second policyshadow.ShadowPolicy
	_ = json.Unmarshal(rec2.Body.Bytes(), &second)

	if second.ShadowBundleID == first.ShadowBundleID {
		t.Fatalf("expected new ShadowBundleID on replace, got same: %q", second.ShadowBundleID)
	}
	if !strings.Contains(second.Content, `version: "2"`) {
		t.Fatalf("replacement content not persisted: %q", second.Content)
	}

	// Confirm only one shadow remains (via store list).
	list, err := s.policyShadowStore.List(context.Background(), "default")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	count := 0
	for _, sp := range list {
		if sp.BundleID == bundleID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("one-per-bundle violated: got %d shadows for %s", count, bundleID)
	}
}

func TestPolicyShadow_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = roleEnforcingAuth{}

	bundleID := "secops/shadow-forbidden"
	rec := httptest.NewRecorder()
	s.handlePutPolicyShadow(rec, shadowRequestRole(http.MethodPost, bundleID, "default", "viewer", shadowPostBody(shadowTestValidYAML)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-admin", rec.Code)
	}
}

func TestPolicyShadow_CrossTenantIsolation(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	bundleID := "secops/shadow-tenant"

	// Tenant A activates.
	rec := httptest.NewRecorder()
	s.handlePutPolicyShadow(rec, shadowRequest(http.MethodPost, bundleID, "tenant-a", shadowPostBody(shadowTestValidYAML)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST as tenant-a: %d %s", rec.Code, rec.Body.String())
	}

	// Tenant B must NOT see tenant-a's shadow via its own GET.
	rec2 := httptest.NewRecorder()
	s.handleGetPolicyShadow(rec2, shadowRequest(http.MethodGet, bundleID, "tenant-b", ""))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant GET status = %d, want 404", rec2.Code)
	}
}

func TestPolicyShadow_InvalidYAMLReturns400(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)

	bundleID := "secops/shadow-invalid"
	invalid := shadowPostBody("::: not: valid: yaml :::")
	rec := httptest.NewRecorder()
	s.handlePutPolicyShadow(rec, shadowRequest(http.MethodPost, bundleID, "default", invalid))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid policy content") {
		t.Fatalf("expected 'invalid policy content' in body: %s", rec.Body.String())
	}
}

func TestPolicyShadow_EmptyContentReturns400(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)

	bundleID := "secops/shadow-empty"
	rec := httptest.NewRecorder()
	s.handlePutPolicyShadow(rec, shadowRequest(http.MethodPost, bundleID, "default", shadowPostBody("")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestPolicyShadow_AuditEventsEmitted(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	sender := &testAuditSender{}
	s.auditExporter = sender

	bundleID := "secops/shadow-audit"

	// Activate.
	postRec := httptest.NewRecorder()
	s.handlePutPolicyShadow(postRec, shadowRequest(http.MethodPost, bundleID, "default", shadowPostBody(shadowTestValidYAML)))
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST: %d %s", postRec.Code, postRec.Body.String())
	}
	var activated policyshadow.ShadowPolicy
	_ = json.Unmarshal(postRec.Body.Bytes(), &activated)

	// Deactivate.
	delRec := httptest.NewRecorder()
	s.handleDeletePolicyShadow(delRec, shadowRequest(http.MethodDelete, bundleID, "default", ""))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", delRec.Code, delRec.Body.String())
	}

	sawActivate, sawDeactivate := false, false
	for i := 0; i < sender.Len(); i++ {
		ev := sender.Get(i)
		if ev.EventType != audit.EventPolicyChange {
			continue
		}
		if ev.Extra["shadow_bundle_id"] != activated.ShadowBundleID {
			continue
		}
		if ev.Extra["bundle_id"] != bundleID {
			continue
		}
		switch ev.Action {
		case "shadow_activate":
			sawActivate = true
		case "shadow_deactivate":
			sawDeactivate = true
		}
	}
	if !sawActivate {
		t.Errorf("missing shadow_activate SIEM event with matching shadow_bundle_id; got %d events", sender.Len())
	}
	if !sawDeactivate {
		t.Errorf("missing shadow_deactivate SIEM event with matching shadow_bundle_id; got %d events", sender.Len())
	}
}

func TestPolicyShadow_BundleDetailEmbedsShadowSummary(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	bundleID := "secops/shadow-detail"

	seedActiveBundle(t, s, bundleID)

	// Case A: no shadow → Shadow field absent.
	getReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/"+bundleID, nil))
	getReq.SetPathValue("id", bundleID)
	rec := httptest.NewRecorder()
	s.handleGetPolicyBundle(rec, getReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET (no shadow): %d %s", rec.Code, rec.Body.String())
	}
	var noShadowDetail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &noShadowDetail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, ok := noShadowDetail["shadow"]; ok && v != nil {
		t.Fatalf("expected Shadow absent / nil when no shadow: got %v", v)
	}

	// Case B: activate shadow → Shadow field present with ShadowBundleID.
	activateRec := httptest.NewRecorder()
	s.handlePutPolicyShadow(activateRec, shadowRequest(http.MethodPost, bundleID, "default", shadowPostBody(shadowTestValidYAML)))
	if activateRec.Code != http.StatusOK {
		t.Fatalf("activate shadow: %d %s", activateRec.Code, activateRec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	getReq2 := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/"+bundleID, nil))
	getReq2.SetPathValue("id", bundleID)
	s.handleGetPolicyBundle(rec2, getReq2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET (shadow present): %d %s", rec2.Code, rec2.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	shadow, ok := detail["shadow"].(map[string]any)
	if !ok {
		t.Fatalf("expected embedded shadow summary map, got %#v", detail["shadow"])
	}
	if id, _ := shadow["shadow_bundle_id"].(string); id == "" {
		t.Fatalf("embedded shadow missing shadow_bundle_id: %#v", shadow)
	}
	// Summary must not leak Content.
	if _, hasContent := shadow["content"]; hasContent {
		t.Fatalf("embedded shadow summary must not carry content: %#v", shadow)
	}
}
