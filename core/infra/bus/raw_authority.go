package bus

import (
	"context"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// RawAdmissionAuthority contains transport-derived facts established while
// verifying the exact inbound wire bytes. Packet fields are never authority.
type RawAdmissionAuthority struct {
	ActualSubject  string
	SessionSubject string
	TenantID       string
	Identity       *pb.IdentityBinding
	MessageID      []byte
	UnsignedDigest []byte
}

type rawAdmissionAuthorityKey struct{}

func cloneRawAdmissionAuthority(authority *RawAdmissionAuthority) *RawAdmissionAuthority {
	if authority == nil {
		return nil
	}
	cloned := *authority
	cloned.MessageID = append([]byte(nil), authority.MessageID...)
	cloned.UnsignedDigest = append([]byte(nil), authority.UnsignedDigest...)
	if authority.Identity != nil {
		cloned.Identity = proto.Clone(authority.Identity).(*pb.IdentityBinding)
	}
	return &cloned
}

func withRawAdmissionAuthority(ctx context.Context, authority *RawAdmissionAuthority) context.Context {
	if authority == nil {
		return ctx
	}
	return context.WithValue(ctx, rawAdmissionAuthorityKey{}, cloneRawAdmissionAuthority(authority))
}

// RawAdmissionAuthorityFromContext returns an isolated copy of verified
// transport authority. Callers may mutate it without affecting other handlers.
func RawAdmissionAuthorityFromContext(ctx context.Context) (*RawAdmissionAuthority, bool) {
	if ctx == nil {
		return nil, false
	}
	authority, ok := ctx.Value(rawAdmissionAuthorityKey{}).(*RawAdmissionAuthority)
	if !ok || authority == nil {
		return nil, false
	}
	return cloneRawAdmissionAuthority(authority), true
}
