package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

type scimUserListResponse struct {
	Schemas      []string           `json:"schemas"`
	TotalResults int                `json:"totalResults"`
	StartIndex   int                `json:"startIndex"`
	ItemsPerPage int                `json:"itemsPerPage"`
	Resources    []scimUserResource `json:"Resources"`
}

type scimGroupListResponse struct {
	Schemas      []string            `json:"schemas"`
	TotalResults int                 `json:"totalResults"`
	StartIndex   int                 `json:"startIndex"`
	ItemsPerPage int                 `json:"itemsPerPage"`
	Resources    []scimGroupResource `json:"Resources"`
}

func newTestSCIMService(t *testing.T, resolver *licensing.EntitlementResolver) (*SCIMService, *RedisUserStore, *http.ServeMux, string) {
	t.Helper()

	store, _ := newTestUserStore(t)
	token := "scim-test-token"
	t.Setenv(scimBearerTokenEnv, token)
	t.Setenv("CORDUM_API_BASE_URL", "http://localhost:8081")

	service, err := NewSCIMService(store, "default", resolver)
	if err != nil {
		t.Fatalf("NewSCIMService: %v", err)
	}
	service.now = func() time.Time {
		return time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	}

	mux := http.NewServeMux()
	service.RegisterRoutes(mux, nil)
	return service, store, mux, token
}

func scimRequest(t *testing.T, mux *http.ServeMux, method, target, token string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	switch typed := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(typed))
	case []byte:
		reader = bytes.NewReader(typed)
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			t.Fatalf("json.Marshal(%T): %v", body, err)
		}
		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeSCIMJSON[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()

	var out T
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("json.Decode: %v body=%s", err, rr.Body.String())
	}
	return out
}

func createSCIMUserViaHTTP(t *testing.T, mux *http.ServeMux, token, username, displayName, role string) scimUserResource {
	t.Helper()

	body := scimUserResource{
		Schemas:     []string{scimUserSchema},
		UserName:    username,
		DisplayName: displayName,
		Name: &scimName{
			GivenName:  strings.Split(displayName, " ")[0],
			FamilyName: strings.TrimSpace(strings.TrimPrefix(displayName, strings.Split(displayName, " ")[0])),
		},
		Emails: []scimMultiValued{{Value: username, Type: "work", Primary: true}},
		Roles:  []scimMultiValued{{Value: role, Display: role}},
	}

	rr := scimRequest(t, mux, http.MethodPost, SCIMUsersPath, token, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /Users status=%d body=%s", rr.Code, rr.Body.String())
	}
	return decodeSCIMJSON[scimUserResource](t, rr)
}

func TestSCIMDiscoveryEndpoints(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SCIM = true
	})
	_, _, mux, token := newTestSCIMService(t, resolver)

	cases := []struct {
		name   string
		target string
		check  func(t *testing.T, payload map[string]any)
	}{
		{
			name:   "service provider config",
			target: SCIMServiceProviderConfigPath,
			check: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if got := payload["schemas"]; len(got.([]any)) == 0 || got.([]any)[0] != scimSPConfigSchema {
					t.Fatalf("schemas = %#v", got)
				}
				if patch := payload["patch"].(map[string]any); patch["supported"] != true {
					t.Fatalf("patch.supported = %#v", patch["supported"])
				}
				schemes, ok := payload["authenticationSchemes"].([]any)
				if !ok || len(schemes) != 1 {
					t.Fatalf("authenticationSchemes = %#v", payload["authenticationSchemes"])
				}
			},
		},
		{
			name:   "schemas",
			target: SCIMSchemasPath,
			check: func(t *testing.T, payload map[string]any) {
				t.Helper()
				resources, ok := payload["Resources"].([]any)
				if !ok || len(resources) != 2 {
					t.Fatalf("Resources = %#v", payload["Resources"])
				}
				first := resources[0].(map[string]any)
				second := resources[1].(map[string]any)
				if first["id"] != scimUserSchema || second["id"] != scimGroupSchema {
					t.Fatalf("schema ids = %#v %#v", first["id"], second["id"])
				}
			},
		},
		{
			name:   "resource types",
			target: SCIMResourceTypesPath,
			check: func(t *testing.T, payload map[string]any) {
				t.Helper()
				resources, ok := payload["Resources"].([]any)
				if !ok || len(resources) != 2 {
					t.Fatalf("Resources = %#v", payload["Resources"])
				}
				first := resources[0].(map[string]any)
				second := resources[1].(map[string]any)
				if first["endpoint"] != "Users" || second["endpoint"] != "Groups" {
					t.Fatalf("resource endpoints = %#v %#v", first["endpoint"], second["endpoint"])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := scimRequest(t, mux, http.MethodGet, tc.target, token, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/scim+json") {
				t.Fatalf("Content-Type=%q", got)
			}
			payload := decodeSCIMJSON[map[string]any](t, rr)
			tc.check(t, payload)
		})
	}
}

func TestSCIMUsersCRUDAndSoftDelete(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SCIM = true
	})
	_, store, mux, token := newTestSCIMService(t, resolver)

	createBody := scimUserResource{
		Schemas:     []string{scimUserSchema},
		UserName:    "john@example.com",
		ExternalID:  "idp-john",
		DisplayName: "John Doe",
		Name: &scimName{
			GivenName:  "John",
			FamilyName: "Doe",
		},
		Emails: []scimMultiValued{{Value: "john@example.com", Type: "work", Primary: true}},
		Roles:  []scimMultiValued{{Value: "viewer", Display: "viewer"}},
	}

	createRR := scimRequest(t, mux, http.MethodPost, SCIMUsersPath, token, createBody)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("POST /Users status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	if got := createRR.Header().Get("Location"); !strings.HasSuffix(got, "/Users/") && !strings.Contains(got, "/Users/") {
		t.Fatalf("Location=%q", got)
	}
	created := decodeSCIMJSON[scimUserResource](t, createRR)
	if created.UserName != "john@example.com" {
		t.Fatalf("UserName=%q", created.UserName)
	}
	if created.Active == nil || !*created.Active {
		t.Fatalf("created.Active=%v", created.Active)
	}

	stored, err := store.GetByUsername(context.Background(), "john@example.com", "default")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if stored.Email != "john@example.com" || stored.DisplayName != "John Doe" || stored.Role != "viewer" {
		t.Fatalf("stored user = %+v", stored)
	}

	getRR := scimRequest(t, mux, http.MethodGet, SCIMUsersPath+"/"+created.ID, token, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /Users/{id} status=%d body=%s", getRR.Code, getRR.Body.String())
	}
	gotUser := decodeSCIMJSON[scimUserResource](t, getRR)
	if gotUser.ExternalID != "idp-john" {
		t.Fatalf("ExternalID=%q", gotUser.ExternalID)
	}

	listRR := scimRequest(t, mux, http.MethodGet, SCIMUsersPath, token, nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("GET /Users status=%d body=%s", listRR.Code, listRR.Body.String())
	}
	list := decodeSCIMJSON[scimUserListResponse](t, listRR)
	if list.TotalResults != 1 || len(list.Resources) != 1 {
		t.Fatalf("list=%+v", list)
	}

	patchBody := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOperation{
			{Op: "Replace", Path: "displayName", Value: "John R. Doe"},
			{Op: "Replace", Path: "emails", Value: "john.renamed@example.com"},
			{Op: "Replace", Path: "roles", Value: []map[string]any{{"value": "admin"}}},
		},
	}

	patchRR := scimRequest(t, mux, http.MethodPatch, SCIMUsersPath+"/"+created.ID, token, patchBody)
	if patchRR.Code != http.StatusOK {
		t.Fatalf("PATCH /Users/{id} status=%d body=%s", patchRR.Code, patchRR.Body.String())
	}
	patched := decodeSCIMJSON[scimUserResource](t, patchRR)
	if patched.DisplayName != "John R. Doe" {
		t.Fatalf("DisplayName=%q", patched.DisplayName)
	}
	if got := firstSCIMEmail(patched.Emails); got != "john.renamed@example.com" {
		t.Fatalf("patched email=%q", got)
	}
	if got := firstSCIMRole(patched.Roles); got != "admin" {
		t.Fatalf("patched role=%q", got)
	}

	updated, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID after patch: %v", err)
	}
	if updated.Email != "john.renamed@example.com" || updated.Role != "admin" {
		t.Fatalf("updated user = %+v", updated)
	}

	deleteRR := scimRequest(t, mux, http.MethodDelete, SCIMUsersPath+"/"+created.ID, token, nil)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("DELETE /Users/{id} status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	deleted, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID after delete: %v", err)
	}
	if !deleted.Disabled {
		t.Fatalf("expected disabled user after delete, got %+v", deleted)
	}

	getDeletedRR := scimRequest(t, mux, http.MethodGet, SCIMUsersPath+"/"+created.ID, token, nil)
	if getDeletedRR.Code != http.StatusOK {
		t.Fatalf("GET deleted /Users/{id} status=%d body=%s", getDeletedRR.Code, getDeletedRR.Body.String())
	}
	deletedResource := decodeSCIMJSON[scimUserResource](t, getDeletedRR)
	if deletedResource.Active == nil || *deletedResource.Active {
		t.Fatalf("deleted resource active=%v", deletedResource.Active)
	}
}

func TestSCIMGroupsCRUDAndMembership(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SCIM = true
	})
	_, store, mux, token := newTestSCIMService(t, resolver)

	user := createSCIMUserViaHTTP(t, mux, token, "group-user@example.com", "Group User", "viewer")

	createGroupBody := scimGroupResource{
		Schemas:     []string{scimGroupSchema},
		DisplayName: "admin",
		ExternalID:  "group-ext-1",
		Members:     []scimMultiValued{{Value: user.ID}},
	}

	createRR := scimRequest(t, mux, http.MethodPost, SCIMGroupsPath, token, createGroupBody)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("POST /Groups status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	group := decodeSCIMJSON[scimGroupResource](t, createRR)
	if group.DisplayName != "admin" || len(group.Members) != 1 || group.Members[0].Value != user.ID {
		t.Fatalf("created group=%+v", group)
	}

	member, err := store.GetByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetByID after group create: %v", err)
	}
	if member.Role != "admin" {
		t.Fatalf("member role=%q", member.Role)
	}

	getRR := scimRequest(t, mux, http.MethodGet, SCIMGroupsPath+"/"+group.ID, token, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /Groups/{id} status=%d body=%s", getRR.Code, getRR.Body.String())
	}

	listRR := scimRequest(t, mux, http.MethodGet, SCIMGroupsPath, token, nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("GET /Groups status=%d body=%s", listRR.Code, listRR.Body.String())
	}
	list := decodeSCIMJSON[scimGroupListResponse](t, listRR)
	if list.TotalResults != 1 || len(list.Resources) != 1 {
		t.Fatalf("group list=%+v", list)
	}

	patchBody := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOperation{
			{Op: "Remove", Path: "members"},
		},
	}

	patchRR := scimRequest(t, mux, http.MethodPatch, SCIMGroupsPath+"/"+group.ID, token, patchBody)
	if patchRR.Code != http.StatusOK {
		t.Fatalf("PATCH /Groups/{id} status=%d body=%s", patchRR.Code, patchRR.Body.String())
	}
	patched := decodeSCIMJSON[scimGroupResource](t, patchRR)
	if len(patched.Members) != 0 {
		t.Fatalf("patched members=%+v", patched.Members)
	}

	cleared, err := store.GetByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetByID after group patch: %v", err)
	}
	if cleared.Role != "viewer" {
		t.Fatalf("expected viewer after clearing membership, got %q", cleared.Role)
	}

	deleteRR := scimRequest(t, mux, http.MethodDelete, SCIMGroupsPath+"/"+group.ID, token, nil)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("DELETE /Groups/{id} status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	postDeleteListRR := scimRequest(t, mux, http.MethodGet, SCIMGroupsPath, token, nil)
	if postDeleteListRR.Code != http.StatusOK {
		t.Fatalf("GET /Groups after delete status=%d body=%s", postDeleteListRR.Code, postDeleteListRR.Body.String())
	}
	postDeleteList := decodeSCIMJSON[scimGroupListResponse](t, postDeleteListRR)
	if postDeleteList.TotalResults != 0 || len(postDeleteList.Resources) != 0 {
		t.Fatalf("post delete list=%+v", postDeleteList)
	}
}

func TestSCIMUsersFilterAndPagination(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SCIM = true
	})
	_, _, mux, token := newTestSCIMService(t, resolver)

	createSCIMUserViaHTTP(t, mux, token, "charlie@example.com", "Charlie Example", "viewer")
	createSCIMUserViaHTTP(t, mux, token, "alpha@example.com", "Alpha Example", "viewer")
	createSCIMUserViaHTTP(t, mux, token, "bravo@example.com", "Bravo Example", "viewer")

	filter := url.QueryEscape(`userName eq "bravo@example.com"`)
	filterRR := scimRequest(t, mux, http.MethodGet, SCIMUsersPath+"?filter="+filter, token, nil)
	if filterRR.Code != http.StatusOK {
		t.Fatalf("GET /Users filter status=%d body=%s", filterRR.Code, filterRR.Body.String())
	}
	filtered := decodeSCIMJSON[scimUserListResponse](t, filterRR)
	if filtered.TotalResults != 1 || len(filtered.Resources) != 1 || filtered.Resources[0].UserName != "bravo@example.com" {
		t.Fatalf("filtered=%+v", filtered)
	}

	pageRR := scimRequest(t, mux, http.MethodGet, SCIMUsersPath+"?sortBy=userName&sortOrder=ascending&startIndex=2&count=1", token, nil)
	if pageRR.Code != http.StatusOK {
		t.Fatalf("GET /Users pagination status=%d body=%s", pageRR.Code, pageRR.Body.String())
	}
	page := decodeSCIMJSON[scimUserListResponse](t, pageRR)
	if page.TotalResults != 3 || page.StartIndex != 2 || page.ItemsPerPage != 1 {
		t.Fatalf("page=%+v", page)
	}
	if len(page.Resources) != 1 || page.Resources[0].UserName != "bravo@example.com" {
		t.Fatalf("pagination resources=%+v", page.Resources)
	}
}

func TestSCIMHandlers_EntitlementDisabled(t *testing.T) {
	_, _, mux, token := newTestSCIMService(t, nil)

	cases := []struct {
		name   string
		method string
		target string
	}{
		{name: "service provider config", method: http.MethodGet, target: SCIMServiceProviderConfigPath},
		{name: "schemas", method: http.MethodGet, target: SCIMSchemasPath},
		{name: "resource types", method: http.MethodGet, target: SCIMResourceTypesPath},
		{name: "list users", method: http.MethodGet, target: SCIMUsersPath},
		{name: "create user", method: http.MethodPost, target: SCIMUsersPath},
		{name: "get user", method: http.MethodGet, target: SCIMUsersPath + "/user-1"},
		{name: "replace user", method: http.MethodPut, target: SCIMUsersPath + "/user-1"},
		{name: "patch user", method: http.MethodPatch, target: SCIMUsersPath + "/user-1"},
		{name: "delete user", method: http.MethodDelete, target: SCIMUsersPath + "/user-1"},
		{name: "list groups", method: http.MethodGet, target: SCIMGroupsPath},
		{name: "create group", method: http.MethodPost, target: SCIMGroupsPath},
		{name: "get group", method: http.MethodGet, target: SCIMGroupsPath + "/group-1"},
		{name: "replace group", method: http.MethodPut, target: SCIMGroupsPath + "/group-1"},
		{name: "patch group", method: http.MethodPatch, target: SCIMGroupsPath + "/group-1"},
		{name: "delete group", method: http.MethodDelete, target: SCIMGroupsPath + "/group-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := scimRequest(t, mux, tc.method, tc.target, token, nil)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") {
				t.Fatalf("expected tier_limit_exceeded body, got %s", rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "upgrade_url") {
				t.Fatalf("expected upgrade_url body, got %s", rr.Body.String())
			}
		})
	}
}

func TestSCIMHandlers_WrongBearerTokenUnauthorized(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SCIM = true
	})
	_, _, mux, _ := newTestSCIMService(t, resolver)

	for _, target := range []string{SCIMServiceProviderConfigPath, SCIMUsersPath, SCIMGroupsPath} {
		t.Run(target, func(t *testing.T) {
			rr := scimRequest(t, mux, http.MethodGet, target, "wrong-token", nil)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "invalid bearer token") {
				t.Fatalf("expected invalid bearer token body, got %s", rr.Body.String())
			}
		})
	}
}
