package execgrant

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
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

type mutation struct {
	path   string
	value  protoreflect.Value
	reject Reason
}

func mutations() []mutation {
	s, b, u, i, e := protoreflect.ValueOfString, protoreflect.ValueOfBytes, protoreflect.ValueOfUint64, protoreflect.ValueOfInt64, protoreflect.ValueOfEnum
	return []mutation{
		{"schema_version", protoreflect.ValueOfUint32(2), UnknownVersion},
		{"issuer_id", s("issuer-2"), ""}, {"issuer_key_id", s("grant-2"), ""},
		{"tenant_id", s("tenant-2"), ""}, {"principal_id", s("principal-2"), ""},
		{"agent_id", s("agent-2"), ""}, {"session_id", s("session-2"), ""},
		{"delegation_chain.0.agent_id", s("root-2"), ""}, {"delegation_chain.0.token_jti", s("jti-2"), ""},
		{"delegation_chain.0.parent_jti", s("parent-2"), ""},
		{"action.operation", s("operation-2"), ""},
		{"action.target.account", s("cluster-2"), ""}, {"action.target.environment", s("env-2"), ""}, {"action.target.resource", s("ns-2"), ""},
		{"action.preconditions.0.resource", s("aa"), ""}, {"action.preconditions.0.kind", e(2), ""}, {"action.preconditions.0.expected_version", s("2"), ""},
		{"action.inputs.0.name", s("manifest-2"), ""}, {"action.inputs.0.media_type", s("text/yaml"), ""},
		{"action.inputs.0.sha256", b(bytes.Repeat([]byte{6}, 32)), ""}, {"action.inputs.0.size_bytes", u(124), ""},
		{"action.runtime.tool_id", s("tool-2"), ""}, {"action.runtime.tool_version", s("version-2"), ""}, {"action.runtime.image_sha256", b(bytes.Repeat([]byte{6}, 32)), ""},
		{"action.kubernetes_manifest_apply.manifest_input", s("values"), ""},
		{"action.kubernetes_manifest_apply.objects.0.api_group", s("a"), ""}, {"action.kubernetes_manifest_apply.objects.0.api_version", s("v2"), ""},
		{"action.kubernetes_manifest_apply.objects.0.kind", s("Secret"), ""}, {"action.kubernetes_manifest_apply.objects.0.namespace", s("ns-2"), ""},
		{"action.kubernetes_manifest_apply.objects.0.name", s("b"), ""},
		{"policy_digest", b(bytes.Repeat([]byte{6}, 32)), ""}, {"authority_epoch", u(2), ""},
		{"approval.state", e(1), ""}, {"approval.approval_ref", s("approval-2"), ""},
		{"approval.approver_ids.0", s("amy"), ""}, {"approval.action_commitment", b(bytes.Repeat([]byte{6}, 32)), ""},
		{"enforcement_profile.profile_id", s("profile-2"), ""}, {"enforcement_profile.required_capabilities.0", s("cap-2"), ""},
		{"grant_id", b(bytes.Repeat([]byte{6}, 16)), ""}, {"execution_id", s("execution-2"), ""},
		{"issued_at_unix", i(1700000001), ""}, {"not_before_unix", i(1700000001), ""}, {"expires_at_unix", i(1700000061), ""},
		{"max_uses", protoreflect.ValueOfUint32(2), InvalidField}, {"executor.executor_id", s("broker-2"), ""}, {"executor.executor_kind", e(2), ""},
	}
}

func leafPaths(md protoreflect.MessageDescriptor, parent string) []string {
	var paths []string
	for i := 0; i < md.Fields().Len(); i++ {
		fd := md.Fields().Get(i)
		path := string(fd.Name())
		if parent != "" {
			path = parent + "." + path
		}
		if fd.IsList() {
			path += ".0"
		}
		if fd.Kind() == protoreflect.MessageKind {
			paths = append(paths, leafPaths(fd.Message(), path)...)
		} else {
			paths = append(paths, path)
		}
	}
	return paths
}

func locate(t *testing.T, m protoreflect.Message, path string) (protoreflect.Message, protoreflect.FieldDescriptor) {
	t.Helper()
	parts := strings.Split(path, ".")
	for i := 0; i < len(parts); i++ {
		fd := m.Descriptor().Fields().ByName(protoreflect.Name(parts[i]))
		if fd == nil {
			t.Fatalf("unknown test path %s", path)
		}
		if i == len(parts)-1 || fd.Kind() != protoreflect.MessageKind {
			return m, fd
		}
		if fd.IsList() {
			m = m.Get(fd).List().Get(0).Message()
			i++
		} else {
			m = m.Get(fd).Message()
		}
	}
	t.Fatalf("invalid test path %s", path)
	return nil, nil
}

func TestCanonicalBytesMutations(t *testing.T) {
	base := fullGrant(t)
	before := marshal(t, base)
	covered := make(map[string]bool)
	for _, row := range mutations() {
		if covered[row.path] {
			t.Fatalf("duplicate mutation: %s", row.path)
		}
		covered[row.path] = true
		t.Run(row.path, func(t *testing.T) {
			g := proto.Clone(base).(*pb.ExecutionGrant)
			m, fd := locate(t, g.ProtoReflect(), row.path)
			if fd.IsList() {
				m.Get(fd).List().Set(0, row.value)
			} else {
				m.Set(fd, row.value)
			}
			got, err := CanonicalBytes(g)
			if row.reject != "" {
				assertReason(t, err, row.reject)
				if got != nil {
					t.Fatal("rejection released bytes")
				}
				return
			}
			if err != nil || bytes.Equal(before, got) {
				t.Fatalf("mutation did not change canonical bytes: %v", err)
			}
			if !bytes.Equal(got, marshal(t, g)) {
				t.Fatal("encoder normalized the mutation")
			}
		})
	}
	paths := leafPaths(base.ProtoReflect().Descriptor(), "")
	for _, path := range paths {
		if !covered[path] {
			t.Errorf("uncovered leaf: %s", path)
		}
	}
	if len(covered) != len(paths) {
		t.Fatal("mutation paths do not match descriptor")
	}
	t.Logf("covered %d leaf paths", len(paths))
}

type invalidCase struct {
	name   string
	change func(*pb.ExecutionGrant)
	reason Reason
	field  string
}

func invalidCases() []invalidCase {
	return []invalidCase{
		{"empty-grant-id", func(g *pb.ExecutionGrant) { g.GrantId = nil }, MissingField, "grant_id"},
		{"short-grant-id", func(g *pb.ExecutionGrant) { g.GrantId = []byte{1} }, InvalidField, "grant_id"},
		{"empty-authority", func(g *pb.ExecutionGrant) { g.AuthorityEpoch = 0 }, MissingField, "authority_epoch"},
		{"negative-time", func(g *pb.ExecutionGrant) { g.IssuedAtUnix = -1 }, InvalidField, "issued_at_unix"},
		{"empty-time", func(g *pb.ExecutionGrant) { g.ExpiresAtUnix = 0 }, MissingField, "expires_at_unix"},
		{"empty-action", func(g *pb.ExecutionGrant) { g.Action = nil }, MissingField, "action"},
		{"empty-oneof", func(g *pb.ExecutionGrant) { g.Action.Parameters = nil }, MissingField, "action.kubernetes_manifest_apply"},
		{"nil-oneof-message", func(g *pb.ExecutionGrant) { g.Action.Parameters = &pb.ExecutionGrant_Action_KubernetesManifestApply{} }, MissingField, "action.kubernetes_manifest_apply"},
		{"nil-oneof-wrapper", func(g *pb.ExecutionGrant) {
			g.Action.Parameters = (*pb.ExecutionGrant_Action_KubernetesManifestApply)(nil)
		}, MissingField, "action.kubernetes_manifest_apply"},
		{"nil-list-message", func(g *pb.ExecutionGrant) { g.Action.Inputs[0] = nil }, MissingField, "action.inputs.0"},
		{"empty-inputs", func(g *pb.ExecutionGrant) { g.Action.Inputs = nil }, MissingField, "action.inputs"},
		{"empty-input-name", func(g *pb.ExecutionGrant) { g.Action.Inputs[0].Name = "" }, MissingField, "action.inputs.0.name"},
		{"empty-input-size", func(g *pb.ExecutionGrant) { g.Action.Inputs[0].SizeBytes = 0 }, MissingField, "action.inputs.0.size_bytes"},
		{"large-input", func(g *pb.ExecutionGrant) { g.Action.Inputs[0].SizeBytes = MaxInputSizeBytes + 1 }, InvalidField, "action.inputs.0.size_bytes"},
		{"short-input-digest", func(g *pb.ExecutionGrant) { g.Action.Inputs[0].Sha256 = []byte{1} }, InvalidField, "action.inputs.0.sha256"},
		{"short-image", func(g *pb.ExecutionGrant) { g.Action.Runtime.ImageSha256 = []byte{1} }, InvalidField, "action.runtime.image_sha256"},
		{"short-commitment", func(g *pb.ExecutionGrant) { g.Approval.ActionCommitment = []byte{1} }, InvalidField, "approval.action_commitment"},
		{"empty-approver", func(g *pb.ExecutionGrant) { g.Approval.ApproverIds[0] = "" }, MissingField, "approval.approver_ids.0"},
		{"unsorted-approvers", func(g *pb.ExecutionGrant) { g.Approval.ApproverIds = []string{"b", "a"} }, InvalidField, "approval.approver_ids"},
		{"duplicate-capabilities", func(g *pb.ExecutionGrant) { g.EnforcementProfile.RequiredCapabilities = []string{"a", "a"} }, InvalidField, "enforcement_profile.required_capabilities"},
		{"duplicate-jti", func(g *pb.ExecutionGrant) { g.DelegationChain[2].TokenJti = g.DelegationChain[0].TokenJti }, InvalidField, "delegation_chain"},
		{"unsorted-preconditions", func(g *pb.ExecutionGrant) { g.Action.Preconditions[0].Resource = "z" }, InvalidField, "action.preconditions"},
		{"numeric-precondition-order", func(g *pb.ExecutionGrant) {
			g.Action.Preconditions[0].Resource = "b"
			g.Action.Preconditions[0].Kind = 3
		}, InvalidField, "action.preconditions"},
		{"unknown-oneof-field", func(g *pb.ExecutionGrant) {
			g.Action.GetKubernetesManifestApply().ProtoReflect().SetUnknown(wireInt(99, 1))
		}, UnknownField, "action.kubernetes_manifest_apply"},
		{"version-before-unknown", func(g *pb.ExecutionGrant) { g.SchemaVersion = 2; g.ProtoReflect().SetUnknown(wireInt(99, 1)) }, UnknownVersion, "schema_version"},
		{"unknown-before-missing", func(g *pb.ExecutionGrant) { g.TenantId = ""; g.Executor.ExecutorKind = 99 }, UnknownField, "executor.executor_kind"},
	}
}

func TestCanonicalValidate(t *testing.T) {
	assertReason(t, Validate(nil), UnknownVersion)
	for _, row := range invalidCases() {
		t.Run(row.name, func(t *testing.T) {
			g := fullGrant(t)
			row.change(g)
			out, err := CanonicalBytes(g)
			if got := assertReason(t, err, row.reason); got.Field != row.field {
				t.Fatalf("field %q, want %q", got.Field, row.field)
			}
			if out != nil {
				t.Fatal("rejection released bytes")
			}
		})
	}
	t.Run("scalar-boundaries", checkScalarBounds)
	t.Run("list-boundaries", checkListBounds)
	t.Run("byte-cap", checkByteCap)
}

func checkScalarBounds(t *testing.T) {
	rows := []struct {
		field   string
		values  []protoreflect.Value
		reasons []Reason
	}{
		{"tenant_id", []protoreflect.Value{protoreflect.ValueOfString(strings.Repeat("~", MaxStringBytes)), protoreflect.ValueOfString(strings.Repeat("x", MaxStringBytes+1)), protoreflect.ValueOfString("\x7f"), protoreflect.ValueOfString("é")}, []Reason{"", InvalidField, InvalidField, InvalidField}},
		{"issuer_key_id", []protoreflect.Value{protoreflect.ValueOfString("grant-" + strings.Repeat("x", MaxKeyIDBytes-6)), protoreflect.ValueOfString("grant-" + strings.Repeat("x", MaxKeyIDBytes-5)), protoreflect.ValueOfString("grant-"), protoreflect.ValueOfString("grant-X"), protoreflect.ValueOfString("wrong-key")}, []Reason{"", InvalidField, InvalidField, InvalidField, InvalidField}},
		{"action.inputs.0.size_bytes", []protoreflect.Value{protoreflect.ValueOfUint64(MaxInputSizeBytes)}, []Reason{""}},
		{"approval.state", []protoreflect.Value{protoreflect.ValueOfEnum(0)}, []Reason{MissingField}},
		{"executor.executor_kind", []protoreflect.Value{protoreflect.ValueOfEnum(0)}, []Reason{MissingField}},
		{"action.preconditions.0.kind", []protoreflect.Value{protoreflect.ValueOfEnum(0)}, []Reason{MissingField}},
	}
	for _, row := range rows {
		for i, value := range row.values {
			g := fullGrant(t)
			m, fd := locate(t, g.ProtoReflect(), row.field)
			m.Set(fd, value)
			err := Validate(g)
			if row.reasons[i] == "" {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			if got := assertReason(t, err, row.reasons[i]); got.Field != row.field {
				t.Fatalf("field %s, want %s", got.Field, row.field)
			}
		}
	}
}

func fillList(t *testing.T, g *pb.ExecutionGrant, path string, size int) {
	m, fd := locate(t, g.ProtoReflect(), path)
	list := m.Get(fd).List()
	first := list.Get(0)
	list.Truncate(0)
	key := map[protoreflect.Name]protoreflect.Name{"delegation_chain": "token_jti", "inputs": "name", "objects": "name", "preconditions": "resource"}[fd.Name()]
	for i := 0; i < size; i++ {
		value := protoreflect.ValueOfString(fmt.Sprintf("item-%03d", i))
		if fd.Kind() == protoreflect.MessageKind {
			msg := proto.Clone(first.Message().Interface()).ProtoReflect()
			msg.Set(msg.Descriptor().Fields().ByName(key), value)
			value = protoreflect.ValueOfMessage(msg)
		}
		list.Append(value)
	}
}

func checkListBounds(t *testing.T) {
	for path, max := range map[string]int{"delegation_chain": MaxDelegationLinks, "action.inputs": MaxInputs, "action.kubernetes_manifest_apply.objects": MaxObjects,
		"action.preconditions": MaxPreconditions, "approval.approver_ids": MaxApprovers, "enforcement_profile.required_capabilities": MaxCapabilities} {
		g := fullGrant(t)
		fillList(t, g, path, max)
		if err := Validate(g); err != nil {
			t.Fatal(err)
		}
		fillList(t, g, path, max+1)
		if got := assertReason(t, Validate(g), InvalidField); got.Field != path {
			t.Fatalf("field %s, want %s", got.Field, path)
		}
	}
}

func checkByteCap(t *testing.T) {
	g, err := Decode(make([]byte, MaxGrantBytes+1))
	assertReason(t, err, TooLarge)
	if g != nil {
		t.Fatal("oversized input released a grant")
	}
	g = fullGrant(t)
	fillList(t, g, "action.kubernetes_manifest_apply.objects", 40)
	for _, object := range g.Action.GetKubernetesManifestApply().Objects {
		object.Name += strings.Repeat("x", 2000)
	}
	if err := Validate(g); err != nil {
		t.Fatal(err)
	}
	out, err := CanonicalBytes(g)
	assertReason(t, err, TooLarge)
	if out != nil {
		t.Fatal("oversized grant released bytes")
	}
}
