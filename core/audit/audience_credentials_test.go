package audit_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/policysign"
	"github.com/cordum/cordum/tests/identity"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Each presenter mints its credential with its issuer, independently of the
// target's Redis. Issuing a credential cannot count as the target's effect.
func audienceCredential(t *testing.T, c identity.Credential) string {
	t.Helper()
	switch c {
	case identity.Anonymous:
		return ""
	case identity.AgentIDHeader:
		return "matrix-agent"
	case identity.APIKeyStatic, identity.SCIMBearer:
		token := audienceRandom(t)
		name := "CORDUM_API_KEYS"
		if c == identity.SCIMBearer {
			name = "CORDUM_SCIM_BEARER_TOKEN"
		}
		t.Setenv(name, token)
		return token
	case identity.UserSession, identity.APIKeyManaged:
		return audienceStoredCredential(t, c)
	case identity.GatewayJWT, identity.GatewayJWTWrongAudience,
		identity.OIDCBearer, identity.OIDCBearerWrongAudience:
		return audienceJWT(t, c)
	case identity.DelegationToken:
		return audienceDelegation(t)
	case identity.ServiceToken, identity.WorkerSession, identity.WorkerSessionWrongAudience:
		return audienceSignedCredential(t, c)
	case identity.WorkerCredential:
		return audienceWorkerCredential(t)
	case identity.AgentdNonce:
		srv, err := agentd.NewLocalServer(agentd.LocalServerConfig{})
		require.NoError(t, err)
		return srv.Nonce()
	default:
		t.Fatalf("unhandled credential %q", c)
		return ""
	}
}

func audienceRandom(t *testing.T) string {
	t.Helper()
	var b [32]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func audienceIssuerRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	return rdb, "redis://" + mr.Addr()
}

func audienceStoredCredential(t *testing.T, c identity.Credential) string {
	t.Helper()
	rdb, _ := audienceIssuerRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c == identity.UserSession {
		// Same random session format as auth.newSessionToken; StoreSession is
		// the issuer's exported persistence seam used by the matrix contract.
		token := "session-" + audienceRandom(t)
		users := auth.NewRedisUserStoreFromClient(rdb)
		require.NoError(t, users.StoreSession(ctx, token, &auth.User{
			ID: "matrix-user", Username: "matrix-user", Tenant: "default", Role: "admin",
		}, time.Hour))
		return token
	}
	token, err := auth.GenerateRawKey()
	require.NoError(t, err)
	keys := auth.NewRedisKeyStoreFromClient(rdb)
	require.NoError(t, keys.Create(ctx, &auth.ManagedKey{
		Name: "matrix", Tenant: "default", Scopes: []string{"admin"}, CreatedAt: time.Now(),
	}, token))
	return token
}

func audienceJWT(t *testing.T, c identity.Credential) string {
	t.Helper()
	// JWT and OIDC are externally issued. Use a real RSA issuer key, as
	// auth's OIDC test issuer does, with the configured issuer and audience.
	issuer, audience, prefix := "https://gateway-issuer.example", "cordum-api", "CORDUM_JWT_"
	if c == identity.OIDCBearer || c == identity.OIDCBearerWrongAudience {
		issuer, audience, prefix = "https://oidc-issuer.example", "cordum-oidc", "CORDUM_OIDC_"
	}
	t.Setenv(prefix+"ISSUER", issuer)
	t.Setenv(prefix+"AUDIENCE", audience)
	if c == identity.GatewayJWTWrongAudience || c == identity.OIDCBearerWrongAudience {
		audience = "unrelated-service"
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	claims := jwt.RegisteredClaims{Issuer: issuer, Subject: "matrix-user",
		Audience: jwt.ClaimStrings{audience}, IssuedAt: jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "matrix-issuer"
	raw, err := token.SignedString(key)
	require.NoError(t, err)
	return raw
}

func audienceDelegation(t *testing.T) string {
	t.Helper()
	key, err := delegation.GenerateSigningKey("matrix-delegation")
	require.NoError(t, err)
	svc, err := delegation.NewTokenService(key, nil, nil, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, _, err := svc.IssueDelegationToken(ctx, delegation.IssueRequest{
		Tenant: "default", DelegatingAgentID: "matrix-agent", TargetAgentID: "matrix-target",
	})
	require.NoError(t, err)
	return token
}

func audienceSignedCredential(t *testing.T, c identity.Credential) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	if c == identity.ServiceToken {
		token, err := servicetoken.MintService(priv, "matrix-service", servicetoken.IdentityGateway, time.Now())
		require.NoError(t, err)
		return token
	}
	rdb, _ := audienceIssuerRedis(t)
	trust := policysign.NewTrustStore()
	require.NoError(t, trust.Add("matrix-session", pub))
	issuer, err := scheduler.NewSessionTokenIssuer(priv, "matrix-session", trust, rdb, scheduler.SessionTokenIssuerOptions{})
	require.NoError(t, err)
	audience := "cordum-scheduler"
	if c == identity.WorkerSessionWrongAudience {
		audience = "unrelated-service"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, _, err := issuer.IssueBound(ctx, scheduler.SessionBinding{
		WorkerID: "matrix-worker", AgentID: "matrix-agent", Tenant: "default",
		Audience: audience, ProofKeyID: "matrix-proof", SDKVersion: "v2.17.0",
	})
	require.NoError(t, err)
	return token
}

func audienceWorkerCredential(t *testing.T) string {
	t.Helper()
	_, redisURL := audienceIssuerRedis(t)
	cfg, err := configsvc.New(redisURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cfg.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	issued, err := workercredentials.NewService(cfg).Create(ctx, workercredentials.IssueInput{
		WorkerID: "matrix-worker", AgentID: "matrix-agent", TenantID: "default",
	})
	require.NoError(t, err)
	return issued.Token
}
