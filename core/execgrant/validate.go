package execgrant

import (
	"regexp"
	"strconv"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var keyID = regexp.MustCompile(`^grant-[a-z0-9._-]+$`)

// Validate checks the v1 single-field rules. Cross-field bindings and contextual
// authorization belong to the verifier, not this byte-profile validator.
func Validate(g *pb.ExecutionGrant) error {
	if g == nil || g.SchemaVersion != SchemaVersion {
		return &Error{UnknownVersion, "schema_version"}
	}
	if err := unknownFields(g.ProtoReflect(), ""); err != nil {
		return err
	}
	return validateMessage(g.ProtoReflect(), "")
}

func fieldPath(parent string, name protoreflect.Name) string {
	if parent == "" {
		return string(name)
	}
	return parent + "." + string(name)
}

func unknownFields(m protoreflect.Message, path string) error {
	if len(m.GetUnknown()) != 0 {
		return &Error{UnknownField, path}
	}
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}
		p, v := fieldPath(path, fd.Name()), m.Get(fd)
		if fd.IsList() {
			for j := 0; j < v.List().Len(); j++ {
				if err := unknownValue(fd, v.List().Get(j), p+"."+strconv.Itoa(j)); err != nil {
					return err
				}
			}
		} else if err := unknownValue(fd, v, p); err != nil {
			return err
		}
	}
	return nil
}

func unknownValue(fd protoreflect.FieldDescriptor, v protoreflect.Value, path string) error {
	if fd.Kind() == protoreflect.MessageKind {
		return unknownFields(v.Message(), path)
	}
	if fd.Kind() == protoreflect.EnumKind && fd.Enum().Values().ByNumber(v.Enum()) == nil {
		return &Error{UnknownField, path}
	}
	return nil
}

// The optional fields are explicit; all other v1 fields require presence.
func optional(name protoreflect.Name) bool {
	switch name {
	case "delegation_chain", "parent_jti", "api_group", "preconditions", "expected_version",
		"image_sha256", "approval_ref", "approver_ids", "action_commitment":
		return true
	}
	return false
}

func validateMessage(m protoreflect.Message, path string) error {
	if !m.IsValid() {
		return &Error{MissingField, path}
	}
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		p, v := fieldPath(path, fd.Name()), m.Get(fd)
		if !m.Has(fd) {
			if !optional(fd.Name()) {
				return &Error{MissingField, p}
			}
			continue
		}
		if fd.IsList() {
			if err := validateList(fd, v.List(), p); err != nil {
				return err
			}
		} else if err := validateValue(fd, v, p); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(fd protoreflect.FieldDescriptor, v protoreflect.Value, path string) error {
	valid := true
	switch fd.Kind() {
	case protoreflect.MessageKind:
		return validateMessage(v.Message(), path)
	case protoreflect.StringKind:
		s := v.String()
		if s == "" && (fd.IsList() || !optional(fd.Name())) {
			return &Error{MissingField, path}
		}
		valid = len(s) <= MaxStringBytes
		for i := 0; i < len(s); i++ {
			valid = valid && s[i] >= 0x20 && s[i] <= 0x7e
		}
		if fd.Name() == "issuer_key_id" {
			valid = valid && len(s) <= MaxKeyIDBytes && keyID.MatchString(s)
		}
	case protoreflect.BytesKind:
		size := 32
		if fd.Name() == "grant_id" {
			size = 16
		}
		valid = len(v.Bytes()) == size
	case protoreflect.Int64Kind:
		valid = v.Int() > 0
	case protoreflect.Uint32Kind:
		valid = v.Uint() == 1 // schema_version and max_uses are fixed in v1.
	case protoreflect.Uint64Kind:
		if fd.Name() == "size_bytes" {
			valid = v.Uint() <= MaxInputSizeBytes
		}
	case protoreflect.EnumKind:
		valid = v.Enum() != 0
	}
	if !valid {
		return &Error{InvalidField, path}
	}
	return nil
}

func validateList(fd protoreflect.FieldDescriptor, list protoreflect.List, path string) error {
	limit := map[protoreflect.Name]int{
		"delegation_chain": MaxDelegationLinks, "inputs": MaxInputs, "objects": MaxObjects,
		"preconditions": MaxPreconditions, "approver_ids": MaxApprovers, "required_capabilities": MaxCapabilities,
	}[fd.Name()]
	if list.Len() > limit {
		return &Error{InvalidField, path}
	}
	seen := make(map[string]bool, list.Len())
	previous := ""
	for i := 0; i < list.Len(); i++ {
		v := list.Get(i)
		if err := validateValue(fd, v, path+"."+strconv.Itoa(i)); err != nil {
			return err
		}
		key := listKey(fd.Name(), v)
		if seen[key] || (fd.Name() != "delegation_chain" && i > 0 && key <= previous) {
			return &Error{InvalidField, path}
		}
		seen[key], previous = true, key
	}
	return nil
}

func listKey(name protoreflect.Name, v protoreflect.Value) string {
	var fields []protoreflect.Name
	switch name {
	case "delegation_chain":
		fields = []protoreflect.Name{"token_jti"}
	case "inputs":
		fields = []protoreflect.Name{"name"}
	case "objects":
		fields = []protoreflect.Name{"api_group", "api_version", "kind", "namespace", "name"}
	case "preconditions":
		fields = []protoreflect.Name{"resource", "kind"}
	default:
		return v.String()
	}
	m := v.Message()
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = m.Get(m.Descriptor().Fields().ByName(field)).String()
	}
	// All strings are printable ASCII, and the only enum key is 1..3, so NUL
	// separates tuples without collisions and preserves bytewise/numeric order.
	return strings.Join(parts, "\x00")
}
