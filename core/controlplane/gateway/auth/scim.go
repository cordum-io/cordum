package auth

import (
	"context"
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/licensing"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	SCIMBasePath                  = "/api/v1/scim/v2"
	SCIMUsersPath                 = SCIMBasePath + "/Users"
	SCIMGroupsPath                = SCIMBasePath + "/Groups"
	SCIMServiceProviderConfigPath = SCIMBasePath + "/ServiceProviderConfig"
	SCIMSchemasPath               = SCIMBasePath + "/Schemas"
	SCIMResourceTypesPath         = SCIMBasePath + "/ResourceTypes"

	scimSettingsPath      = "/api/v1/scim/settings"
	scimRotateTokenPath   = "/api/v1/scim/settings/token" // #nosec G101 -- API route, not a secret value.
	scimBearerTokenEnv    = "CORDUM_SCIM_BEARER_TOKEN"    // #nosec G101 -- environment variable name, not a secret value.
	scimTokenRedisKey     = "auth:scim:bearer_token"      // #nosec G101 -- Redis key name, not a secret value.
	scimUserMetaPrefix    = "scim:user:meta:"
	scimUserTenantPrefix  = "scim:user:tenant:"
	scimGroupKeyPrefix    = "scim:group:id:"
	scimGroupTenantPrefix = "scim:group:tenant:"
	scimGroupNamePrefix   = "scim:group:name:"

	scimErrorSchema        = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimListSchema         = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema        = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimUserSchema         = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema        = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimSPConfigSchema     = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	scimResourceTypeSchema = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	scimSchemaSchema       = "urn:ietf:params:scim:schemas:core:2.0:Schema"

	scimDefaultCount = 100
	scimMaxCount     = 200
)

type SCIMService struct {
	defaultTenant string
	userStore     UserStore
	redisStore    *RedisUserStore
	resolver      *licensing.EntitlementResolver
	baseURL       string
	now           func() time.Time
	newToken      func() (string, error)
}

type scimName struct {
	Formatted  string `json:"formatted,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
}

type scimMultiValued struct {
	Value   string `json:"value,omitempty"`
	Display string `json:"display,omitempty"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Ref     string `json:"$ref,omitempty"`
}

type scimResourceMeta struct {
	ResourceType string `json:"resourceType,omitempty"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Location     string `json:"location,omitempty"`
}

type scimUserResource struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id,omitempty"`
	ExternalID  string            `json:"externalId,omitempty"`
	UserName    string            `json:"userName,omitempty"`
	DisplayName string            `json:"displayName,omitempty"`
	Name        *scimName         `json:"name,omitempty"`
	Emails      []scimMultiValued `json:"emails,omitempty"`
	Active      *bool             `json:"active,omitempty"`
	Roles       []scimMultiValued `json:"roles,omitempty"`
	Meta        *scimResourceMeta `json:"meta,omitempty"`
}

type scimGroupResource struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id,omitempty"`
	ExternalID  string            `json:"externalId,omitempty"`
	DisplayName string            `json:"displayName,omitempty"`
	Members     []scimMultiValued `json:"members,omitempty"`
	Meta        *scimResourceMeta `json:"meta,omitempty"`
}

type scimListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    any      `json:"Resources,omitempty"`
}

type scimPatchRequest struct {
	Schemas    []string             `json:"schemas"`
	Operations []scimPatchOperation `json:"Operations"`
}

type scimPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

type scimFilter struct {
	Attr  string
	Op    string
	Value string
}

type scimUserMeta struct {
	ExternalID string    `json:"external_id,omitempty"`
	GivenName  string    `json:"given_name,omitempty"`
	FamilyName string    `json:"family_name,omitempty"`
	Source     string    `json:"source,omitempty"`
	SyncedAt   time.Time `json:"synced_at,omitempty"`
}

type scimGroupRecord struct {
	ID          string    `json:"id"`
	Tenant      string    `json:"tenant"`
	ExternalID  string    `json:"external_id,omitempty"`
	DisplayName string    `json:"display_name"`
	Members     []string  `json:"members,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type scimProvisionedUserView struct {
	ID          string `json:"id"`
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Source      string `json:"source,omitempty"`
	Active      bool   `json:"active"`
	SyncedAt    string `json:"syncedAt,omitempty"`
}

type scimSettingsResponse struct {
	Entitled        bool                      `json:"entitled"`
	Configured      bool                      `json:"configured"`
	EndpointURL     string                    `json:"endpointUrl"`
	BearerToken     string                    `json:"bearerToken,omitempty"`
	BearerTokenMask string                    `json:"bearerTokenMasked,omitempty"`
	TokenManagedBy  string                    `json:"tokenManagedBy"`
	Users           []scimProvisionedUserView `json:"users"`
}

func NewSCIMService(store UserStore, defaultTenant string, resolver *licensing.EntitlementResolver) (*SCIMService, error) {
	svc := &SCIMService{
		defaultTenant: normalizeSCIMTenant(defaultTenant),
		userStore:     store,
		resolver:      resolver,
		now: func() time.Time {
			return time.Now().UTC()
		},
		newToken: newSCIMBearerToken,
	}
	if svc.defaultTenant == "" {
		svc.defaultTenant = "default"
	}
	if baseURL, err := parseSAMLBaseURL("CORDUM_API_BASE_URL", "CORDUM_API_BASE", "CORDUM_SAML_BASE_URL"); err == nil && baseURL != nil {
		svc.baseURL = strings.TrimRight(baseURL.String(), "/")
	}
	if rs, ok := store.(*RedisUserStore); ok {
		svc.redisStore = rs
	}
	return svc, nil
}

func (s *SCIMService) Enabled() bool {
	return s != nil && s.userStore != nil
}

func (s *SCIMService) entitlementEnabled() (bool, string) {
	entitlements := licensing.DefaultEntitlements(licensing.PlanCommunity)
	if s != nil && s.resolver != nil {
		entitlements = s.resolver.Entitlements()
	}
	if !entitlements.SCIM {
		return false, "scim"
	}
	return true, ""
}

func (s *SCIMService) AuthenticateHTTP(*http.Request) (*AuthContext, error) {
	return nil, errors.New("scim: delegate to primary")
}

func (s *SCIMService) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return nil, errors.New("scim: delegate to primary")
}

func (s *SCIMService) RequireRole(*http.Request, ...string) error {
	return errors.New("scim: delegate to primary")
}

func (s *SCIMService) ResolveTenant(*http.Request, string, string) (string, error) {
	return "", errors.New("scim: delegate to primary")
}

func (s *SCIMService) RequireTenantAccess(*http.Request, string) error {
	return errors.New("scim: delegate to primary")
}

func (s *SCIMService) ResolvePrincipal(*http.Request, string) (string, error) {
	return "", errors.New("scim: delegate to primary")
}

func (s *SCIMService) IsPublicPath(path string) bool {
	if s == nil || s.userStore == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(path), SCIMBasePath)
}

func (s *SCIMService) RegisterRoutes(mux *http.ServeMux, wrap func(string, http.HandlerFunc) http.HandlerFunc) {
	if s == nil || mux == nil {
		return
	}
	apply := func(route string, fn http.HandlerFunc) http.HandlerFunc {
		if wrap == nil {
			return fn
		}
		return wrap(route, fn)
	}

	mux.HandleFunc("GET "+SCIMServiceProviderConfigPath, apply(SCIMServiceProviderConfigPath, s.handleServiceProviderConfig))
	mux.HandleFunc("GET "+SCIMSchemasPath, apply(SCIMSchemasPath, s.handleSchemas))
	mux.HandleFunc("GET "+SCIMResourceTypesPath, apply(SCIMResourceTypesPath, s.handleResourceTypes))

	mux.HandleFunc("GET "+SCIMUsersPath, apply(SCIMUsersPath, s.handleListUsers))
	mux.HandleFunc("POST "+SCIMUsersPath, apply(SCIMUsersPath, s.handleCreateUser))
	mux.HandleFunc("GET "+SCIMUsersPath+"/{id}", apply(SCIMUsersPath+"/{id}", s.handleGetUser))
	mux.HandleFunc("PUT "+SCIMUsersPath+"/{id}", apply(SCIMUsersPath+"/{id}", s.handleReplaceUser))
	mux.HandleFunc("PATCH "+SCIMUsersPath+"/{id}", apply(SCIMUsersPath+"/{id}", s.handlePatchUser))
	mux.HandleFunc("DELETE "+SCIMUsersPath+"/{id}", apply(SCIMUsersPath+"/{id}", s.handleDeleteUser))

	mux.HandleFunc("GET "+SCIMGroupsPath, apply(SCIMGroupsPath, s.handleListGroups))
	mux.HandleFunc("POST "+SCIMGroupsPath, apply(SCIMGroupsPath, s.handleCreateGroup))
	mux.HandleFunc("GET "+SCIMGroupsPath+"/{id}", apply(SCIMGroupsPath+"/{id}", s.handleGetGroup))
	mux.HandleFunc("PUT "+SCIMGroupsPath+"/{id}", apply(SCIMGroupsPath+"/{id}", s.handleReplaceGroup))
	mux.HandleFunc("PATCH "+SCIMGroupsPath+"/{id}", apply(SCIMGroupsPath+"/{id}", s.handlePatchGroup))
	mux.HandleFunc("DELETE "+SCIMGroupsPath+"/{id}", apply(SCIMGroupsPath+"/{id}", s.handleDeleteGroup))

	mux.HandleFunc("GET "+scimSettingsPath, apply(scimSettingsPath, s.handleSettings))
	mux.HandleFunc("POST "+scimRotateTokenPath, apply(scimRotateTokenPath, s.handleRotateToken))
}

func (s *SCIMService) handleServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, map[string]any{
		"schemas": []string{scimSPConfigSchema},
		"patch":   map[string]any{"supported": true},
		"bulk":    map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":  map[string]any{"supported": true, "maxResults": scimMaxCount},
		"changePassword": map[string]any{
			"supported": true,
		},
		"sort": map[string]any{"supported": true},
		"etag": map[string]any{"supported": true},
		"authenticationSchemes": []map[string]any{{
			"type":        "oauthbearertoken",
			"name":        "Bearer Token",
			"description": "Static SCIM bearer token",
			"primary":     true,
			"specUri":     "https://datatracker.ietf.org/doc/html/rfc6750",
		}},
	})
}

func (s *SCIMService) handleSchemas(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, map[string]any{
		"schemas": []string{scimListSchema},
		"Resources": []map[string]any{
			{
				"schemas":     []string{scimSchemaSchema},
				"id":          scimUserSchema,
				"name":        "User",
				"description": "Cordum SCIM user schema",
				"attributes": []map[string]any{
					{"name": "userName", "type": "string", "required": true, "mutability": "readWrite"},
					{"name": "displayName", "type": "string", "required": false, "mutability": "readWrite"},
					{"name": "active", "type": "boolean", "required": false, "mutability": "readWrite"},
					{"name": "emails", "type": "complex", "multiValued": true, "required": false},
					{"name": "roles", "type": "complex", "multiValued": true, "required": false},
				},
			},
			{
				"schemas":     []string{scimSchemaSchema},
				"id":          scimGroupSchema,
				"name":        "Group",
				"description": "Cordum SCIM group schema",
				"attributes": []map[string]any{
					{"name": "displayName", "type": "string", "required": true, "mutability": "readWrite"},
					{"name": "members", "type": "complex", "multiValued": true, "required": false},
				},
			},
		},
	})
}

func (s *SCIMService) handleResourceTypes(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, map[string]any{
		"schemas": []string{scimListSchema},
		"Resources": []map[string]any{
			{
				"schemas":     []string{scimResourceTypeSchema},
				"id":          "User",
				"name":        "User",
				"endpoint":    "Users",
				"schema":      scimUserSchema,
				"description": "Cordum provisioned users",
			},
			{
				"schemas":     []string{scimResourceTypeSchema},
				"id":          "Group",
				"name":        "Group",
				"endpoint":    "Groups",
				"schema":      scimGroupSchema,
				"description": "Cordum role groups",
			},
		},
	})
}

func (s *SCIMService) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	users, err := s.listSCIMUsers(r.Context(), s.defaultTenant)
	if err != nil {
		s.writeSCIMInternalError(w, r, "list SCIM users", err)
		return
	}
	filtered, err := s.filterSCIMUsers(users, strings.TrimSpace(r.URL.Query().Get("filter")))
	if err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, err.Error(), "invalidFilter")
		return
	}
	sortSCIMUsers(filtered, r.URL.Query().Get("sortBy"), r.URL.Query().Get("sortOrder"))
	startIndex, count := scimPaginationFromRequest(r)
	page := paginateUsers(filtered, startIndex, count)
	s.writeSCIMJSON(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: len(filtered),
		StartIndex:   startIndex,
		ItemsPerPage: len(page),
		Resources:    page,
	})
}

func (s *SCIMService) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	var req scimUserResource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, "invalid SCIM payload", "invalidSyntax")
		return
	}
	created, err := s.createSCIMUser(r.Context(), req)
	if err != nil {
		s.writeSCIMCreateOrUpdateError(w, r, err)
		return
	}
	w.Header().Set("Location", s.resourceLocation(SCIMUsersPath+"/"+created.ID))
	s.writeSCIMJSON(w, http.StatusCreated, created)
}

func (s *SCIMService) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	resource, err := s.getSCIMUser(r.Context(), s.defaultTenant, r.PathValue("id"))
	if err != nil {
		s.writeSCIMLookupError(w, r, err)
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, resource)
}

func (s *SCIMService) handleReplaceUser(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	var req scimUserResource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, "invalid SCIM payload", "invalidSyntax")
		return
	}
	updated, err := s.replaceSCIMUser(r.Context(), s.defaultTenant, r.PathValue("id"), req)
	if err != nil {
		s.writeSCIMCreateOrUpdateError(w, r, err)
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, updated)
}

func (s *SCIMService) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	var req scimPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, "invalid SCIM patch payload", "invalidSyntax")
		return
	}
	updated, err := s.patchSCIMUser(r.Context(), s.defaultTenant, r.PathValue("id"), req)
	if err != nil {
		s.writeSCIMCreateOrUpdateError(w, r, err)
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, updated)
}

func (s *SCIMService) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	if err := s.deleteSCIMUser(r.Context(), s.defaultTenant, r.PathValue("id")); err != nil {
		s.writeSCIMLookupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *SCIMService) handleListGroups(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	groups, err := s.listSCIMGroups(r.Context(), s.defaultTenant)
	if err != nil {
		s.writeSCIMInternalError(w, r, "list SCIM groups", err)
		return
	}
	filtered, err := filterSCIMGroups(groups, strings.TrimSpace(r.URL.Query().Get("filter")))
	if err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, err.Error(), "invalidFilter")
		return
	}
	sortSCIMGroups(filtered, r.URL.Query().Get("sortBy"), r.URL.Query().Get("sortOrder"))
	startIndex, count := scimPaginationFromRequest(r)
	page := paginateGroups(filtered, startIndex, count)
	s.writeSCIMJSON(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: len(filtered),
		StartIndex:   startIndex,
		ItemsPerPage: len(page),
		Resources:    page,
	})
}

func (s *SCIMService) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	var req scimGroupResource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, "invalid SCIM payload", "invalidSyntax")
		return
	}
	created, err := s.createSCIMGroup(r.Context(), s.defaultTenant, req)
	if err != nil {
		s.writeSCIMCreateOrUpdateError(w, r, err)
		return
	}
	w.Header().Set("Location", s.resourceLocation(SCIMGroupsPath+"/"+created.ID))
	s.writeSCIMJSON(w, http.StatusCreated, created)
}

func (s *SCIMService) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	group, err := s.getSCIMGroup(r.Context(), s.defaultTenant, r.PathValue("id"))
	if err != nil {
		s.writeSCIMLookupError(w, r, err)
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, group)
}

func (s *SCIMService) handleReplaceGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	var req scimGroupResource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, "invalid SCIM payload", "invalidSyntax")
		return
	}
	updated, err := s.replaceSCIMGroup(r.Context(), s.defaultTenant, r.PathValue("id"), req)
	if err != nil {
		s.writeSCIMCreateOrUpdateError(w, r, err)
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, updated)
}

func (s *SCIMService) handlePatchGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	var req scimPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeSCIMError(w, http.StatusBadRequest, "invalid SCIM patch payload", "invalidSyntax")
		return
	}
	updated, err := s.patchSCIMGroup(r.Context(), s.defaultTenant, r.PathValue("id"), req)
	if err != nil {
		s.writeSCIMCreateOrUpdateError(w, r, err)
		return
	}
	s.writeSCIMJSON(w, http.StatusOK, updated)
}

func (s *SCIMService) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateSCIMRequest(w, r) {
		return
	}
	if err := s.deleteSCIMGroup(r.Context(), s.defaultTenant, r.PathValue("id")); err != nil {
		s.writeSCIMLookupError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *SCIMService) handleSettings(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.authorizeSettings(w, r)
	if !ok {
		return
	}
	token, source, err := s.currentBearerToken(r.Context())
	if err != nil {
		s.writeSettingsError(w, http.StatusInternalServerError, "failed to load SCIM settings")
		return
	}
	users, err := s.redisStore.listSCIMProvisionedUsers(r.Context(), tenant)
	if err != nil {
		s.writeSettingsError(w, http.StatusInternalServerError, "failed to load SCIM users")
		return
	}
	s.writeSettingsJSON(w, http.StatusOK, scimSettingsResponse{
		Entitled:        true,
		Configured:      strings.TrimSpace(token) != "",
		EndpointURL:     s.resourceLocation(SCIMUsersPath),
		BearerToken:     token,
		BearerTokenMask: maskSCIMSecret(token),
		TokenManagedBy:  source,
		Users:           users,
	})
}

func (s *SCIMService) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authorizeSettings(w, r)
	if !ok {
		return
	}
	if s.redisStore == nil {
		s.writeSettingsError(w, http.StatusBadRequest, "SCIM token rotation requires Redis-backed user auth")
		return
	}
	token, err := s.newToken()
	if err != nil {
		s.writeSettingsError(w, http.StatusInternalServerError, "failed to generate SCIM bearer token")
		return
	}
	if err := s.redisStore.putSCIMBearerToken(r.Context(), token); err != nil {
		s.writeSettingsError(w, http.StatusInternalServerError, "failed to store SCIM bearer token")
		return
	}
	s.writeSettingsJSON(w, http.StatusOK, map[string]any{
		"bearerToken":       token,
		"bearerTokenMasked": maskSCIMSecret(token),
		"tokenManagedBy":    "redis",
	})
}

func (s *SCIMService) authenticateSCIMRequest(w http.ResponseWriter, r *http.Request) bool {
	if s == nil || s.userStore == nil {
		s.writeSCIMError(w, http.StatusServiceUnavailable, "SCIM service unavailable", "")
		return false
	}
	allowed, limit := s.entitlementEnabled()
	if !allowed {
		s.writeSCIMTierLimit(w, limit, fmt.Sprintf("SCIM requires the %s entitlement", strings.ToUpper(limit)))
		return false
	}
	token, _, err := s.currentBearerToken(r.Context())
	if err != nil {
		s.writeSCIMError(w, http.StatusInternalServerError, "failed to load SCIM bearer token", "")
		return false
	}
	if strings.TrimSpace(token) == "" {
		s.writeSCIMError(w, http.StatusServiceUnavailable, "SCIM bearer token not configured", "")
		return false
	}
	supplied := BearerToken(r.Header.Get("Authorization"))
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(supplied)), []byte(strings.TrimSpace(token))) != 1 {
		s.writeSCIMError(w, http.StatusUnauthorized, "invalid bearer token", "")
		return false
	}
	return true
}

func (s *SCIMService) authorizeSettings(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s == nil || s.userStore == nil {
		s.writeSettingsError(w, http.StatusServiceUnavailable, "SCIM service unavailable")
		return "", false
	}
	allowed, _ := s.entitlementEnabled()
	if !allowed {
		s.writeSettingsError(w, http.StatusForbidden, "SCIM entitlement required")
		return "", false
	}
	auth := FromRequest(r)
	if auth == nil {
		s.writeSettingsError(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	if NormalizeRole(auth.Role) != "admin" {
		s.writeSettingsError(w, http.StatusForbidden, "admin role required")
		return "", false
	}
	tenant := normalizeSCIMTenant(auth.Tenant)
	if tenant == "" {
		tenant = s.defaultTenant
	}
	return tenant, true
}

func (s *SCIMService) currentBearerToken(ctx context.Context) (string, string, error) {
	if s != nil && s.redisStore != nil {
		token, err := s.redisStore.getSCIMBearerToken(ctx)
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(token) != "" {
			return token, "redis", nil
		}
	}
	token := NormalizeAPIKey(os.Getenv(scimBearerTokenEnv))
	if token != "" {
		return token, "env", nil
	}
	return "", "none", nil
}

func (s *SCIMService) resourceLocation(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if s != nil && strings.TrimSpace(s.baseURL) != "" {
		return strings.TrimRight(s.baseURL, "/") + path
	}
	return path
}

func (s *SCIMService) writeSCIMJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *SCIMService) writeSCIMError(w http.ResponseWriter, status int, detail string, scimType string) {
	body := map[string]any{
		"schemas": []string{scimErrorSchema},
		"detail":  strings.TrimSpace(detail),
		"status":  strconv.Itoa(status),
	}
	if strings.TrimSpace(scimType) != "" {
		body["scimType"] = strings.TrimSpace(scimType)
	}
	s.writeSCIMJSON(w, status, body)
}

func (s *SCIMService) writeSCIMTierLimit(w http.ResponseWriter, limit, message string) {
	payload := map[string]any{
		"schemas":     []string{scimErrorSchema},
		"detail":      strings.TrimSpace(message),
		"status":      strconv.Itoa(http.StatusForbidden),
		"error":       "tier_limit_exceeded",
		"code":        "tier_limit_exceeded",
		"limit":       strings.TrimSpace(limit),
		"upgrade_url": licensing.DefaultUpgradeURL,
	}
	s.writeSCIMJSON(w, http.StatusForbidden, payload)
}

func (s *SCIMService) writeSCIMInternalError(w http.ResponseWriter, _ *http.Request, _ string, _ error) {
	s.writeSCIMError(w, http.StatusInternalServerError, "internal error", "")
}

func (s *SCIMService) writeSCIMLookupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrUserNotFound), errors.Is(err, redis.Nil):
		s.writeSCIMError(w, http.StatusNotFound, "resource not found", "")
	default:
		s.writeSCIMInternalError(w, r, "lookup scim resource", err)
	}
}

func (s *SCIMService) writeSCIMCreateOrUpdateError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrUserAlreadyExists):
		s.writeSCIMError(w, http.StatusConflict, "resource already exists", "uniqueness")
	case errors.Is(err, ErrUserNotFound), errors.Is(err, redis.Nil):
		s.writeSCIMError(w, http.StatusNotFound, "resource not found", "")
	default:
		s.writeSCIMInternalError(w, r, "persist scim resource", err)
	}
}

func (s *SCIMService) writeSettingsJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *SCIMService) writeSettingsError(w http.ResponseWriter, status int, message string) {
	s.writeSettingsJSON(w, status, map[string]any{"error": message, "status": status})
}

func (s *SCIMService) createSCIMUser(ctx context.Context, req scimUserResource) (scimUserResource, error) {
	if s.redisStore == nil {
		return scimUserResource{}, errors.New("scim requires redis-backed user store")
	}
	username := strings.TrimSpace(req.UserName)
	if username == "" {
		return scimUserResource{}, errors.New("userName required")
	}
	if _, err := s.redisStore.GetByUsername(ctx, username, s.defaultTenant); err == nil {
		return scimUserResource{}, ErrUserAlreadyExists
	} else if !errors.Is(err, ErrUserNotFound) {
		return scimUserResource{}, err
	}
	user := &User{
		Username:    username,
		Email:       firstSCIMEmail(req.Emails),
		DisplayName: resolveSCIMDisplayName(req.DisplayName, req.Name, username),
		Tenant:      s.defaultTenant,
		Role:        firstSCIMRole(req.Roles),
	}
	if user.Role == "" {
		user.Role = "viewer"
	}
	if err := s.redisStore.Create(ctx, user, newProvisionedPassword()); err != nil {
		return scimUserResource{}, err
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	if !active {
		if err := s.redisStore.setUserDisabled(ctx, user.ID, true); err != nil {
			return scimUserResource{}, err
		}
	}
	meta := scimUserMeta{
		ExternalID: strings.TrimSpace(req.ExternalID),
		GivenName:  strings.TrimSpace(scimNameGiven(req.Name)),
		FamilyName: strings.TrimSpace(scimNameFamily(req.Name)),
		Source:     "scim",
		SyncedAt:   s.now(),
	}
	if err := s.redisStore.putSCIMUserMeta(ctx, user.ID, s.defaultTenant, meta); err != nil {
		return scimUserResource{}, err
	}
	return s.getSCIMUser(ctx, s.defaultTenant, user.ID)
}

func (s *SCIMService) getSCIMUser(ctx context.Context, tenant, id string) (scimUserResource, error) {
	if s.redisStore == nil {
		return scimUserResource{}, errors.New("scim requires redis-backed user store")
	}
	user, err := s.redisStore.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return scimUserResource{}, err
	}
	if normalizeSCIMTenant(user.Tenant) != normalizeSCIMTenant(tenant) {
		return scimUserResource{}, ErrUserNotFound
	}
	meta, err := s.redisStore.getSCIMUserMeta(ctx, user.ID)
	if err != nil {
		return scimUserResource{}, err
	}
	if meta.Source != "scim" {
		return scimUserResource{}, ErrUserNotFound
	}
	return s.buildSCIMUserResource(user, meta), nil
}

func (s *SCIMService) replaceSCIMUser(ctx context.Context, tenant, id string, req scimUserResource) (scimUserResource, error) {
	if s.redisStore == nil {
		return scimUserResource{}, errors.New("scim requires redis-backed user store")
	}
	existing, err := s.redisStore.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return scimUserResource{}, err
	}
	meta, err := s.redisStore.getSCIMUserMeta(ctx, existing.ID)
	if err != nil {
		return scimUserResource{}, err
	}
	if meta.Source != "scim" || normalizeSCIMTenant(existing.Tenant) != normalizeSCIMTenant(tenant) {
		return scimUserResource{}, ErrUserNotFound
	}
	desired := &User{
		ID:           existing.ID,
		Username:     strings.TrimSpace(req.UserName),
		Email:        firstSCIMEmail(req.Emails),
		DisplayName:  resolveSCIMDisplayName(req.DisplayName, req.Name, existing.Username),
		PasswordHash: existing.PasswordHash,
		Tenant:       existing.Tenant,
		Role:         firstSCIMRole(req.Roles),
		Disabled:     existing.Disabled,
		CreatedAt:    existing.CreatedAt,
	}
	if desired.Username == "" {
		desired.Username = existing.Username
	}
	if desired.DisplayName == "" {
		desired.DisplayName = existing.DisplayName
	}
	if desired.Role == "" {
		desired.Role = existing.Role
	}
	if req.Active != nil {
		desired.Disabled = !*req.Active
	}
	if err := s.redisStore.upsertUser(ctx, existing, desired); err != nil {
		return scimUserResource{}, err
	}
	meta.ExternalID = strings.TrimSpace(req.ExternalID)
	meta.GivenName = strings.TrimSpace(scimNameGiven(req.Name))
	meta.FamilyName = strings.TrimSpace(scimNameFamily(req.Name))
	meta.Source = "scim"
	meta.SyncedAt = s.now()
	if err := s.redisStore.putSCIMUserMeta(ctx, desired.ID, desired.Tenant, meta); err != nil {
		return scimUserResource{}, err
	}
	return s.getSCIMUser(ctx, desired.Tenant, desired.ID)
}

func (s *SCIMService) patchSCIMUser(ctx context.Context, tenant, id string, req scimPatchRequest) (scimUserResource, error) {
	current, err := s.getSCIMUser(ctx, tenant, id)
	if err != nil {
		return scimUserResource{}, err
	}
	patched, err := applySCIMUserPatch(current, req)
	if err != nil {
		return scimUserResource{}, err
	}
	return s.replaceSCIMUser(ctx, tenant, id, patched)
}

func (s *SCIMService) deleteSCIMUser(ctx context.Context, tenant, id string) error {
	if s.redisStore == nil {
		return errors.New("scim requires redis-backed user store")
	}
	user, err := s.redisStore.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	meta, err := s.redisStore.getSCIMUserMeta(ctx, user.ID)
	if err != nil {
		return err
	}
	if meta.Source != "scim" || normalizeSCIMTenant(user.Tenant) != normalizeSCIMTenant(tenant) {
		return ErrUserNotFound
	}
	return s.redisStore.setUserDisabled(ctx, user.ID, true)
}

func (s *SCIMService) listSCIMUsers(ctx context.Context, tenant string) ([]scimUserResource, error) {
	if s.redisStore == nil {
		return nil, errors.New("scim requires redis-backed user store")
	}
	views, err := s.redisStore.listSCIMProvisionedUsers(ctx, tenant)
	if err != nil {
		return nil, err
	}
	users := make([]scimUserResource, 0, len(views))
	for _, view := range views {
		user, err := s.redisStore.GetByID(ctx, view.ID)
		if err != nil {
			continue
		}
		meta, err := s.redisStore.getSCIMUserMeta(ctx, user.ID)
		if err != nil {
			continue
		}
		users = append(users, s.buildSCIMUserResource(user, meta))
	}
	return users, nil
}

func (s *SCIMService) buildSCIMUserResource(user *User, meta scimUserMeta) scimUserResource {
	active := !user.Disabled
	res := scimUserResource{
		Schemas:     []string{scimUserSchema},
		ID:          user.ID,
		ExternalID:  meta.ExternalID,
		UserName:    user.Username,
		DisplayName: user.DisplayName,
		Active:      &active,
		Meta: &scimResourceMeta{
			ResourceType: "User",
			Created:      user.CreatedAt.UTC().Format(time.RFC3339),
			LastModified: user.UpdatedAt.UTC().Format(time.RFC3339),
			Location:     s.resourceLocation(SCIMUsersPath + "/" + user.ID),
		},
	}
	if trimmed := strings.TrimSpace(user.Email); trimmed != "" {
		res.Emails = []scimMultiValued{{Value: trimmed, Type: "work", Primary: true}}
	}
	if trimmed := strings.TrimSpace(user.Role); trimmed != "" {
		res.Roles = []scimMultiValued{{Value: trimmed, Display: trimmed}}
	}
	if meta.GivenName != "" || meta.FamilyName != "" {
		res.Name = &scimName{
			GivenName:  meta.GivenName,
			FamilyName: meta.FamilyName,
			Formatted:  strings.TrimSpace(strings.TrimSpace(meta.GivenName) + " " + strings.TrimSpace(meta.FamilyName)),
		}
	}
	return res
}

func (s *SCIMService) createSCIMGroup(ctx context.Context, tenant string, req scimGroupResource) (scimGroupResource, error) {
	if s.redisStore == nil {
		return scimGroupResource{}, errors.New("scim requires redis-backed user store")
	}
	displayName := normalizeSCIMRole(req.DisplayName)
	if displayName == "" {
		return scimGroupResource{}, errors.New("displayName required")
	}
	now := s.now()
	record := scimGroupRecord{
		ID:          uuid.New().String(),
		Tenant:      tenant,
		ExternalID:  strings.TrimSpace(req.ExternalID),
		DisplayName: displayName,
		Members:     extractMemberIDs(req.Members),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.redisStore.putSCIMGroup(ctx, record, true); err != nil {
		return scimGroupResource{}, err
	}
	if err := s.redisStore.applyGroupMembership(ctx, tenant, record.DisplayName, record.Members); err != nil {
		return scimGroupResource{}, err
	}
	return s.getSCIMGroup(ctx, tenant, record.ID)
}

func (s *SCIMService) getSCIMGroup(ctx context.Context, tenant, id string) (scimGroupResource, error) {
	if s.redisStore == nil {
		return scimGroupResource{}, errors.New("scim requires redis-backed user store")
	}
	group, err := s.redisStore.getSCIMGroup(ctx, tenant, strings.TrimSpace(id))
	if err != nil {
		return scimGroupResource{}, err
	}
	return s.buildSCIMGroupResource(group), nil
}

func (s *SCIMService) replaceSCIMGroup(ctx context.Context, tenant, id string, req scimGroupResource) (scimGroupResource, error) {
	if s.redisStore == nil {
		return scimGroupResource{}, errors.New("scim requires redis-backed user store")
	}
	existing, err := s.redisStore.getSCIMGroup(ctx, tenant, strings.TrimSpace(id))
	if err != nil {
		return scimGroupResource{}, err
	}
	displayName := normalizeSCIMRole(req.DisplayName)
	if displayName == "" {
		displayName = existing.DisplayName
	}
	updated := scimGroupRecord{
		ID:          existing.ID,
		Tenant:      existing.Tenant,
		ExternalID:  strings.TrimSpace(req.ExternalID),
		DisplayName: displayName,
		Members:     extractMemberIDs(req.Members),
		CreatedAt:   existing.CreatedAt,
		UpdatedAt:   s.now(),
	}
	if err := s.redisStore.putSCIMGroup(ctx, updated, false); err != nil {
		return scimGroupResource{}, err
	}
	if err := s.redisStore.clearGroupMembership(ctx, tenant, existing.DisplayName, existing.Members); err != nil {
		return scimGroupResource{}, err
	}
	if err := s.redisStore.applyGroupMembership(ctx, tenant, updated.DisplayName, updated.Members); err != nil {
		return scimGroupResource{}, err
	}
	return s.getSCIMGroup(ctx, tenant, updated.ID)
}

func (s *SCIMService) patchSCIMGroup(ctx context.Context, tenant, id string, req scimPatchRequest) (scimGroupResource, error) {
	current, err := s.getSCIMGroup(ctx, tenant, id)
	if err != nil {
		return scimGroupResource{}, err
	}
	patched, err := applySCIMGroupPatch(current, req)
	if err != nil {
		return scimGroupResource{}, err
	}
	return s.replaceSCIMGroup(ctx, tenant, id, patched)
}

func (s *SCIMService) deleteSCIMGroup(ctx context.Context, tenant, id string) error {
	if s.redisStore == nil {
		return errors.New("scim requires redis-backed user store")
	}
	existing, err := s.redisStore.getSCIMGroup(ctx, tenant, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if err := s.redisStore.clearGroupMembership(ctx, tenant, existing.DisplayName, existing.Members); err != nil {
		return err
	}
	return s.redisStore.deleteSCIMGroup(ctx, tenant, existing.ID, existing.DisplayName)
}

func (s *SCIMService) listSCIMGroups(ctx context.Context, tenant string) ([]scimGroupResource, error) {
	if s.redisStore == nil {
		return nil, errors.New("scim requires redis-backed user store")
	}
	records, err := s.redisStore.listSCIMGroups(ctx, tenant)
	if err != nil {
		return nil, err
	}
	groups := make([]scimGroupResource, 0, len(records))
	for _, record := range records {
		groups = append(groups, s.buildSCIMGroupResource(record))
	}
	return groups, nil
}

func (s *SCIMService) buildSCIMGroupResource(record scimGroupRecord) scimGroupResource {
	members := make([]scimMultiValued, 0, len(record.Members))
	for _, memberID := range record.Members {
		if trimmed := strings.TrimSpace(memberID); trimmed != "" {
			members = append(members, scimMultiValued{
				Value: trimmed,
				Ref:   s.resourceLocation(SCIMUsersPath + "/" + trimmed),
			})
		}
	}
	return scimGroupResource{
		Schemas:     []string{scimGroupSchema},
		ID:          record.ID,
		ExternalID:  record.ExternalID,
		DisplayName: record.DisplayName,
		Members:     members,
		Meta: &scimResourceMeta{
			ResourceType: "Group",
			Created:      record.CreatedAt.UTC().Format(time.RFC3339),
			LastModified: record.UpdatedAt.UTC().Format(time.RFC3339),
			Location:     s.resourceLocation(SCIMGroupsPath + "/" + record.ID),
		},
	}
}

func (s *RedisUserStore) getSCIMBearerToken(ctx context.Context) (string, error) {
	if s == nil || s.client == nil {
		return "", nil
	}
	token, err := s.client.Get(ctx, scimTokenRedisKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis get scim token: %w", err)
	}
	return NormalizeAPIKey(token), nil
}

func (s *RedisUserStore) putSCIMBearerToken(ctx context.Context, token string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis unavailable")
	}
	if err := s.client.Set(ctx, scimTokenRedisKey, token, 0).Err(); err != nil {
		return fmt.Errorf("redis set scim token: %w", err)
	}
	return nil
}

func (s *RedisUserStore) putSCIMUserMeta(ctx context.Context, userID, tenant string, meta scimUserMeta) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis unavailable")
	}
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("user id required")
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal scim user meta: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, scimUserMetaPrefix+userID, payload, 0)
	pipe.SAdd(ctx, scimUserTenantPrefix+normalizeSCIMTenant(tenant), userID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis store scim user meta: %w", err)
	}
	return nil
}

func (s *RedisUserStore) getSCIMUserMeta(ctx context.Context, userID string) (scimUserMeta, error) {
	if s == nil || s.client == nil {
		return scimUserMeta{}, fmt.Errorf("redis unavailable")
	}
	payload, err := s.client.Get(ctx, scimUserMetaPrefix+strings.TrimSpace(userID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return scimUserMeta{}, ErrUserNotFound
	}
	if err != nil {
		return scimUserMeta{}, fmt.Errorf("redis get scim user meta: %w", err)
	}
	var meta scimUserMeta
	if err := json.Unmarshal(payload, &meta); err != nil {
		return scimUserMeta{}, fmt.Errorf("unmarshal scim user meta: %w", err)
	}
	return meta, nil
}

func (s *RedisUserStore) listSCIMProvisionedUsers(ctx context.Context, tenant string) ([]scimProvisionedUserView, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("redis unavailable")
	}
	ids, err := s.client.SMembers(ctx, scimUserTenantPrefix+normalizeSCIMTenant(tenant)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis smembers scim users: %w", err)
	}
	sort.Strings(ids)
	users := make([]scimProvisionedUserView, 0, len(ids))
	for _, id := range ids {
		user, err := s.GetByID(ctx, id)
		if err != nil {
			continue
		}
		meta, err := s.getSCIMUserMeta(ctx, id)
		if err != nil || meta.Source != "scim" {
			continue
		}
		view := scimProvisionedUserView{
			ID:          user.ID,
			UserName:    user.Username,
			DisplayName: user.DisplayName,
			Email:       user.Email,
			Source:      meta.Source,
			Active:      !user.Disabled,
		}
		if !meta.SyncedAt.IsZero() {
			view.SyncedAt = meta.SyncedAt.UTC().Format(time.RFC3339)
		}
		users = append(users, view)
	}
	return users, nil
}

func (s *RedisUserStore) upsertUser(ctx context.Context, oldUser, newUser *User) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis unavailable")
	}
	if oldUser == nil || newUser == nil {
		return fmt.Errorf("user required")
	}
	if strings.TrimSpace(newUser.ID) == "" {
		return fmt.Errorf("user id required")
	}
	if strings.TrimSpace(newUser.Username) == "" {
		return fmt.Errorf("username required")
	}
	if strings.TrimSpace(newUser.Tenant) == "" {
		newUser.Tenant = oldUser.Tenant
	}
	if newUser.PasswordHash == "" {
		newUser.PasswordHash = oldUser.PasswordHash
	}
	if newUser.CreatedAt.IsZero() {
		newUser.CreatedAt = oldUser.CreatedAt
	}
	newUser.UpdatedAt = time.Now().UTC()

	oldKey := userKey(oldUser.Tenant, oldUser.Username)
	newKey := userKey(newUser.Tenant, newUser.Username)
	oldEmailKey := ""
	newEmailKey := ""
	if oldUser.Email != "" {
		oldEmailKey = userEmailKey(oldUser.Tenant, oldUser.Email)
	}
	if newUser.Email != "" {
		newEmailKey = userEmailKey(newUser.Tenant, newUser.Email)
	}

	if oldKey != newKey {
		if _, err := s.client.Get(ctx, newKey).Result(); err == nil {
			return ErrUserAlreadyExists
		} else if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("redis check username collision: %w", err)
		}
	}
	if newEmailKey != "" && oldEmailKey != newEmailKey {
		if existing, err := s.client.Get(ctx, newEmailKey).Result(); err == nil && !strings.EqualFold(existing, oldUser.Username) {
			return ErrUserAlreadyExists
		} else if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("redis check email collision: %w", err)
		}
	}

	payload, err := json.Marshal(toUserRecord(newUser))
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	pipe := s.client.TxPipeline()
	if oldKey != newKey {
		pipe.Del(ctx, oldKey)
	}
	pipe.Set(ctx, newKey, payload, 0)
	pipe.Set(ctx, userIDKey(newUser.ID), newUser.Tenant+":"+newUser.Username, 0)

	if oldEmailKey != "" && oldEmailKey != newEmailKey {
		pipe.Del(ctx, oldEmailKey)
	}
	if newUser.Disabled {
		if newEmailKey != "" {
			pipe.Del(ctx, newEmailKey)
		}
		pipe.SRem(ctx, userTenantIndexPrefix+newUser.Tenant, newUser.ID)
	} else {
		if newEmailKey != "" {
			pipe.Set(ctx, newEmailKey, newUser.Username, 0)
		}
		pipe.SAdd(ctx, userTenantIndexPrefix+newUser.Tenant, newUser.ID)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis upsert user: %w", err)
	}
	return nil
}

func (s *RedisUserStore) setUserDisabled(ctx context.Context, userID string, disabled bool) error {
	user, err := s.GetByID(ctx, strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	clone := *user
	clone.Disabled = disabled
	return s.upsertUser(ctx, user, &clone)
}

func (s *RedisUserStore) putSCIMGroup(ctx context.Context, record scimGroupRecord, allowCreate bool) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis unavailable")
	}
	if record.ID == "" {
		return fmt.Errorf("group id required")
	}
	if record.Tenant == "" {
		record.Tenant = "default"
	}
	if record.DisplayName == "" {
		return fmt.Errorf("group display name required")
	}
	key := scimGroupKeyPrefix + record.ID
	nameKey := scimGroupNamePrefix + normalizeSCIMTenant(record.Tenant) + ":" + strings.ToLower(record.DisplayName)
	existingID, err := s.client.Get(ctx, nameKey).Result()
	if err == nil && existingID != record.ID {
		return ErrUserAlreadyExists
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("redis get scim group name index: %w", err)
	}
	if !allowCreate {
		if _, err := s.client.Get(ctx, key).Result(); err != nil {
			if errors.Is(err, redis.Nil) {
				return ErrUserNotFound
			}
			return fmt.Errorf("redis get scim group: %w", err)
		}
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal scim group: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, key, payload, 0)
	pipe.Set(ctx, nameKey, record.ID, 0)
	pipe.SAdd(ctx, scimGroupTenantPrefix+normalizeSCIMTenant(record.Tenant), record.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis store scim group: %w", err)
	}
	return nil
}

func (s *RedisUserStore) getSCIMGroup(ctx context.Context, tenant, id string) (scimGroupRecord, error) {
	if s == nil || s.client == nil {
		return scimGroupRecord{}, fmt.Errorf("redis unavailable")
	}
	payload, err := s.client.Get(ctx, scimGroupKeyPrefix+strings.TrimSpace(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return scimGroupRecord{}, ErrUserNotFound
	}
	if err != nil {
		return scimGroupRecord{}, fmt.Errorf("redis get scim group: %w", err)
	}
	var record scimGroupRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return scimGroupRecord{}, fmt.Errorf("unmarshal scim group: %w", err)
	}
	if normalizeSCIMTenant(record.Tenant) != normalizeSCIMTenant(tenant) {
		return scimGroupRecord{}, ErrUserNotFound
	}
	return record, nil
}

func (s *RedisUserStore) listSCIMGroups(ctx context.Context, tenant string) ([]scimGroupRecord, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("redis unavailable")
	}
	ids, err := s.client.SMembers(ctx, scimGroupTenantPrefix+normalizeSCIMTenant(tenant)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis list scim groups: %w", err)
	}
	groups := make([]scimGroupRecord, 0, len(ids))
	for _, id := range ids {
		record, err := s.getSCIMGroup(ctx, tenant, id)
		if err != nil {
			continue
		}
		groups = append(groups, record)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].DisplayName < groups[j].DisplayName
	})
	return groups, nil
}

func (s *RedisUserStore) deleteSCIMGroup(ctx context.Context, tenant, id, displayName string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis unavailable")
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, scimGroupKeyPrefix+strings.TrimSpace(id))
	pipe.Del(ctx, scimGroupNamePrefix+normalizeSCIMTenant(tenant)+":"+strings.ToLower(strings.TrimSpace(displayName)))
	pipe.SRem(ctx, scimGroupTenantPrefix+normalizeSCIMTenant(tenant), strings.TrimSpace(id))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis delete scim group: %w", err)
	}
	return nil
}

func (s *RedisUserStore) applyGroupMembership(ctx context.Context, tenant, role string, members []string) error {
	role = normalizeSCIMRole(role)
	if role == "" {
		return nil
	}
	for _, memberID := range members {
		user, err := s.GetByID(ctx, memberID)
		if err != nil {
			continue
		}
		if normalizeSCIMTenant(user.Tenant) != normalizeSCIMTenant(tenant) {
			continue
		}
		clone := *user
		clone.Role = role
		clone.Disabled = false
		if err := s.upsertUser(ctx, user, &clone); err != nil {
			return err
		}
	}
	return nil
}

func (s *RedisUserStore) clearGroupMembership(ctx context.Context, tenant, role string, members []string) error {
	role = normalizeSCIMRole(role)
	if role == "" {
		return nil
	}
	for _, memberID := range members {
		user, err := s.GetByID(ctx, memberID)
		if err != nil {
			continue
		}
		if normalizeSCIMTenant(user.Tenant) != normalizeSCIMTenant(tenant) {
			continue
		}
		if normalizeSCIMRole(user.Role) != role {
			continue
		}
		clone := *user
		clone.Role = "viewer"
		if err := s.upsertUser(ctx, user, &clone); err != nil {
			return err
		}
	}
	return nil
}

func applySCIMUserPatch(current scimUserResource, req scimPatchRequest) (scimUserResource, error) {
	next := current
	for _, op := range req.Operations {
		action := strings.ToLower(strings.TrimSpace(op.Op))
		if action == "" {
			return scimUserResource{}, errors.New("patch op required")
		}
		switch action {
		case "replace", "add":
			if err := applySingleUserPatch(&next, op.Path, op.Value); err != nil {
				return scimUserResource{}, err
			}
		case "remove":
			if err := removeSingleUserPatch(&next, op.Path); err != nil {
				return scimUserResource{}, err
			}
		default:
			return scimUserResource{}, fmt.Errorf("unsupported patch op %q", op.Op)
		}
	}
	return next, nil
}

func applySingleUserPatch(target *scimUserResource, path string, value any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		payload, ok := value.(map[string]any)
		if !ok {
			return errors.New("patch payload must be object when path is empty")
		}
		for key, val := range payload {
			if err := applySingleUserPatch(target, key, val); err != nil {
				return err
			}
		}
		return nil
	}
	switch strings.ToLower(path) {
	case "username":
		target.UserName = asString(value)
	case "displayname":
		target.DisplayName = asString(value)
	case "externalid":
		target.ExternalID = asString(value)
	case "active":
		parsed, err := asBool(value)
		if err != nil {
			return err
		}
		target.Active = &parsed
	case "name.givenname":
		if target.Name == nil {
			target.Name = &scimName{}
		}
		target.Name.GivenName = asString(value)
	case "name.familyname":
		if target.Name == nil {
			target.Name = &scimName{}
		}
		target.Name.FamilyName = asString(value)
	case "emails", "emails[value eq \"work\"].value", "emails.value":
		email := asString(value)
		if email == "" {
			target.Emails = nil
		} else {
			target.Emails = []scimMultiValued{{Value: email, Type: "work", Primary: true}}
		}
	case "roles", "roles.value":
		role := normalizeSCIMRole(extractRoleValue(value))
		if role == "" {
			target.Roles = nil
		} else {
			target.Roles = []scimMultiValued{{Value: role, Display: role}}
		}
	default:
		return fmt.Errorf("unsupported user patch path %q", path)
	}
	return nil
}

func removeSingleUserPatch(target *scimUserResource, path string) error {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "displayname":
		target.DisplayName = ""
	case "externalid":
		target.ExternalID = ""
	case "name.givenname":
		if target.Name != nil {
			target.Name.GivenName = ""
		}
	case "name.familyname":
		if target.Name != nil {
			target.Name.FamilyName = ""
		}
	case "emails", "emails.value":
		target.Emails = nil
	case "roles", "roles.value":
		target.Roles = nil
	default:
		return fmt.Errorf("unsupported user patch path %q", path)
	}
	return nil
}

func applySCIMGroupPatch(current scimGroupResource, req scimPatchRequest) (scimGroupResource, error) {
	next := current
	for _, op := range req.Operations {
		switch strings.ToLower(strings.TrimSpace(op.Op)) {
		case "replace", "add":
			if err := applySingleGroupPatch(&next, op.Path, op.Value); err != nil {
				return scimGroupResource{}, err
			}
		case "remove":
			if err := removeSingleGroupPatch(&next, op.Path); err != nil {
				return scimGroupResource{}, err
			}
		default:
			return scimGroupResource{}, fmt.Errorf("unsupported patch op %q", op.Op)
		}
	}
	return next, nil
}

func applySingleGroupPatch(target *scimGroupResource, path string, value any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		payload, ok := value.(map[string]any)
		if !ok {
			return errors.New("patch payload must be object when path is empty")
		}
		for key, val := range payload {
			if err := applySingleGroupPatch(target, key, val); err != nil {
				return err
			}
		}
		return nil
	}
	switch strings.ToLower(path) {
	case "displayname":
		target.DisplayName = normalizeSCIMRole(asString(value))
	case "externalid":
		target.ExternalID = asString(value)
	case "members":
		target.Members = memberValuesToRefs(extractMembersFromValue(value))
	default:
		return fmt.Errorf("unsupported group patch path %q", path)
	}
	return nil
}

func removeSingleGroupPatch(target *scimGroupResource, path string) error {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "members":
		target.Members = nil
	case "externalid":
		target.ExternalID = ""
	default:
		return fmt.Errorf("unsupported group patch path %q", path)
	}
	return nil
}

func (s *SCIMService) filterSCIMUsers(users []scimUserResource, raw string) ([]scimUserResource, error) {
	filter, err := parseSCIMFilter(raw)
	if err != nil || filter == nil {
		return users, err
	}
	filtered := make([]scimUserResource, 0, len(users))
	for _, user := range users {
		if filter.matchesUser(user) {
			filtered = append(filtered, user)
		}
	}
	return filtered, nil
}

func filterSCIMGroups(groups []scimGroupResource, raw string) ([]scimGroupResource, error) {
	filter, err := parseSCIMFilter(raw)
	if err != nil || filter == nil {
		return groups, err
	}
	filtered := make([]scimGroupResource, 0, len(groups))
	for _, group := range groups {
		if filter.matchesGroup(group) {
			filtered = append(filtered, group)
		}
	}
	return filtered, nil
}

func parseSCIMFilter(raw string) (*scimFilter, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Fields(raw)
	if len(parts) < 3 {
		return nil, errors.New("invalid SCIM filter")
	}
	attr := strings.TrimSpace(parts[0])
	op := strings.ToLower(strings.TrimSpace(parts[1]))
	value := strings.TrimSpace(strings.Join(parts[2:], " "))
	value = strings.Trim(value, "\"")
	if attr == "" || value == "" {
		return nil, errors.New("invalid SCIM filter")
	}
	switch op {
	case "eq", "co":
		return &scimFilter{Attr: attr, Op: op, Value: value}, nil
	default:
		return nil, fmt.Errorf("unsupported filter operator %q", op)
	}
}

func (f *scimFilter) matchesUser(user scimUserResource) bool {
	if f == nil {
		return true
	}
	var candidate string
	switch strings.ToLower(f.Attr) {
	case "username":
		candidate = user.UserName
	case "displayname":
		candidate = user.DisplayName
	case "externalid":
		candidate = user.ExternalID
	case "id":
		candidate = user.ID
	case "emails.value", "emails":
		candidate = firstSCIMEmail(user.Emails)
	case "active":
		if user.Active == nil {
			candidate = "false"
		} else {
			candidate = strconv.FormatBool(*user.Active)
		}
	default:
		return false
	}
	return compareSCIMFilterValue(candidate, f.Op, f.Value)
}

func (f *scimFilter) matchesGroup(group scimGroupResource) bool {
	if f == nil {
		return true
	}
	var candidate string
	switch strings.ToLower(f.Attr) {
	case "displayname":
		candidate = group.DisplayName
	case "externalid":
		candidate = group.ExternalID
	case "id":
		candidate = group.ID
	default:
		return false
	}
	return compareSCIMFilterValue(candidate, f.Op, f.Value)
}

func compareSCIMFilterValue(candidate, op, want string) bool {
	candidate = strings.TrimSpace(candidate)
	want = strings.TrimSpace(want)
	switch strings.ToLower(op) {
	case "eq":
		return strings.EqualFold(candidate, want)
	case "co":
		return strings.Contains(strings.ToLower(candidate), strings.ToLower(want))
	default:
		return false
	}
}

func scimPaginationFromRequest(r *http.Request) (int, int) {
	startIndex := 1
	count := scimDefaultCount
	if raw := strings.TrimSpace(r.URL.Query().Get("startIndex")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			startIndex = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			count = parsed
		}
	}
	if count > scimMaxCount {
		count = scimMaxCount
	}
	return startIndex, count
}

func paginateUsers(items []scimUserResource, startIndex, count int) []scimUserResource {
	if len(items) == 0 {
		return []scimUserResource{}
	}
	start := startIndex - 1
	if start < 0 || start >= len(items) {
		return []scimUserResource{}
	}
	end := start + count
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func paginateGroups(items []scimGroupResource, startIndex, count int) []scimGroupResource {
	if len(items) == 0 {
		return []scimGroupResource{}
	}
	start := startIndex - 1
	if start < 0 || start >= len(items) {
		return []scimGroupResource{}
	}
	end := start + count
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func sortSCIMUsers(items []scimUserResource, sortBy, sortOrder string) {
	descending := strings.EqualFold(strings.TrimSpace(sortOrder), "descending")
	key := strings.ToLower(strings.TrimSpace(sortBy))
	if key == "" {
		key = "username"
	}
	sort.Slice(items, func(i, j int) bool {
		left := scimUserSortValue(items[i], key)
		right := scimUserSortValue(items[j], key)
		if descending {
			return left > right
		}
		return left < right
	})
}

func scimUserSortValue(item scimUserResource, key string) string {
	switch key {
	case "displayname":
		return strings.ToLower(item.DisplayName)
	case "externalid":
		return strings.ToLower(item.ExternalID)
	case "id":
		return strings.ToLower(item.ID)
	default:
		return strings.ToLower(item.UserName)
	}
}

func sortSCIMGroups(items []scimGroupResource, sortBy, sortOrder string) {
	descending := strings.EqualFold(strings.TrimSpace(sortOrder), "descending")
	key := strings.ToLower(strings.TrimSpace(sortBy))
	if key == "" {
		key = "displayname"
	}
	sort.Slice(items, func(i, j int) bool {
		left := scimGroupSortValue(items[i], key)
		right := scimGroupSortValue(items[j], key)
		if descending {
			return left > right
		}
		return left < right
	})
}

func scimGroupSortValue(item scimGroupResource, key string) string {
	switch key {
	case "id":
		return strings.ToLower(item.ID)
	case "externalid":
		return strings.ToLower(item.ExternalID)
	default:
		return strings.ToLower(item.DisplayName)
	}
}

func firstSCIMEmail(values []scimMultiValued) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value.Value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstSCIMRole(values []scimMultiValued) string {
	for _, value := range values {
		if trimmed := normalizeSCIMRole(value.Value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeSCIMRole(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeSCIMTenant(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "default"
	}
	return raw
}

func resolveSCIMDisplayName(displayName string, name *scimName, username string) string {
	if trimmed := strings.TrimSpace(displayName); trimmed != "" {
		return trimmed
	}
	if name != nil {
		if trimmed := strings.TrimSpace(name.Formatted); trimmed != "" {
			return trimmed
		}
		combined := strings.TrimSpace(strings.TrimSpace(name.GivenName) + " " + strings.TrimSpace(name.FamilyName))
		if combined != "" {
			return combined
		}
	}
	return strings.TrimSpace(username)
}

func scimNameGiven(name *scimName) string {
	if name == nil {
		return ""
	}
	return name.GivenName
}

func scimNameFamily(name *scimName) string {
	if name == nil {
		return ""
	}
	return name.FamilyName
}

func extractRoleValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		return asString(typed["value"])
	case []any:
		for _, item := range typed {
			if role := extractRoleValue(item); role != "" {
				return role
			}
		}
	}
	return ""
}

func extractMemberIDs(values []scimMultiValued) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value.Value); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

func extractMembersFromValue(value any) []string {
	if values, ok := value.([]any); ok {
		out := make([]string, 0, len(values))
		for _, item := range values {
			memberID := ""
			switch typed := item.(type) {
			case string:
				memberID = typed
			case map[string]any:
				memberID = asString(typed["value"])
			}
			if trimmed := strings.TrimSpace(memberID); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}
	if single := strings.TrimSpace(asString(value)); single != "" {
		return []string{single}
	}
	return nil
}

func memberValuesToRefs(memberIDs []string) []scimMultiValued {
	out := make([]scimMultiValued, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		if trimmed := strings.TrimSpace(memberID); trimmed != "" {
			out = append(out, scimMultiValued{Value: trimmed})
		}
	}
	return out
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func asBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, fmt.Errorf("invalid boolean value %q", typed)
		}
		return parsed, nil
	default:
		return false, fmt.Errorf("invalid boolean payload")
	}
}

func newSCIMBearerToken() (string, error) {
	var tokenBytes [32]byte
	if _, err := crand.Read(tokenBytes[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return "scim-" + base64.RawURLEncoding.EncodeToString(tokenBytes[:]), nil
}

func maskSCIMSecret(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= 12 {
		return raw[:4] + strings.Repeat("*", len(raw)-4)
	}
	return raw[:8] + strings.Repeat("*", len(raw)-12) + raw[len(raw)-4:]
}
