package resource

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/redis/go-redis/v9"
)

const absoluteRedisMaxBytes uint64 = 64 << 20

var (
	authorityPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)
	keyPrefixPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,126}:$`)
	resourceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

// RedisResolverConfig is local operator policy. Client credentials are never
// accepted from a ResourceRef URI.
type RedisResolverConfig struct {
	ID                string
	Client            redis.Cmdable
	Scheme            string
	Authority         string
	KeyPrefix         string
	AllowedMediaTypes []string
	MaxBytes          uint64
}

type redisResolver struct {
	id         string
	client     redis.Cmdable
	authority  string
	keyPrefix  string
	mediaTypes map[string]struct{}
	maxBytes   uint64
}

// NewRedisResolver snapshots an exact URI, scope, type, and size allowlist.
func NewRedisResolver(config RedisResolverConfig) (Resolver, error) {
	if err := validateRedisConfig(config); err != nil {
		return nil, err
	}
	mediaTypes := make(map[string]struct{}, len(config.AllowedMediaTypes))
	for _, mediaType := range config.AllowedMediaTypes {
		mediaTypes[mediaType] = struct{}{}
	}
	return &redisResolver{
		id: config.ID, client: config.Client, authority: config.Authority,
		keyPrefix: config.KeyPrefix, mediaTypes: mediaTypes, maxBytes: config.MaxBytes,
	}, nil
}

func (r *redisResolver) ID() string { return r.id }

func (r *redisResolver) resolve(
	ctx context.Context,
	rawURI string,
	trusted TrustedContext,
	mediaType string,
	declaredSize uint64,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := r.mediaTypes[mediaType]; !ok {
		return nil, ErrMediaTypeMismatch
	}
	if declaredSize > r.maxBytes {
		return nil, ErrSizeMismatch
	}
	key, err := r.redisKey(rawURI, trusted)
	if err != nil {
		return nil, err
	}
	// declaredSize is already bounded above by r.maxBytes, which is itself
	// capped at construction time to absoluteRedisMaxBytes (64 MiB) -- never
	// approaches int64's range.
	content, err := r.client.GetRange(ctx, key, 0, int64(declaredSize)).Bytes() // #nosec G115 -- see comment above
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || len(content) == 0 {
		return nil, ErrUnavailable
	}
	if uint64(len(content)) > declaredSize {
		return nil, ErrSizeMismatch
	}
	return content, nil
}

func (r *redisResolver) redisKey(rawURI string, trusted TrustedContext) (string, error) {
	if strings.ContainsAny(rawURI, "%\x00\\") {
		return "", ErrPolicyViolation
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "redis" || parsed.Host != r.authority {
		return "", ErrPolicyViolation
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.Opaque != "" {
		return "", ErrPolicyViolation
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.Count(parsed.Path, "/") != 1 {
		return "", ErrPolicyViolation
	}
	key := strings.TrimPrefix(parsed.Path, "/")
	expected := r.keyPrefix + trusted.TenantID + ":" + trusted.JobID + ":"
	resourceKey := strings.TrimPrefix(key, expected)
	if resourceKey == key || !validResourceKey(resourceKey) {
		return "", ErrPolicyViolation
	}
	return key, nil
}

func validateRedisConfig(config RedisResolverConfig) error {
	if !validResolverID(config.ID) || interfaceIsNil(config.Client) || config.Scheme != "redis" {
		return ErrInvalidResolverConfig
	}
	if !validAuthority(config.Authority) || !validKeyPrefix(config.KeyPrefix) {
		return ErrInvalidResolverConfig
	}
	if config.MaxBytes == 0 || config.MaxBytes > absoluteRedisMaxBytes || len(config.AllowedMediaTypes) == 0 {
		return ErrInvalidResolverConfig
	}
	seen := make(map[string]struct{}, len(config.AllowedMediaTypes))
	for _, mediaType := range config.AllowedMediaTypes {
		if !validMediaType(mediaType) {
			return ErrInvalidResolverConfig
		}
		if _, exists := seen[mediaType]; exists {
			return ErrInvalidResolverConfig
		}
		seen[mediaType] = struct{}{}
	}
	return nil
}

func validAuthority(value string) bool {
	return authorityPattern.MatchString(value) && !strings.Contains(value, "..") && !strings.HasSuffix(value, ".")
}

func validKeyPrefix(value string) bool {
	return keyPrefixPattern.MatchString(value) && !strings.Contains(value, "..") && !strings.Contains(value, "::")
}

func validResourceKey(value string) bool {
	return resourceKeyPattern.MatchString(value) && !strings.Contains(value, "..")
}
