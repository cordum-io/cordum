package secrets

import (
	"testing"
)

func TestParseSecretRef_ValidVault(t *testing.T) {
	ref, ok := ParseSecretRef("secret://vault/database/creds#password")
	if !ok {
		t.Fatal("expected valid secret ref")
	}
	if ref.Provider != "vault" {
		t.Fatalf("provider = %q, want %q", ref.Provider, "vault")
	}
	if ref.Path != "database/creds" {
		t.Fatalf("path = %q, want %q", ref.Path, "database/creds")
	}
	if ref.Key != "password" {
		t.Fatalf("key = %q, want %q", ref.Key, "password")
	}
	if ref.Raw != "secret://vault/database/creds#password" {
		t.Fatalf("raw = %q, want original URI", ref.Raw)
	}
}

func TestParseSecretRef_ValidAWSSM(t *testing.T) {
	ref, ok := ParseSecretRef("secret://aws-sm/prod/api-key")
	if !ok {
		t.Fatal("expected valid secret ref")
	}
	if ref.Provider != "aws-sm" {
		t.Fatalf("provider = %q, want %q", ref.Provider, "aws-sm")
	}
	if ref.Path != "prod/api-key" {
		t.Fatalf("path = %q, want %q", ref.Path, "prod/api-key")
	}
	if ref.Key != "" {
		t.Fatalf("key = %q, want empty", ref.Key)
	}
}

func TestParseSecretRef_ValidK8s(t *testing.T) {
	ref, ok := ParseSecretRef("secret://k8s/default/my-secret#token")
	if !ok {
		t.Fatal("expected valid secret ref")
	}
	if ref.Provider != "k8s" {
		t.Fatalf("provider = %q, want %q", ref.Provider, "k8s")
	}
	if ref.Path != "default/my-secret" {
		t.Fatalf("path = %q, want %q", ref.Path, "default/my-secret")
	}
	if ref.Key != "token" {
		t.Fatalf("key = %q, want %q", ref.Key, "token")
	}
}

func TestParseSecretRef_DeepPath(t *testing.T) {
	ref, ok := ParseSecretRef("secret://vault/a/b/c/d/e#key")
	if !ok {
		t.Fatal("expected valid secret ref")
	}
	if ref.Path != "a/b/c/d/e" {
		t.Fatalf("path = %q, want %q", ref.Path, "a/b/c/d/e")
	}
}

func TestParseSecretRef_NoFragment(t *testing.T) {
	ref, ok := ParseSecretRef("secret://vault/simple-path")
	if !ok {
		t.Fatal("expected valid secret ref")
	}
	if ref.Key != "" {
		t.Fatalf("key = %q, want empty", ref.Key)
	}
	if ref.Path != "simple-path" {
		t.Fatalf("path = %q, want %q", ref.Path, "simple-path")
	}
}

func TestParseSecretRef_WhitespaceHandling(t *testing.T) {
	ref, ok := ParseSecretRef("  secret://vault/path  ")
	if !ok {
		t.Fatal("expected valid secret ref after trimming")
	}
	if ref.Provider != "vault" {
		t.Fatalf("provider = %q", ref.Provider)
	}
}

func TestParseSecretRef_NotSecretURI(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"https URL", "https://example.com"},
		{"plain string", "hello world"},
		{"empty string", ""},
		{"just prefix", "secret://"},
		{"no path", "secret://vault"},
		{"no path trailing slash", "secret://vault/"},
		{"spaces only", "   "},
		{"http", "http://vault/path"},
		{"different scheme", "ssecret://vault/path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := ParseSecretRef(tc.input)
			if ok {
				t.Fatalf("ParseSecretRef(%q) = true, want false", tc.input)
			}
		})
	}
}

func TestIsSecretRef(t *testing.T) {
	if !IsSecretRef("secret://vault/path") {
		t.Fatal("expected true for valid ref")
	}
	if IsSecretRef("not-a-secret") {
		t.Fatal("expected false for non-ref")
	}
}

func TestParseSecretRef_SpecialCharsInPath(t *testing.T) {
	// Paths may contain dots, dashes, underscores.
	ref, ok := ParseSecretRef("secret://vault/my.org/api-key_v2")
	if !ok {
		t.Fatal("expected valid secret ref")
	}
	if ref.Path != "my.org/api-key_v2" {
		t.Fatalf("path = %q", ref.Path)
	}
}

func TestParseSecretRef_EmptyFragment(t *testing.T) {
	// "secret://vault/path#" — empty fragment is valid (key = "").
	ref, ok := ParseSecretRef("secret://vault/path#")
	if !ok {
		t.Fatal("expected valid ref with empty fragment")
	}
	if ref.Key != "" {
		t.Fatalf("key = %q, want empty", ref.Key)
	}
}
