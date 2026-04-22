package delegation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/golang-jwt/jwt/v5"
)

const (
	envDelegationMaxDepth = "CORDUM_DELEGATION_MAX_DEPTH"
	defaultMaxDepth       = 3
	defaultTokenTTL       = 15 * time.Minute
	maxTokenTTL           = 24 * time.Hour
	defaultClockLeeway    = 30 * time.Second
)

type AgentPermissions struct {
	AllowedActions []string
	AllowedTopics  []string
}

type AgentPermissionsResolver interface {
	ResolveAgentPermissions(ctx context.Context, agentID string) (AgentPermissions, error)
}

type IssueRequest struct {
	Tenant            string
	DelegatingAgentID string
	TargetAgentID     string
	AllowedActions    []string
	AllowedTopics     []string
	TTL               time.Duration
	ParentToken       string
}

type ChainLink struct {
	AgentID   string `json:"agent_id"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
	JTI       string `json:"jti"`
	ParentJTI string `json:"parent_jti,omitempty"`
	IssuedBy  string `json:"issued_by"`
}

type DelegationClaims struct {
	Tenant          string      `json:"tenant"`
	AllowedActions  []string    `json:"allowed_actions,omitempty"`
	AllowedTopics   []string    `json:"allowed_topics,omitempty"`
	DelegationChain []ChainLink `json:"delegation_chain"`
	ChainDepth      int         `json:"chain_depth"`
	ParentTokenJTI  string      `json:"parent_token_jti,omitempty"`
	jwt.RegisteredClaims
}

type VerifiedToken struct {
	Token           string
	KID             string
	Subject         string
	Audience        string
	Tenant          string
	AllowedActions  []string
	AllowedTopics   []string
	DelegationChain []ChainLink
	ChainDepth      int
	JTI             string
	ParentTokenJTI  string
	IssuedAt        time.Time
	NotBefore       time.Time
	ExpiresAt       time.Time
	Claims          DelegationClaims
}

type TokenService struct {
	signingKey       SigningKey
	keyring          map[string]ed25519.PublicKey
	agentPermissions AgentPermissionsResolver
	revocations      RevocationStore
	defaultTTL       time.Duration
	maxTTL           time.Duration
	leeway           time.Duration
	maxDepth         int
	now              func() time.Time
}

func NewTokenService(signingKey SigningKey, keyring map[string]ed25519.PublicKey, agentPermissions AgentPermissionsResolver, revocations RevocationStore) *TokenService {
	copiedKeyring := make(map[string]ed25519.PublicKey, len(keyring)+1)
	for kid, pub := range keyring {
		copiedKeyring[kid] = append(ed25519.PublicKey(nil), pub...)
	}
	if pub := signingKey.PublicKey(); len(pub) == ed25519.PublicKeySize {
		copiedKeyring[signingKey.KID] = pub
	}
	return &TokenService{
		signingKey:       signingKey,
		keyring:          copiedKeyring,
		agentPermissions: agentPermissions,
		revocations:      revocations,
		defaultTTL:       defaultTokenTTL,
		maxTTL:           maxTokenTTL,
		leeway:           defaultClockLeeway,
		maxDepth:         env.IntOr(envDelegationMaxDepth, defaultMaxDepth),
		now:              time.Now,
	}
}

func (s *TokenService) KeyID() string {
	if s == nil {
		return ""
	}
	return s.signingKey.KID
}

func (s *TokenService) MaxTTL() time.Duration {
	if s == nil || s.maxTTL <= 0 {
		return maxTokenTTL
	}
	return s.maxTTL
}

func (s *TokenService) IssueDelegationToken(ctx context.Context, req IssueRequest) (string, DelegationClaims, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return "", DelegationClaims{}, fmt.Errorf("delegation token service unavailable")
	}
	if len(s.signingKey.PrivateKey) != ed25519.PrivateKeySize {
		return "", DelegationClaims{}, ErrInvalidSigningKey
	}
	req, ttl, err := s.normalizeIssueRequest(req)
	if err != nil {
		return "", DelegationClaims{}, err
	}

	now := s.now().UTC()
	parent, parentClaims, err := s.loadParentClaims(ctx, req)
	if err != nil {
		return "", DelegationClaims{}, err
	}

	agentPermissions, err := s.resolveAgentPermissions(ctx, req.DelegatingAgentID)
	if err != nil {
		return "", DelegationClaims{}, err
	}

	allowedActions := normalizeStringSet(req.AllowedActions)
	allowedTopics := normalizeStringSet(req.AllowedTopics)
	if len(allowedActions) == 0 {
		allowedActions = normalizeStringSet(agentPermissions.AllowedActions)
	}
	if len(allowedTopics) == 0 {
		allowedTopics = normalizeStringSet(agentPermissions.AllowedTopics)
	}
	if parent != nil {
		if len(req.AllowedActions) == 0 {
			allowedActions = intersectScopes(allowedActions, parent.AllowedActions)
		}
		if len(req.AllowedTopics) == 0 {
			allowedTopics = intersectScopes(allowedTopics, parent.AllowedTopics)
		}
		if !isSubset(allowedActions, parent.AllowedActions) || !isSubset(allowedTopics, parent.AllowedTopics) {
			return "", DelegationClaims{}, ErrScopeExceeded
		}
	}
	if !isSubset(allowedActions, agentPermissions.AllowedActions) || !isSubset(allowedTopics, agentPermissions.AllowedTopics) {
		return "", DelegationClaims{}, ErrScopeExceeded
	}

	jti, err := newTokenJTI()
	if err != nil {
		return "", DelegationClaims{}, err
	}
	expiresAt := now.Add(ttl)

	chain := cloneChain(parentClaims.DelegationChain)
	parentJTI := strings.TrimSpace(parentClaims.ID)
	issuedBy := "cordum"
	if parent != nil {
		issuedBy = req.DelegatingAgentID
	}
	chain = append(chain, ChainLink{
		AgentID:   req.DelegatingAgentID,
		IssuedAt:  now.Format(time.RFC3339Nano),
		ExpiresAt: expiresAt.Format(time.RFC3339Nano),
		JTI:       jti,
		ParentJTI: parentJTI,
		IssuedBy:  issuedBy,
	})
	if len(chain) > s.maxDepth {
		return "", DelegationClaims{}, ErrChainTooDeep
	}

	claims := DelegationClaims{
		Tenant:          req.Tenant,
		AllowedActions:  allowedActions,
		AllowedTopics:   allowedTopics,
		DelegationChain: chain,
		ChainDepth:      len(chain),
		ParentTokenJTI:  parentJTI,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "cordum",
			Subject:   req.DelegatingAgentID,
			Audience:  jwt.ClaimStrings{req.TargetAgentID},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.signingKey.KID
	signed, err := token.SignedString(s.signingKey.PrivateKey)
	if err != nil {
		return "", DelegationClaims{}, fmt.Errorf("sign delegation token: %w", err)
	}
	return signed, claims, nil
}

func (s *TokenService) VerifyDelegationToken(ctx context.Context, tokenStr, expectedAudience string) (VerifiedToken, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return VerifiedToken{}, fmt.Errorf("delegation token service unavailable")
	}
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return VerifiedToken{}, ErrMalformed
	}
	claims := &DelegationClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithLeeway(s.leeway),
		jwt.WithIssuer("cordum"),
		jwt.WithTimeFunc(func() time.Time { return s.now().UTC() }),
	)
	token, err := parser.ParseWithClaims(tokenStr, claims, s.lookupVerificationKey)
	if err != nil {
		return VerifiedToken{}, mapJWTError(err)
	}
	if !token.Valid {
		return VerifiedToken{}, ErrMalformed
	}
	if expectedAudience != "" && !audienceContains(claims.Audience, expectedAudience) {
		return VerifiedToken{}, ErrAudienceMismatch
	}
	verified := verifiedTokenFromClaims(tokenStr, headerString(token, "kid"), *claims)
	if verified.ChainDepth == 0 || verified.ChainDepth != len(verified.DelegationChain) {
		return VerifiedToken{}, ErrMalformed
	}
	if verified.ChainDepth > s.maxDepth {
		return VerifiedToken{}, ErrChainTooDeep
	}
	if err := s.validateCurrentScope(ctx, verified); err != nil {
		return VerifiedToken{}, err
	}
	if err := s.validateRevocation(ctx, verified); err != nil {
		return VerifiedToken{}, err
	}
	return verified, nil
}

func (s *TokenService) normalizeIssueRequest(req IssueRequest) (IssueRequest, time.Duration, error) {
	req.Tenant = strings.TrimSpace(req.Tenant)
	req.DelegatingAgentID = strings.TrimSpace(req.DelegatingAgentID)
	req.TargetAgentID = strings.TrimSpace(req.TargetAgentID)
	if req.Tenant == "" || req.DelegatingAgentID == "" || req.TargetAgentID == "" {
		return IssueRequest{}, 0, ErrMalformed
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	if ttl > s.maxTTL {
		return IssueRequest{}, 0, fmt.Errorf("%w: ttl exceeds %s", ErrMalformed, s.maxTTL)
	}
	return req, ttl, nil
}

func (s *TokenService) loadParentClaims(ctx context.Context, req IssueRequest) (*VerifiedToken, DelegationClaims, error) {
	parentToken := strings.TrimSpace(req.ParentToken)
	if parentToken == "" {
		return nil, DelegationClaims{}, nil
	}
	parent, err := s.VerifyDelegationToken(ctx, parentToken, req.DelegatingAgentID)
	if err != nil {
		return nil, DelegationClaims{}, err
	}
	return &parent, parent.Claims, nil
}

func (s *TokenService) resolveAgentPermissions(ctx context.Context, agentID string) (AgentPermissions, error) {
	if s.agentPermissions == nil {
		return AgentPermissions{}, nil
	}
	perms, err := s.agentPermissions.ResolveAgentPermissions(ctx, agentID)
	if err != nil {
		return AgentPermissions{}, fmt.Errorf("resolve delegation permissions: %w", err)
	}
	perms.AllowedActions = normalizeStringSet(perms.AllowedActions)
	perms.AllowedTopics = normalizeStringSet(perms.AllowedTopics)
	return perms, nil
}

func (s *TokenService) validateCurrentScope(ctx context.Context, verified VerifiedToken) error {
	if s.agentPermissions == nil {
		return nil
	}
	perms, err := s.resolveAgentPermissions(ctx, verified.Subject)
	if err != nil {
		return err
	}
	if !isSubset(verified.AllowedActions, perms.AllowedActions) || !isSubset(verified.AllowedTopics, perms.AllowedTopics) {
		return ErrScopeExceeded
	}
	return nil
}

func (s *TokenService) validateRevocation(ctx context.Context, verified VerifiedToken) error {
	if s.revocations == nil {
		return nil
	}
	jtis := make([]string, 0, len(verified.DelegationChain)+1)
	if verified.JTI != "" {
		jtis = append(jtis, verified.JTI)
	}
	for _, link := range verified.DelegationChain {
		if link.JTI != "" {
			jtis = append(jtis, link.JTI)
		}
	}
	for _, jti := range normalizeStringSet(jtis) {
		revoked, err := s.revocations.IsRevoked(ctx, jti)
		if err != nil {
			return fmt.Errorf("check delegation revocation: %w", err)
		}
		if revoked {
			return ErrRevoked
		}
	}
	return nil
}

func (s *TokenService) lookupVerificationKey(token *jwt.Token) (any, error) {
	if token == nil || token.Method == nil || token.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
		return nil, ErrBadSignature
	}
	kid := normalizeKeyID(headerString(token, "kid"))
	if kid == "" {
		return nil, ErrUnknownKeyId
	}
	key, ok := s.keyring[kid]
	if !ok {
		return nil, ErrUnknownKeyId
	}
	return key, nil
}

func headerString(token *jwt.Token, key string) string {
	if token == nil {
		return ""
	}
	raw, _ := token.Header[key].(string)
	return strings.TrimSpace(raw)
}

func verifiedTokenFromClaims(tokenStr, kid string, claims DelegationClaims) VerifiedToken {
	audience := ""
	if len(claims.Audience) > 0 {
		audience = claims.Audience[0]
	}
	return VerifiedToken{
		Token:           tokenStr,
		KID:             normalizeKeyID(kid),
		Subject:         claims.Subject,
		Audience:        audience,
		Tenant:          claims.Tenant,
		AllowedActions:  normalizeStringSet(claims.AllowedActions),
		AllowedTopics:   normalizeStringSet(claims.AllowedTopics),
		DelegationChain: cloneChain(claims.DelegationChain),
		ChainDepth:      claims.ChainDepth,
		JTI:             claims.ID,
		ParentTokenJTI:  claims.ParentTokenJTI,
		IssuedAt:        numericDateTime(claims.IssuedAt),
		NotBefore:       numericDateTime(claims.NotBefore),
		ExpiresAt:       numericDateTime(claims.ExpiresAt),
		Claims:          claims,
	}
}

func numericDateTime(value *jwt.NumericDate) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func normalizeStringSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func intersectScopes(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(right))
	for _, value := range right {
		allowed[value] = struct{}{}
	}
	out := make([]string, 0, len(left))
	for _, value := range left {
		if _, ok := allowed[value]; ok {
			out = append(out, value)
		}
	}
	return normalizeStringSet(out)
}

func isSubset(needles, haystack []string) bool {
	if len(needles) == 0 {
		return true
	}
	if len(haystack) == 0 {
		return false
	}
	allowed := make(map[string]struct{}, len(haystack))
	for _, value := range haystack {
		allowed[value] = struct{}{}
	}
	for _, value := range needles {
		if _, ok := allowed[value]; !ok {
			return false
		}
	}
	return true
}

func cloneChain(chain []ChainLink) []ChainLink {
	if len(chain) == 0 {
		return nil
	}
	out := make([]ChainLink, len(chain))
	copy(out, chain)
	return out
}

func newTokenJTI() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate delegation jti: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func mapJWTError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnknownKeyId):
		return ErrUnknownKeyId
	case errors.Is(err, ErrBadSignature):
		return ErrBadSignature
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ErrMalformed
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrExpired
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return ErrNotYetValid
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return ErrBadSignature
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return ErrAudienceMismatch
	default:
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}
}

func audienceContains(values jwt.ClaimStrings, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
