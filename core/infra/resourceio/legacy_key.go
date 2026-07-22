package resourceio

import (
	"errors"

	"github.com/cordum/cordum/core/infra/resource"
	infraStore "github.com/cordum/cordum/core/infra/store"
)

var ErrLegacyScopeMismatch = errors.New("resource access: legacy pointer scope mismatch")

type LegacyKind uint8

const (
	LegacyContext LegacyKind = iota + 1
	LegacyResult
)

// BoundLegacyKey validates pointer grammar and binds the Redis namespace and
// job ID to authenticated context before returning a key.
func BoundLegacyKey(pointer string, kind LegacyKind, trusted resource.TrustedContext) (string, error) {
	if resource.ValidateTrustedContext(trusted) != nil {
		return "", ErrInvalidTrustedContext
	}
	key, err := infraStore.KeyFromPointer(pointer)
	if err != nil {
		return "", err
	}
	var expected string
	switch kind {
	case LegacyContext:
		expected = infraStore.MakeContextKey(trusted.JobID)
	case LegacyResult:
		expected = infraStore.MakeResultKey(trusted.JobID)
	default:
		return "", ErrLegacyScopeMismatch
	}
	if key != expected {
		return "", ErrLegacyScopeMismatch
	}
	return key, nil
}
