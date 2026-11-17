package v1

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// fieldShape is a field's kind, cardinality and message or enum name below cordum.v1.ExecutionGrant.
type fieldShape struct {
	kind protoreflect.Kind
	card protoreflect.Cardinality
	typ  string
}

// grantFieldTable is the ADR-011 "Grant fields" table: envelope.* paths resolve
// in SignedExecutionGrant, every other path in ExecutionGrant.
func grantFieldTable() map[string]fieldShape {
	sc := func(k protoreflect.Kind) fieldShape { return fieldShape{k, protoreflect.Optional, ""} }
	str, byt, i64 := sc(protoreflect.StringKind), sc(protoreflect.BytesKind), sc(protoreflect.Int64Kind)
	strs := fieldShape{protoreflect.StringKind, protoreflect.Repeated, ""}
	msg := func(t string) fieldShape { return fieldShape{protoreflect.MessageKind, protoreflect.Optional, t} }
	msgs := func(t string) fieldShape { return fieldShape{protoreflect.MessageKind, protoreflect.Repeated, t} }
	enum := func(t string) fieldShape { return fieldShape{protoreflect.EnumKind, protoreflect.Optional, t} }
	k8s := "action.kubernetes_manifest_apply."
	return map[string]fieldShape{
		"envelope.grant": byt, "envelope.signature": byt, "envelope.algorithm": str, "envelope.key_id": str,
		"schema_version": sc(protoreflect.Uint32Kind), "issuer_id": str, "issuer_key_id": str, "tenant_id": str,
		"principal_id": str, "agent_id": str, "session_id": str, "delegation_chain": msgs("DelegationLink"),
		"delegation_chain.agent_id": str, "delegation_chain.token_jti": str, "delegation_chain.parent_jti": str,
		"action": msg("Action"), "action.operation": str, "action.kubernetes_manifest_apply": msg("KubernetesManifestApply"),
		k8s + "manifest_input": str, k8s + "objects": msgs("KubernetesObjectRef"), k8s + "objects.api_group": str,
		k8s + "objects.api_version": str, k8s + "objects.kind": str, k8s + "objects.namespace": str, k8s + "objects.name": str,
		"action.target": msg("Target"), "action.target.account": str,
		"action.target.environment": str, "action.target.resource": str,
		"action.preconditions": msgs("Precondition"), "action.preconditions.resource": str,
		"action.preconditions.kind": enum("PreconditionKind"), "action.preconditions.expected_version": str,
		"action.inputs": msgs("InputDigest"), "action.inputs.name": str, "action.inputs.media_type": str,
		"action.inputs.sha256": byt, "action.inputs.size_bytes": sc(protoreflect.Uint64Kind),
		"action.runtime": msg("RuntimeIdentity"), "action.runtime.tool_id": str,
		"action.runtime.tool_version": str, "action.runtime.image_sha256": byt,
		"policy_digest": byt, "authority_epoch": sc(protoreflect.Uint64Kind),
		"approval": msg("Approval"), "approval.state": enum("ApprovalState"), "approval.approval_ref": str,
		"approval.approver_ids": strs, "approval.action_commitment": byt,
		"enforcement_profile": msg("EnforcementProfile"), "enforcement_profile.profile_id": str,
		"enforcement_profile.required_capabilities": strs,
		"grant_id": byt, "execution_id": str, "issued_at_unix": i64, "not_before_unix": i64, "expires_at_unix": i64,
		"max_uses": sc(protoreflect.Uint32Kind),
		"executor": msg("Executor"), "executor.executor_id": str, "executor.executor_kind": enum("ExecutorKind"),
	}
}

// grantEnumValues pins each enum's values in declaration order.
var grantEnumValues = map[string][]string{
	"PreconditionKind": {"PRECONDITION_KIND_UNSPECIFIED=0", "PRECONDITION_KIND_MUST_EXIST=1",
		"PRECONDITION_KIND_MUST_NOT_EXIST=2", "PRECONDITION_KIND_VERSION_EQUALS=3"},
	"ApprovalState": {"APPROVAL_STATE_UNSPECIFIED=0", "APPROVAL_STATE_NOT_REQUIRED=1", "APPROVAL_STATE_APPROVED=2"},
	"ExecutorKind":  {"EXECUTOR_KIND_UNSPECIFIED=0", "EXECUTOR_KIND_BROKER=1", "EXECUTOR_KIND_RUNNER=2"},
}

// collectShapes records every field path under md, and each enum's values as NAME=number.
func collectShapes(prefix string, md protoreflect.MessageDescriptor, shapes map[string]fieldShape, enums map[string][]string) {
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		path := prefix + string(fd.Name())
		shape := fieldShape{kind: fd.Kind(), card: fd.Cardinality()}
		switch {
		case fd.Message() != nil:
			shape.typ = strings.TrimPrefix(string(fd.Message().FullName()), "cordum.v1.ExecutionGrant.")
			collectShapes(path+".", fd.Message(), shapes, enums)
		case fd.Enum() != nil:
			shape.typ = strings.TrimPrefix(string(fd.Enum().FullName()), "cordum.v1.ExecutionGrant.")
			var values []string
			for j := range fd.Enum().Values().Len() {
				v := fd.Enum().Values().Get(j)
				values = append(values, fmt.Sprintf("%s=%d", v.Name(), v.Number()))
			}
			enums[shape.typ] = values
		}
		shapes[path] = shape
	}
}

func diffShapes(want, got map[string]fieldShape) []string {
	var diffs []string
	for path, w := range want {
		if g, ok := got[path]; !ok {
			diffs = append(diffs, "missing "+path)
		} else if g != w {
			diffs = append(diffs, fmt.Sprintf("%s: got %v %v %q, want %v %v %q", path, g.card, g.kind, g.typ, w.card, w.kind, w.typ))
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			diffs = append(diffs, "unlisted "+path)
		}
	}
	slices.Sort(diffs)
	return diffs
}

// shapeMutations each break the collected schema in one way diffShapes must report.
var shapeMutations = map[string]func(map[string]fieldShape){
	"removed field":       func(m map[string]fieldShape) { delete(m, "action.inputs.size_bytes") },
	"changed kind":        func(m map[string]fieldShape) { m["max_uses"] = m["authority_epoch"] },
	"changed cardinality": func(m map[string]fieldShape) { m["approval.approver_ids"] = m["approval.approval_ref"] },
	"extra field":         func(m map[string]fieldShape) { m["action.extra"] = m["action.operation"] },
}

func TestExecutionGrantFieldTable(t *testing.T) {
	want := grantFieldTable()
	got, enums := map[string]fieldShape{}, map[string][]string{}
	collectShapes("envelope.", (&SignedExecutionGrant{}).ProtoReflect().Descriptor(), got, enums)
	collectShapes("", (&ExecutionGrant{}).ProtoReflect().Descriptor(), got, enums)
	if diffs := diffShapes(want, got); len(diffs) > 0 {
		t.Fatalf("schema differs from the ADR-011 field table:\n%s", strings.Join(diffs, "\n"))
	}
	if !maps.EqualFunc(enums, grantEnumValues, slices.Equal) {
		t.Fatalf("enum values = %v, want %v", enums, grantEnumValues)
	}
	oneofs := (&ExecutionGrant_Action{}).ProtoReflect().Descriptor().Oneofs()
	if oneofs.Len() != 1 || oneofs.Get(0).Name() != "parameters" || oneofs.Get(0).Fields().Len() != 1 ||
		oneofs.Get(0).Fields().Get(0).Name() != "kubernetes_manifest_apply" {
		t.Fatalf("Action oneofs: want only parameters { kubernetes_manifest_apply }")
	}
	for name, mutate := range shapeMutations {
		t.Run(name, func(t *testing.T) {
			mutated := maps.Clone(got)
			mutate(mutated)
			if diffs := diffShapes(want, mutated); len(diffs) != 1 {
				t.Fatalf("diffs = %q, want exactly one", diffs)
			}
		})
	}
}

// grantSchemaViolations reports, as "<rule>: <name>", every construct that
// ADR-011 "Canonical bytes" item 1 forbids in the grant schema.
func grantSchemaViolations(fd *descriptorpb.FileDescriptorProto) []string {
	var v []string
	for _, dep := range fd.GetDependency() {
		v = append(v, "import: "+dep)
	}
	// The file's top-level types are checked as the nested types of an empty message.
	return append(v, messageViolations(&descriptorpb.DescriptorProto{NestedType: fd.MessageType, EnumType: fd.EnumType})...)
}

func messageViolations(m *descriptorpb.DescriptorProto) []string {
	var v []string
	if m.GetOptions().GetMapEntry() {
		v = append(v, "map: "+m.GetName())
	}
	inOneof := func(f *descriptorpb.FieldDescriptorProto) bool { return f.OneofIndex != nil && !f.GetProto3Optional() }
	var maxOrdinary int32
	for _, f := range m.GetField() {
		switch f.GetType() {
		case descriptorpb.FieldDescriptorProto_TYPE_FLOAT, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
			v = append(v, "float: "+f.GetName())
		case descriptorpb.FieldDescriptorProto_TYPE_GROUP:
			v = append(v, "group: "+f.GetName())
		}
		if f.GetProto3Optional() {
			v = append(v, "proto3_optional: "+f.GetName())
		}
		if !inOneof(f) {
			maxOrdinary = max(maxOrdinary, f.GetNumber())
		}
	}
	for _, f := range m.GetField() {
		if inOneof(f) && f.GetNumber() <= maxOrdinary {
			v = append(v, "oneof-order: "+f.GetName())
		}
	}
	for _, e := range m.GetEnumType() {
		v = append(v, enumViolations(e)...)
	}
	for _, n := range m.GetNestedType() {
		v = append(v, messageViolations(n)...)
	}
	return v
}

var camelHump = regexp.MustCompile(`\B[A-Z]`)

// enumViolations requires every value to carry the enum's prefix
// (PreconditionKind gives PRECONDITION_KIND_) and value 0 to be <PREFIX>_UNSPECIFIED.
func enumViolations(e *descriptorpb.EnumDescriptorProto) []string {
	prefix := strings.ToUpper(camelHump.ReplaceAllString(e.GetName(), "_$0")) + "_"
	var v []string
	for _, val := range e.GetValue() {
		if !strings.HasPrefix(val.GetName(), prefix) {
			v = append(v, "enum-prefix: "+val.GetName())
		}
		if val.GetNumber() == 0 && val.GetName() != prefix+"UNSPECIFIED" {
			v = append(v, "enum-zero: "+val.GetName())
		}
	}
	return v
}

type fileProto = descriptorpb.FileDescriptorProto

func grant(fd *fileProto) *descriptorpb.DescriptorProto {
	return findMessage(fd.MessageType, "ExecutionGrant")
}

func findMessage(msgs []*descriptorpb.DescriptorProto, name string) *descriptorpb.DescriptorProto {
	return msgs[slices.IndexFunc(msgs, func(m *descriptorpb.DescriptorProto) bool { return m.GetName() == name })]
}

// ruleMutations each add exactly one violation of the named rule; oneof-order moves
// Action.runtime (5) above kubernetes_manifest_apply (6).
var ruleMutations = map[string]func(*fileProto){
	"import": func(fd *fileProto) { fd.Dependency = append(fd.Dependency, "google/protobuf/any.proto") },
	"map": func(fd *fileProto) {
		g := grant(fd)
		g.NestedType = append(g.NestedType, &descriptorpb.DescriptorProto{Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}})
	},
	"float":           func(fd *fileProto) { grant(fd).Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_DOUBLE.Enum() },
	"group":           func(fd *fileProto) { grant(fd).Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_GROUP.Enum() },
	"proto3_optional": func(fd *fileProto) { grant(fd).Field[1].Proto3Optional = proto.Bool(true) },
	"enum-zero":       func(fd *fileProto) { grant(fd).EnumType[0].Value[0].Name = proto.String("PRECONDITION_KIND_NONE") },
	"enum-prefix":     func(fd *fileProto) { grant(fd).EnumType[0].Value[1].Name = proto.String("UNPREFIXED") },
	"oneof-order":     func(fd *fileProto) { findMessage(grant(fd).NestedType, "Action").Field[4].Number = proto.Int32(7) },
}

func TestExecutionGrantDescriptorRules(t *testing.T) {
	file := protodesc.ToFileDescriptorProto(File_execution_grant_proto)
	if v := grantSchemaViolations(file); len(v) > 0 {
		t.Fatalf("execution_grant.proto violations: %q", v)
	}
	for rule, mutate := range ruleMutations {
		t.Run(rule, func(t *testing.T) {
			mutated := proto.CloneOf(file)
			mutate(mutated)
			if v := grantSchemaViolations(mutated); len(v) != 1 || !strings.HasPrefix(v[0], rule+": ") {
				t.Fatalf("violations = %q, want exactly one %q", v, rule)
			}
		})
	}
}
