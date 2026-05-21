package engine

import (
	"errors"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestValidateGovernanceWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *pb.UpdateMemoryRequest
		wantErr error
	}{
		{"nil request", nil, nil},
		{"empty governance fields (legacy chat)", &pb.UpdateMemoryRequest{
			MemoryId: "t-a/mem-1",
			Mode:     pb.ContextMode_CONTEXT_MODE_CHAT,
		}, nil},
		{"raw write kind explicit", &pb.UpdateMemoryRequest{
			MemoryId:  "t-a/mem-1",
			WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_RAW,
		}, nil},
		{"chat write kind explicit", &pb.UpdateMemoryRequest{
			MemoryId:  "t-a/mem-1",
			WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_CHAT,
		}, nil},
		{"shared_policy_state missing writer_agent_id", &pb.UpdateMemoryRequest{
			MemoryId:           "t-a/mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
			ProvenanceRef:      "prov-1",
			ProvenanceVerified: true,
		}, ErrSharedWriteMissingWriter},
		{"shared_trust_state missing provenance_ref", &pb.UpdateMemoryRequest{
			MemoryId:           "t-a/mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_TRUST_STATE,
			WriterAgentId:      "writer-1",
			ProvenanceVerified: true,
		}, ErrSharedWriteMissingProvenance},
		{"shared_directive provenance not verified", &pb.UpdateMemoryRequest{
			MemoryId:           "t-a/mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_DIRECTIVE,
			WriterAgentId:      "writer-1",
			ProvenanceRef:      "prov-1",
			ProvenanceVerified: false,
		}, ErrSharedWriteMissingProvenance},
		{"policy_state_mutation flag without write_kind", &pb.UpdateMemoryRequest{
			MemoryId:            "t-a/mem-1",
			PolicyStateMutation: true,
		}, ErrSharedWriteMissingWriter},
		{"tenant mismatch via memory_id", &pb.UpdateMemoryRequest{
			MemoryId:           "t-victim/mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
			WriterAgentId:      "writer-1",
			TenantId:           "t-a",
			ProvenanceRef:      "prov-1",
			ProvenanceVerified: true,
		}, ErrSharedWriteTenantMismatch},
		{"tenant mismatch via target_agent_id", &pb.UpdateMemoryRequest{
			MemoryId:           "t-a/mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
			WriterAgentId:      "writer-1",
			TenantId:           "t-a",
			TargetAgentId:      "t-victim/agent-1",
			ProvenanceRef:      "prov-1",
			ProvenanceVerified: true,
		}, ErrSharedWriteTenantMismatch},
		{"valid verified writer", &pb.UpdateMemoryRequest{
			MemoryId:           "t-a/mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
			WriterAgentId:      "writer-1",
			TenantId:           "t-a",
			TargetAgentId:      "t-a/agent-1",
			ProvenanceRef:      "prov-1",
			ProvenanceVerified: true,
		}, nil},
		{"shared write with no tenant claim and no tenant-prefixed ids fails closed", &pb.UpdateMemoryRequest{
			MemoryId:           "mem-1",
			WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_TRUST_STATE,
			WriterAgentId:      "writer-1",
			ProvenanceRef:      "prov-1",
			ProvenanceVerified: true,
		}, ErrSharedWriteTenantMismatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateGovernanceWrite(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestGovernanceWrite_TenantPrefixWithoutTenantID_FailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *pb.UpdateMemoryRequest
	}{
		{
			name: "memory_id",
			req: &pb.UpdateMemoryRequest{
				MemoryId:           "t-victim/mem-1",
				WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
				WriterAgentId:      "writer-1",
				ProvenanceRef:      "prov-1",
				ProvenanceVerified: true,
			},
		},
		{
			name: "target_agent_id",
			req: &pb.UpdateMemoryRequest{
				MemoryId:           "mem-1",
				WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
				WriterAgentId:      "writer-1",
				TargetAgentId:      "t-victim/agent-1",
				ProvenanceRef:      "prov-1",
				ProvenanceVerified: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateGovernanceWrite(tc.req)
			if !errors.Is(err, ErrSharedWriteTenantMismatch) {
				t.Fatalf("got %v, want %v", err, ErrSharedWriteTenantMismatch)
			}
		})
	}
}

func TestCheckTenantConsistency_FailsClosedOnEmptyTenantSharedPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *pb.UpdateMemoryRequest
		wantErr error
	}{
		{
			name: "empty tenant shared policy unprefixed memory",
			req: &pb.UpdateMemoryRequest{
				MemoryId:  "mem-x",
				WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
			},
			wantErr: ErrSharedWriteTenantMismatch,
		},
		{
			name: "empty tenant shared trust unprefixed memory",
			req: &pb.UpdateMemoryRequest{
				MemoryId:  "mem-x",
				WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_TRUST_STATE,
			},
			wantErr: ErrSharedWriteTenantMismatch,
		},
		{
			name: "whitespace tenant shared directive unprefixed memory",
			req: &pb.UpdateMemoryRequest{
				MemoryId:  "mem-x",
				TenantId:  " \t ",
				WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_DIRECTIVE,
			},
			wantErr: ErrSharedWriteTenantMismatch,
		},
		{
			name: "empty tenant policy state mutation unprefixed memory",
			req: &pb.UpdateMemoryRequest{
				MemoryId:            "mem-x",
				WriteKind:           pb.MemoryWriteKind_MEMORY_WRITE_KIND_RAW,
				PolicyStateMutation: true,
			},
			wantErr: ErrSharedWriteTenantMismatch,
		},
		{
			name: "empty tenant private raw bootstrap write",
			req: &pb.UpdateMemoryRequest{
				MemoryId:  "mem-x",
				WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_RAW,
			},
		},
		{
			name: "tenant-prefixed memory with matching tenant",
			req: &pb.UpdateMemoryRequest{
				MemoryId:  "tenant-a/mem-x",
				TenantId:  "tenant-a",
				WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkTenantConsistency(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateGovernanceWrite_EmptyTenantVerifiedProvenanceStillFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *pb.UpdateMemoryRequest
	}{
		{
			name: "shared policy state",
			req: &pb.UpdateMemoryRequest{
				MemoryId:           "mem-x",
				WriteKind:          pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
				WriterAgentId:      "writer-1",
				ProvenanceRef:      "prov-1",
				ProvenanceVerified: true,
			},
		},
		{
			name: "policy state mutation flag",
			req: &pb.UpdateMemoryRequest{
				MemoryId:            "mem-x",
				WriteKind:           pb.MemoryWriteKind_MEMORY_WRITE_KIND_RAW,
				PolicyStateMutation: true,
				WriterAgentId:       "writer-1",
				ProvenanceRef:       "prov-1",
				ProvenanceVerified:  true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateGovernanceWrite(tc.req)
			if !errors.Is(err, ErrSharedWriteTenantMismatch) {
				t.Fatalf("got %v, want %v", err, ErrSharedWriteTenantMismatch)
			}
		})
	}
}

func TestIsSharedWriteKind(t *testing.T) {
	t.Parallel()
	for _, k := range []pb.MemoryWriteKind{
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_TRUST_STATE,
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_DIRECTIVE,
	} {
		if !isSharedWriteKind(k) {
			t.Errorf("%v should be classified as shared", k)
		}
	}
	for _, k := range []pb.MemoryWriteKind{
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_UNSPECIFIED,
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_RAW,
		pb.MemoryWriteKind_MEMORY_WRITE_KIND_CHAT,
	} {
		if isSharedWriteKind(k) {
			t.Errorf("%v should not be classified as shared", k)
		}
	}
}

func TestTenantPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		want   string
		wantOK bool
	}{
		"":                 {"", false},
		"   ":              {"", false},
		"mem-1":            {"", false},
		"/mem-1":           {"", false},
		"t-a/mem-1":        {"t-a", true},
		"tenant-a/agent/x": {"tenant-a", true},
	}
	for id, tc := range cases {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			got, ok := tenantPrefix(id)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
