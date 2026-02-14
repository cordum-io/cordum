package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubUserStoreEmptyTenant struct{}

func (s *stubUserStoreEmptyTenant) GetByUsername(context.Context, string, string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *stubUserStoreEmptyTenant) GetByEmail(context.Context, string, string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *stubUserStoreEmptyTenant) GetByID(context.Context, string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *stubUserStoreEmptyTenant) Create(context.Context, *User, string) error {
	return nil
}

func (s *stubUserStoreEmptyTenant) List(context.Context, string) ([]*User, error) {
	return []*User{}, nil
}

func (s *stubUserStoreEmptyTenant) Update(context.Context, *User) error {
	return nil
}

func (s *stubUserStoreEmptyTenant) Delete(context.Context, string) error {
	return nil
}

func (s *stubUserStoreEmptyTenant) UpdatePassword(context.Context, string, string) error {
	return nil
}

func (s *stubUserStoreEmptyTenant) ValidatePassword(context.Context, *User, string) bool {
	return false
}

func (s *stubUserStoreEmptyTenant) Close() error {
	return nil
}

func newUserHandlerServer() *server {
	basicAuth := &BasicAuthProvider{}
	basicAuth.SetUserStore(&stubUserStoreEmptyTenant{})
	return &server{auth: basicAuth}
}

func requestWithAuth(req *http.Request, auth *AuthContext) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), authContextKey{}, auth))
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	msg, _ := payload["error"].(string)
	return msg
}

func TestListUsers_EmptyTenant(t *testing.T) {
	s := newUserHandlerServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req = requestWithAuth(req, &AuthContext{Tenant: "", Role: "admin"})
	rec := httptest.NewRecorder()

	s.handleListUsers(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if msg := decodeError(t, rec); strings.TrimSpace(msg) != "tenant required" {
		t.Fatalf("expected tenant required, got %q", msg)
	}
}

func TestUpdateUser_EmptyTenant(t *testing.T) {
	s := newUserHandlerServer()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/user-1", strings.NewReader(`{}`))
	req.SetPathValue("id", "user-1")
	req = requestWithAuth(req, &AuthContext{Tenant: "", Role: "admin"})
	rec := httptest.NewRecorder()

	s.handleUpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if msg := decodeError(t, rec); strings.TrimSpace(msg) != "tenant required" {
		t.Fatalf("expected tenant required, got %q", msg)
	}
}

func TestDeleteUser_EmptyTenant(t *testing.T) {
	s := newUserHandlerServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/user-1", nil)
	req.SetPathValue("id", "user-1")
	req = requestWithAuth(req, &AuthContext{Tenant: "", Role: "admin"})
	rec := httptest.NewRecorder()

	s.handleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if msg := decodeError(t, rec); strings.TrimSpace(msg) != "tenant required" {
		t.Fatalf("expected tenant required, got %q", msg)
	}
}
