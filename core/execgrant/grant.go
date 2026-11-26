// Package execgrant implements the execution-grant v1 canonical byte profile.
// It does not authenticate grants or authorize execution.
package execgrant

import (
	"bytes"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// SchemaVersion and bounds define the v1 profile, not deployment policy.
const (
	SchemaVersion      = 1
	MaxGrantBytes      = 65536
	MaxEnvelopeBytes   = MaxGrantBytes + 256
	MaxStringBytes     = 2048
	MaxKeyIDBytes      = 64
	MaxDelegationLinks = 16
	MaxInputs          = 16
	MaxObjects         = 256
	MaxPreconditions   = 256
	MaxApprovers       = 16
	MaxCapabilities    = 32
	MaxInputSizeBytes  = 64 << 20
)

// Reason is a stable machine-readable rejection category.
type Reason string

const (
	TooLarge       Reason = "too_large"
	Malformed      Reason = "malformed"
	UnknownField   Reason = "unknown_field"
	UnknownVersion Reason = "unknown_version"
	MissingField   Reason = "missing_field"
	InvalidField   Reason = "invalid_field"
)

// Error identifies the rejected field; list indices are dotted path components.
// An empty Field denotes the whole encoded payload.
type Error struct {
	Reason Reason
	Field  string
}

func (e *Error) Error() string { return "execution grant: " + string(e.Reason) + ": " + e.Field }

// CanonicalBytes validates without normalizing and returns the exact bytes to sign.
func CanonicalBytes(g *pb.ExecutionGrant) ([]byte, error) {
	if err := Validate(g); err != nil {
		return nil, err
	}
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(g)
	if err != nil {
		return nil, &Error{Reason: Malformed}
	}
	if len(b) > MaxGrantBytes {
		return nil, &Error{Reason: TooLarge}
	}
	return b, nil
}

// Decode accepts only the canonical encoding. Authentication must precede its
// use by a verifier; successful decoding alone confers no execution authority.
func Decode(b []byte) (*pb.ExecutionGrant, error) {
	if len(b) > MaxGrantBytes {
		return nil, &Error{Reason: TooLarge}
	}
	g := new(pb.ExecutionGrant)
	if err := proto.Unmarshal(b, g); err != nil {
		return nil, &Error{Reason: Malformed}
	}
	canonical, err := CanonicalBytes(g)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(b, canonical) {
		return nil, &Error{Reason: Malformed}
	}
	return g, nil
}
