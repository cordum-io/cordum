package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// updateUserRequest is the request body for PUT /api/v1/users/{id}.
type updateUserRequest struct {
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// adminPasswordRequest is the request body for POST /api/v1/users/{id}/password.
type adminPasswordRequest struct {
	// #nosec G101 -- password is required in request payloads.
	Password string `json:"password"`
}

// userResponse maps a User to the frontend-expected JSON shape.
func userResponse(u *User) AuthUser {
	var roles []string
	if u.Role != "" {
		roles = []string{u.Role}
	}
	return AuthUser{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Tenant:      u.Tenant,
		Roles:       roles,
		CreatedAt:   u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   u.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// handleListUsers lists all users for the authenticated tenant (admin only).
// GET /api/v1/users
func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	basicAuth, ok := s.auth.(*BasicAuthProvider)
	if !ok || basicAuth.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	users, err := basicAuth.UserStore().List(r.Context(), authCtx.Tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	items := make([]AuthUser, 0, len(users))
	for _, u := range users {
		items = append(items, userResponse(u))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

// handleUpdateUser updates a user's mutable fields (admin only).
// PUT /api/v1/users/{id}
func (s *server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	basicAuth, ok := s.auth.(*BasicAuthProvider)
	if !ok || basicAuth.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	userID := r.PathValue("id")
	if userID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "user id required")
		return
	}

	userStore := basicAuth.UserStore()

	// Load existing user and verify tenant
	existing, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "user not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if existing.Tenant != authCtx.Tenant {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Build update user with only the fields to change
	update := &User{ID: userID}
	if strings.TrimSpace(req.Email) != "" {
		update.Email = strings.TrimSpace(req.Email)
	}
	if strings.TrimSpace(req.DisplayName) != "" {
		update.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	if len(req.Roles) > 0 && strings.TrimSpace(req.Roles[0]) != "" {
		update.Role = strings.TrimSpace(req.Roles[0])
	}

	if err := userStore.Update(r.Context(), update); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Re-fetch for response
	updated, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get updated user")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "update", "user", userID, updated.Username, authCtx.PrincipalID, authCtx.Role, "update user "+updated.Username)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, userResponse(updated))
}

// handleDeleteUser soft-deletes a user (admin only).
// DELETE /api/v1/users/{id}
func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	basicAuth, ok := s.auth.(*BasicAuthProvider)
	if !ok || basicAuth.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	userID := r.PathValue("id")
	if userID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "user id required")
		return
	}

	userStore := basicAuth.UserStore()

	// Load user and verify tenant
	user, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "user not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user.Tenant != authCtx.Tenant {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	// Prevent self-deletion
	if user.ID == authCtx.PrincipalID {
		writeErrorJSON(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}

	if err := userStore.Delete(r.Context(), userID); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "delete", "user", userID, user.Username, authCtx.PrincipalID, authCtx.Role, "delete user "+user.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleChangeUserPassword changes a user's password (admin only).
// POST /api/v1/users/{id}/password
func (s *server) handleChangeUserPassword(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	basicAuth, ok := s.auth.(*BasicAuthProvider)
	if !ok || basicAuth.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	userID := r.PathValue("id")
	if userID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "user id required")
		return
	}

	userStore := basicAuth.UserStore()

	// Load user and verify tenant
	user, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "user not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user.Tenant != authCtx.Tenant {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	var req adminPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	password := strings.TrimSpace(req.Password)
	if len(password) < 8 {
		writeErrorJSON(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	if err := userStore.UpdatePassword(r.Context(), userID, password); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to change password")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "change_password", "user", userID, user.Username, authCtx.PrincipalID, authCtx.Role, "admin changed password for "+user.Username)
	w.WriteHeader(http.StatusNoContent)
}
