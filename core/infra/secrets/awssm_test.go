package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// AWS SM mock server
// ---------------------------------------------------------------------------

type awsMockSecret struct {
	SecretString string
	Name         string
}

func newAWSMockServer(t *testing.T, secrets map[string]awsMockSecret) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Basic validation.
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		target := r.Header.Get("X-Amz-Target")
		if target != awsSMTarget {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"__type":  "InvalidAction",
				"message": "unknown target",
			})
			return
		}

		// Check for authorization header (basic check).
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"__type":  "AccessDeniedException",
				"message": "not authorized",
			})
			return
		}

		var req struct {
			SecretId string `json:"SecretId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		secret, ok := secrets[req.SecretId]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"__type":  "ResourceNotFoundException",
				"message": "secret not found: " + req.SecretId,
			})
			return
		}

		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Name":         secret.Name,
			"SecretString": secret.SecretString,
			"VersionId":    "v1",
			"ARN":          "arn:aws:secretsmanager:us-east-1:123456789:secret:" + req.SecretId,
		})
	}))
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAWSSM_ResolveStringSecret(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{
		"prod/api-key": {
			Name:         "prod/api-key",
			SecretString: "my-api-key-value",
		},
	})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "prod/api-key"}
	val, err := ap.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "my-api-key-value" {
		t.Fatalf("value = %q, want %q", val, "my-api-key-value")
	}
}

func TestAWSSM_ResolveJSONWithKey(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{
		"db/credentials": {
			Name:         "db/credentials",
			SecretString: `{"username":"admin","password":"db-pass-123"}`,
		},
	})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "db/credentials", Key: "password"}
	val, err := ap.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "db-pass-123" {
		t.Fatalf("value = %q, want %q", val, "db-pass-123")
	}
}

func TestAWSSM_SecretNotFound(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "nonexistent"}
	_, err = ap.Resolve(context.Background(), ref)
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestAWSSM_KeyNotFound(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{
		"creds": {
			Name:         "creds",
			SecretString: `{"username":"admin"}`,
		},
	})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "creds", Key: "password"}
	_, err = ap.Resolve(context.Background(), ref)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got: %v", err)
	}
}

func TestAWSSM_NotJSONWithKey(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{
		"plain": {
			Name:         "plain",
			SecretString: "just-a-string-not-json",
		},
	})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "plain", Key: "field"}
	_, err = ap.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error when extracting key from non-JSON secret")
	}
	if !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAWSSM_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "key"}
	_, err = ap.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAWSSM_ValidationErrors(t *testing.T) {
	_, err := NewAWSSecretsManagerProvider("", "AKID", "SECRET", "")
	if err == nil {
		t.Fatal("expected error for empty region")
	}

	_, err = NewAWSSecretsManagerProvider("us-east-1", "", "SECRET", "")
	if err == nil {
		t.Fatal("expected error for empty access key")
	}

	_, err = NewAWSSecretsManagerProvider("us-east-1", "AKID", "", "")
	if err == nil {
		t.Fatal("expected error for empty secret key")
	}
}

func TestAWSSM_Scheme(t *testing.T) {
	ap, _ := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "")
	if ap.Scheme() != "aws-sm" {
		t.Fatalf("scheme = %q, want aws-sm", ap.Scheme())
	}
}

func TestAWSSM_Close(t *testing.T) {
	ap, _ := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "")
	if err := ap.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestAWSSM_WithSessionToken(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{
		"temp-cred": {
			Name:         "temp-cred",
			SecretString: "temporary-value",
		},
	})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "SESSION_TOKEN",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ref := SecretRef{Provider: "aws-sm", Path: "temp-cred"}
	val, err := ap.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "temporary-value" {
		t.Fatalf("value = %q", val)
	}
}

func TestAWSSM_ContextCancelled(t *testing.T) {
	srv := newAWSMockServer(t, map[string]awsMockSecret{
		"key": {Name: "key", SecretString: "val"},
	})
	defer srv.Close()

	ap, err := NewAWSSecretsManagerProvider("us-east-1", "AKID", "SECRET", "",
		WithAWSHTTPClient(srv.Client()),
		WithAWSEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new aws provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ref := SecretRef{Provider: "aws-sm", Path: "key"}
	_, err = ap.Resolve(ctx, ref)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
