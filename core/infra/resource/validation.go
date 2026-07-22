package resource

import (
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
)

const maxReferenceURIBytes = 2048

var (
	resolverIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	tenantIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	jobIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@\-\[\]]{0,255}$`)
	mediaTypePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)
	purposePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
)

func validateReference(ref *agentv1.ResourceRef, trusted TrustedContext, now time.Time) (time.Time, error) {
	if ref == nil || !validResolverID(ref.GetResolverId()) || !validTrustedContext(trusted) {
		return time.Time{}, ErrInvalidReference
	}
	uri := ref.GetUri()
	if uri == "" || len(uri) > maxReferenceURIBytes || !utf8.ValidString(uri) {
		return time.Time{}, ErrInvalidReference
	}
	if len(ref.GetSha256()) != sha256DigestBytes || ref.GetSizeBytes() == 0 {
		return time.Time{}, ErrInvalidReference
	}
	if !validMediaType(ref.GetMediaType()) || !purposePattern.MatchString(ref.GetPurpose()) || ref.GetExpiresAt() == nil {
		return time.Time{}, ErrInvalidReference
	}
	if err := ref.GetExpiresAt().CheckValid(); err != nil {
		return time.Time{}, ErrInvalidReference
	}
	expires := ref.GetExpiresAt().AsTime()
	if !expires.After(now) {
		return time.Time{}, ErrExpired
	}
	return expires, nil
}

const sha256DigestBytes = 32

func validResolverID(value string) bool {
	return resolverIDPattern.MatchString(value)
}

func validTrustedContext(trusted TrustedContext) bool {
	return tenantIDPattern.MatchString(trusted.TenantID) &&
		jobIDPattern.MatchString(trusted.JobID) &&
		!strings.Contains(trusted.JobID, "..")
}

// ValidateTrustedContext applies the same grammar used by Registry.Resolve.
// Callers must populate this value only from authenticated local authority.
func ValidateTrustedContext(trusted TrustedContext) error {
	if !validTrustedContext(trusted) {
		return ErrInvalidReference
	}
	return nil
}

func validMediaType(value string) bool {
	if value == "" || value != strings.ToLower(value) || !mediaTypePattern.MatchString(value) {
		return false
	}
	parsed, parameters, err := mime.ParseMediaType(value)
	return err == nil && parsed == value && len(parameters) == 0
}
