package execgrant

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var update = flag.Bool("update", false, "regenerate the execution-grant golden vectors")

type vector struct {
	Name   string `json:"name"`
	Hex    string `json:"hex"`
	Reject Reason `json:"reject"`
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func minimalGrant() *pb.ExecutionGrant {
	return &pb.ExecutionGrant{
		SchemaVersion: 1, IssuerId: "issuer", IssuerKeyId: "grant-test", TenantId: "tenant",
		PrincipalId: "principal", AgentId: "agent", SessionId: "session",
		Action: &pb.ExecutionGrant_Action{
			Operation: "kubernetes.manifest.apply",
			Target:    &pb.ExecutionGrant_Target{Account: "cluster", Environment: "test", Resource: "ns"},
			Inputs:    []*pb.ExecutionGrant_InputDigest{{Name: "manifest", MediaType: "application/yaml", Sha256: bytes.Repeat([]byte{1}, 32), SizeBytes: 123}},
			Runtime:   &pb.ExecutionGrant_RuntimeIdentity{ToolId: "kubectl", ToolVersion: "1.34.0"},
			Parameters: &pb.ExecutionGrant_Action_KubernetesManifestApply{KubernetesManifestApply: &pb.ExecutionGrant_KubernetesManifestApply{
				ManifestInput: "manifest", Objects: []*pb.ExecutionGrant_KubernetesObjectRef{{ApiVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "a"}},
			}},
		},
		PolicyDigest: bytes.Repeat([]byte{2}, 32), AuthorityEpoch: 1,
		Approval:           &pb.ExecutionGrant_Approval{State: pb.ExecutionGrant_APPROVAL_STATE_NOT_REQUIRED},
		EnforcementProfile: &pb.ExecutionGrant_EnforcementProfile{ProfileId: "kubernetes-apply", RequiredCapabilities: []string{"closed-object-set"}},
		GrantId:            bytes.Repeat([]byte{3}, 16), ExecutionId: "execution", IssuedAtUnix: 1700000000,
		NotBeforeUnix: 1700000000, ExpiresAtUnix: 1700000060, MaxUses: 1,
		Executor: &pb.ExecutionGrant_Executor{ExecutorId: "broker", ExecutorKind: pb.ExecutionGrant_EXECUTOR_KIND_BROKER},
	}
}

func fullGrant(t *testing.T) *pb.ExecutionGrant {
	g := minimalGrant()
	g.DelegationChain = []*pb.ExecutionGrant_DelegationLink{
		{AgentId: "root-agent", TokenJti: "z-root"}, {AgentId: "middle-agent", TokenJti: "b-middle", ParentJti: "z-root"},
		{AgentId: "agent", TokenJti: "c-leaf", ParentJti: "b-middle"},
	}
	g.Action.Inputs = append(g.Action.Inputs, &pb.ExecutionGrant_InputDigest{Name: "values", MediaType: "application/json", Sha256: bytes.Repeat([]byte{4}, 32), SizeBytes: 42})
	g.Action.GetKubernetesManifestApply().Objects = append(g.Action.GetKubernetesManifestApply().Objects,
		&pb.ExecutionGrant_KubernetesObjectRef{ApiGroup: "apps", ApiVersion: "v1", Kind: "Deployment", Namespace: "ns", Name: "b"})
	g.Action.Preconditions = []*pb.ExecutionGrant_Precondition{
		{Resource: "a", Kind: pb.ExecutionGrant_PRECONDITION_KIND_MUST_EXIST},
		{Resource: "b", Kind: pb.ExecutionGrant_PRECONDITION_KIND_MUST_NOT_EXIST},
		{Resource: "c", Kind: pb.ExecutionGrant_PRECONDITION_KIND_VERSION_EQUALS, ExpectedVersion: "7"},
	}
	g.Action.Runtime.ImageSha256 = bytes.Repeat([]byte{5}, 32)
	commitment := sha256.Sum256(append([]byte("CORDUM-EXECUTION-GRANT-ACTION-V1\x00"), marshal(t, g.Action)...))
	g.Approval = &pb.ExecutionGrant_Approval{State: pb.ExecutionGrant_APPROVAL_STATE_APPROVED, ApprovalRef: "approval", ApproverIds: []string{"alice", "bob"}, ActionCommitment: commitment[:]}
	return g
}

// rewrite changes the first field at a numeric wire path, preserving other bytes.
func rewrite(t *testing.T, b []byte, path []protowire.Number, change func([]byte) []byte) []byte {
	t.Helper()
	var out []byte
	for len(b) > 0 {
		num, _, n := protowire.ConsumeField(b)
		if n < 0 {
			t.Fatal("bad fixture wire")
		}
		raw := bytes.Clone(b[:n])
		if num == path[0] {
			if len(path) == 1 {
				raw = change(raw)
			} else {
				_, _, tag := protowire.ConsumeTag(raw)
				body, size := protowire.ConsumeBytes(raw[tag:])
				if size < 0 {
					t.Fatal("bad fixture message")
				}
				raw = protowire.AppendBytes(raw[:tag], rewrite(t, body, path[1:], change))
			}
			return append(append(out, raw...), b[n:]...)
		}
		out, b = append(out, raw...), b[n:]
	}
	t.Fatalf("fixture path not found: %v", path)
	return nil
}

func wireBytes(num protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, num, protowire.BytesType), b)
}

func wireInt(num protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, num, protowire.VarintType), value)
}

func goldenVectors(t *testing.T) []vector {
	minimal, full := marshal(t, minimalGrant()), marshal(t, fullGrant(t))
	vec := []vector{{"minimal", hex.EncodeToString(minimal), ""}, {"full", hex.EncodeToString(full), ""}}
	duplicate := func(b []byte) []byte { return append(b, b...) }
	remove := func([]byte) []byte { return nil }
	rows := []struct {
		name   string
		path   []protowire.Number
		change func([]byte) []byte
		reason Reason
	}{
		{"unknown-version", []protowire.Number{1}, func([]byte) []byte { return wireInt(1, 2) }, UnknownVersion},
		{"unknown-field-top", []protowire.Number{20}, func(b []byte) []byte { return append(b, wireInt(99, 1)...) }, UnknownField},
		{"unknown-field-nested", []protowire.Number{9, 4, 4}, func(b []byte) []byte { return append(b, wireInt(99, 1)...) }, UnknownField},
		{"unknown-enum-value", []protowire.Number{9, 3, 2}, func([]byte) []byte { return wireInt(2, 99) }, UnknownField},
		{"duplicate-field", []protowire.Number{4}, duplicate, Malformed},
		{"out-of-order", []protowire.Number{1}, remove, Malformed},
		{"explicit-default", []protowire.Number{9, 6, 2, 2}, func(b []byte) []byte { return append(wireBytes(1, nil), b...) }, Malformed},
		{"non-minimal-varint", []protowire.Number{1}, func([]byte) []byte { return []byte{8, 0x81, 0} }, Malformed},
		{"repeated-oneof-member", []protowire.Number{9, 6}, func(b []byte) []byte { return append(b, wireBytes(6, nil)...) }, Malformed},
		{"truncated", []protowire.Number{20}, func(b []byte) []byte { return b[:len(b)-1] }, Malformed},
		{"missing-tenant", []protowire.Number{4}, remove, MissingField},
		{"missing-executor", []protowire.Number{20}, remove, MissingField},
		{"unsorted-objects", []protowire.Number{9, 6, 2, 2}, func(b []byte) []byte { return append(wireBytes(1, []byte("z")), b...) }, InvalidField},
		{"duplicate-input-name", []protowire.Number{9, 4}, duplicate, InvalidField},
		{"short-policy-digest", []protowire.Number{10}, func([]byte) []byte { return wireBytes(10, []byte{1}) }, InvalidField},
		{"nonprintable-tenant", []protowire.Number{4}, func([]byte) []byte { return wireBytes(4, []byte("bad\n")) }, InvalidField},
	}
	for _, row := range rows {
		b := rewrite(t, full, row.path, row.change)
		if row.name == "out-of-order" {
			b = append(b, wireInt(1, 1)...)
		}
		vec = append(vec, vector{row.name, hex.EncodeToString(b), row.reason})
	}
	return vec
}

func TestGoldenVectorsFile(t *testing.T) {
	want, err := json.MarshalIndent(goldenVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	const path = "testdata/golden_vectors.json"
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("golden vector names/bytes/reasons changed; released vectors must not change")
	}
}

func readVectors(t *testing.T) []vector {
	t.Helper()
	b, err := os.ReadFile("testdata/golden_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
}

func vectorBytes(t *testing.T, v vector) []byte {
	t.Helper()
	b, err := hex.DecodeString(v.Hex)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenRoundTrip(t *testing.T) {
	want := map[string]*pb.ExecutionGrant{"minimal": minimalGrant(), "full": fullGrant(t)}
	for _, v := range readVectors(t) {
		if v.Reject != "" {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			b := vectorBytes(t, v)
			g, err := Decode(b)
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(g, want[v.Name]) {
				t.Fatal("decoded message differs from constructor")
			}
			got, err := CanonicalBytes(g)
			if err != nil || !bytes.Equal(b, got) {
				t.Fatalf("round trip changed bytes: %v", err)
			}
		})
	}
}

func assertReason(t *testing.T, err error, reason Reason) *Error {
	t.Helper()
	var rejected *Error
	if !errors.As(err, &rejected) || rejected.Reason != reason {
		t.Fatalf("got %v, want reason %s", err, reason)
	}
	return rejected
}

func TestGoldenMalformed(t *testing.T) {
	for _, v := range readVectors(t) {
		if v.Reject == "" {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			g, err := Decode(vectorBytes(t, v))
			assertReason(t, err, v.Reject)
			if g != nil {
				t.Fatal("rejected input released a grant")
			}
		})
	}
}

func checkWireOrder(t *testing.T, b []byte, md protoreflect.MessageDescriptor) {
	t.Helper()
	var previous protowire.Number
	for len(b) > 0 {
		num, _, n := protowire.ConsumeField(b)
		if n < 0 || num < previous {
			t.Fatalf("non-ascending wire fields in %s", md.FullName())
		}
		fd := md.Fields().ByNumber(num)
		if fd == nil {
			t.Fatal("unknown fixture field")
		}
		if fd.Kind() == protoreflect.MessageKind {
			_, _, tag := protowire.ConsumeTag(b)
			body, size := protowire.ConsumeBytes(b[tag:n])
			if size < 0 {
				t.Fatal("bad fixture message")
			}
			checkWireOrder(t, body, fd.Message())
		}
		previous, b = num, b[n:]
	}
}

func TestCanonicalWireOrder(t *testing.T) {
	for _, v := range readVectors(t) {
		if v.Reject == "" {
			checkWireOrder(t, vectorBytes(t, v), new(pb.ExecutionGrant).ProtoReflect().Descriptor())
		}
	}
}
