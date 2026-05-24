package policybundles

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

func TestActorIdentity_Priority(t *testing.T) {
	tests := []struct {
		name       string
		ac         *auth.AuthContext
		wantID     string
		wantSource string
		wantLabel  string
	}{
		{
			name:       "nil context",
			ac:         nil,
			wantID:     "",
			wantSource: "",
			wantLabel:  "",
		},
		{
			name:       "empty context",
			ac:         &auth.AuthContext{},
			wantID:     "",
			wantSource: "",
			wantLabel:  "",
		},
		{
			name:       "principal wins over key id and api key",
			ac:         &auth.AuthContext{PrincipalID: "user-1", KeyID: "mk_x", KeyName: "ci", APIKey: "raw-secret"},
			wantID:     "user-1",
			wantSource: "principal",
			wantLabel:  "",
		},
		{
			name:       "key id when no principal, name rides in label",
			ac:         &auth.AuthContext{KeyID: "mk_x", KeyName: "ci", APIKey: "raw-secret"},
			wantID:     "mk_x",
			wantSource: "api_key:mk_x",
			wantLabel:  "ci",
		},
		{
			name:       "static key id",
			ac:         &auth.AuthContext{KeyID: "static:abc123def456"},
			wantID:     "static:abc123def456",
			wantSource: "api_key:static:abc123def456",
			wantLabel:  "",
		},
		{
			name:       "api key fingerprint fallback when no principal or key id",
			ac:         &auth.AuthContext{APIKey: "raw-secret-key-value-000000000000000000000000000"},
			wantID:     auth.APIKeyFingerprint("raw-secret-key-value-000000000000000000000000000"),
			wantSource: "api_key_fp",
			wantLabel:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotSource, gotLabel := ActorIdentity(tt.ac)
			if gotID != tt.wantID {
				t.Fatalf("identity = %q, want %q", gotID, tt.wantID)
			}
			if gotSource != tt.wantSource {
				t.Fatalf("source = %q, want %q", gotSource, tt.wantSource)
			}
			if gotLabel != tt.wantLabel {
				t.Fatalf("label = %q, want %q", gotLabel, tt.wantLabel)
			}
		})
	}
}

func TestActorIdentity_FingerprintIsStableAndNotRawKey(t *testing.T) {
	const raw = "raw-secret-key-value-111111111111111111111111111"
	id, source, _ := ActorIdentity(&auth.AuthContext{APIKey: raw})

	if source != "api_key_fp" {
		t.Fatalf("source = %q, want api_key_fp", source)
	}
	if len(id) != 12 {
		t.Fatalf("fingerprint identity length = %d, want 12 (%q)", len(id), id)
	}
	if id == raw || strings.Contains(raw, id) {
		t.Fatalf("fingerprint %q must not be the raw key", id)
	}
	if again, _, _ := ActorIdentity(&auth.AuthContext{APIKey: raw}); again != id {
		t.Fatalf("fingerprint not deterministic: %q != %q", again, id)
	}
}

// TestAuditEntryToSIEM_NeverLeaksRawKey enforces epic rail + DoD#2: the raw API
// key must never appear in any audit event or its serialized form.
func TestAuditEntryToSIEM_NeverLeaksRawKey(t *testing.T) {
	const rawKey = "super-secret-raw-key"
	ac := &auth.AuthContext{APIKey: rawKey}

	id, source, label := ActorIdentity(ac)
	entry := PolicyAuditEntry{
		Action:         "submit",
		ResourceType:   "job",
		ActorID:        id,
		IdentitySource: source,
		IdentityLabel:  label,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	ev := AuditEntryToSIEM(entry, "tenant-1")

	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal(SIEMEvent) error = %v", err)
	}
	if strings.Contains(string(payload), rawKey) {
		t.Fatalf("raw API key leaked into SIEM event JSON:\n%s", payload)
	}
	// The identity that IS recorded must be the non-reversible fingerprint.
	if ev.Identity != auth.APIKeyFingerprint(rawKey) {
		t.Fatalf("identity = %q, want fingerprint %q", ev.Identity, auth.APIKeyFingerprint(rawKey))
	}
	if ev.Extra["identity_source"] != "api_key_fp" {
		t.Fatalf("identity_source = %q, want api_key_fp", ev.Extra["identity_source"])
	}
}

func TestAuditEntryToSIEM_IdentitySourceAndLabelInExtra(t *testing.T) {
	ac := &auth.AuthContext{KeyID: "mk_x", KeyName: "ci"}
	id, source, label := ActorIdentity(ac)
	entry := PolicyAuditEntry{
		Action:         "submit",
		ResourceType:   "job",
		ActorID:        id,
		IdentitySource: source,
		IdentityLabel:  label,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	ev := AuditEntryToSIEM(entry, "tenant-1")

	if ev.Identity != "mk_x" {
		t.Fatalf("Identity = %q, want %q (must equal ActorID)", ev.Identity, "mk_x")
	}
	if got := ev.Extra["identity_source"]; got != "api_key:mk_x" {
		t.Fatalf("Extra[identity_source] = %q, want %q", got, "api_key:mk_x")
	}
	if got := ev.Extra["identity_label"]; got != "ci" {
		t.Fatalf("Extra[identity_label] = %q, want %q", got, "ci")
	}
}

// When identity_source/identity_label are empty they must be omitted from Extra
// entirely (no empty-string keys), preserving additive export stability.
func TestAuditEntryToSIEM_OmitsEmptyIdentityFields(t *testing.T) {
	entry := PolicyAuditEntry{
		Action:    "submit",
		ActorID:   "user-1",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	ev := AuditEntryToSIEM(entry, "tenant-1")

	if _, ok := ev.Extra["identity_source"]; ok {
		t.Fatalf("identity_source must be omitted when empty, got %q", ev.Extra["identity_source"])
	}
	if _, ok := ev.Extra["identity_label"]; ok {
		t.Fatalf("identity_label must be omitted when empty, got %q", ev.Extra["identity_label"])
	}
}
