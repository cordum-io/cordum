package scheduler

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/tests/identity"
	"github.com/redis/go-redis/v9"
)

const (
	matrixJWTAudience   = "cordum-api"
	matrixWrongAudience = "cordum-gateway"
	matrixJWTIssuer     = "cordum-matrix"
	matrixOIDCKeyID     = "matrix-oidc-key"
)

// matrixKit mints every identity.Credential with its real issuer and keys,
// all naming one enrolled worker so the credential is the only difference.
type matrixKit struct {
	issuer     *SessionTokenIssuer
	rdb        *redis.Client
	binding    SessionBinding // the worker's bound session, audience WorkerHandshakeAudience
	credential string         // the worker's bearer from workercredentials.Service.Create
}

// newMatrixKit enrolls the worker through workercredentials.Service.Create
// with its registered P-256 proof key, pools and topics.
func newMatrixKit(t *testing.T, issuer *SessionTokenIssuer, rdb redis.UniversalClient, binding SessionBinding, proofKey *ecdsa.PublicKey, pools, topics []string) *matrixKit {
	t.Helper()
	client, ok := rdb.(*redis.Client)
	if !ok {
		t.Fatalf("matrix kit needs a *redis.Client, got %T", rdb)
	}
	der, err := x509.MarshalPKIXPublicKey(proofKey)
	if err != nil {
		t.Fatalf("marshal proof key: %v", err)
	}
	issued, err := workercredentials.NewService(configsvc.NewFromClient(client)).Create(context.Background(), workercredentials.IssueInput{
		TenantID: binding.Tenant, WorkerID: binding.WorkerID, AgentID: binding.AgentID,
		ProofKeyID: binding.ProofKeyID, ProofAlgorithm: workercredentials.ProofAlgorithmECDSAP256SHA256,
		ProofPublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
		AllowedPools:      pools, AllowedTopics: topics, CreatedBy: "audience-matrix",
	})
	if err != nil {
		t.Fatalf("enroll worker credential: %v", err)
	}
	return &matrixKit{issuer: issuer, rdb: client, binding: binding, credential: issued.Token}
}

// token returns the auth_token that carries credential c. Anonymous and
// agent_id_header carry none: the latter is the agent id as sender alone.
func (k *matrixKit) token(t *testing.T, c identity.Credential) string {
	t.Helper()
	switch c {
	case identity.Anonymous, identity.AgentIDHeader:
		return ""
	case identity.UserSession:
		return k.userSession(t)
	case identity.APIKeyStatic:
		return staticAPIKey(t, k.binding.Tenant)
	case identity.APIKeyManaged:
		return k.managedAPIKey(t)
	case identity.GatewayJWT, identity.GatewayJWTWrongAudience:
		return k.gatewayJWT(t, c == identity.GatewayJWT)
	case identity.OIDCBearer, identity.OIDCBearerWrongAudience:
		return k.oidcBearer(t, c == identity.OIDCBearer)
	case identity.DelegationToken:
		return k.delegationToken(t)
	case identity.ServiceToken:
		token, err := servicetoken.MintService(k.issuer.privateKey, k.issuer.keyID, servicetoken.IdentityGateway, time.Now())
		return mustMint(t, c, token, err)
	case identity.WorkerCredential:
		return k.credential
	case identity.WorkerSession, identity.WorkerSessionWrongAudience:
		binding := k.binding
		if c == identity.WorkerSessionWrongAudience {
			binding.Audience = matrixWrongAudience
		}
		token, _, err := k.issuer.IssueBound(context.Background(), binding)
		return mustMint(t, c, token, err)
	case identity.SCIMBearer:
		return scimBearer(t)
	case identity.AgentdNonce:
		server, err := agentd.NewLocalServer(agentd.LocalServerConfig{})
		if err != nil {
			t.Fatalf("agentd local server: %v", err)
		}
		return server.Nonce()
	}
	t.Fatalf("matrix: no issuer for credential %q", c)
	return ""
}

// sender is the packet sender for c: agent_id_header claims the worker's agent id.
func (k *matrixKit) sender(c identity.Credential, normal string) string {
	if c == identity.AgentIDHeader {
		return k.binding.AgentID
	}
	return normal
}

func mustMint(t *testing.T, c identity.Credential, token string, err error) string {
	t.Helper()
	if err != nil || token == "" {
		t.Fatalf("mint %s: token empty=%v err=%v", c, token == "", err)
	}
	return token
}

// userSession is a gateway login session, stored the way handlers_auth.go stores it.
func (k *matrixKit) userSession(t *testing.T) string {
	t.Helper()
	token := "session-" + randomMatrixValue(t) // the auth/saml.go newSessionToken shape
	user := &auth.User{ID: k.binding.WorkerID, Username: k.binding.WorkerID, Tenant: k.binding.Tenant, Role: "admin"}
	err := auth.NewRedisUserStoreFromClient(k.rdb).StoreSession(context.Background(), token, user, time.Hour)
	return mustMint(t, identity.UserSession, token, err)
}

// staticAPIKey is a key parsed the way the gateway parses CORDUM_API_KEYS.
func staticAPIKey(t *testing.T, tenant string) string {
	t.Helper()
	raw, err := auth.GenerateRawKey()
	if err != nil {
		t.Fatalf("generate static key: %v", err)
	}
	entries, err := auth.ParseAPIKeys(tenant + ":" + raw + ":admin")
	if err != nil || len(entries) != 1 || entries[0].Key != raw {
		t.Fatalf("parse CORDUM_API_KEYS: entries=%d err=%v", len(entries), err)
	}
	return entries[0].Key
}

func (k *matrixKit) managedAPIKey(t *testing.T) string {
	t.Helper()
	raw, err := auth.GenerateRawKey()
	if err != nil {
		t.Fatalf("generate managed key: %v", err)
	}
	key := &auth.ManagedKey{Name: "audience-matrix", Tenant: k.binding.Tenant, Scopes: []string{}, CreatedAt: time.Now().UTC()}
	err = auth.NewRedisKeyStoreFromClient(k.rdb).Create(context.Background(), key, raw)
	return mustMint(t, identity.APIKeyManaged, raw, err)
}

// gatewayJWT is an HS256 token under the gateway's CORDUM_JWT_* config; the
// gateway's own validator must accept it exactly when the audience is right.
func (k *matrixKit) gatewayJWT(t *testing.T, rightAudience bool) string {
	t.Helper()
	secret := []byte(randomMatrixValue(t))
	t.Setenv("CORDUM_JWT_HMAC_SECRET", string(secret))
	t.Setenv("CORDUM_JWT_ISSUER", matrixJWTIssuer)
	t.Setenv("CORDUM_JWT_AUDIENCE", matrixJWTAudience)
	token := signMatrixJWT(t, map[string]string{"alg": "HS256", "typ": "JWT"}, k.jwtClaims(matrixJWTIssuer, rightAudience), func(input []byte) ([]byte, error) {
		mac := hmac.New(sha256.New, secret)
		mac.Write(input)
		return mac.Sum(nil), nil
	})
	validator, _, err := auth.NewReloadableJWTValidator()
	if err == nil {
		_, err = validator.Validate(token)
	}
	requireHomeVerdict(t, identity.GatewayJWT, rightAudience, err)
	return token
}

// oidcBearer is an RS256 token whose key an httptest JWKS serves; the gateway's
// OIDC provider must accept it exactly when the audience is right.
func (k *matrixKit) oidcBearer(t *testing.T, rightAudience bool) string {
	t.Helper()
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC key: %v", err)
	}
	idp := httptest.NewServer(oidcMatrixMux(&key.PublicKey))
	t.Cleanup(idp.Close)
	token := signMatrixJWT(t, map[string]string{"alg": "RS256", "typ": "JWT", "kid": matrixOIDCKeyID}, k.jwtClaims(idp.URL, rightAudience), func(input []byte) ([]byte, error) {
		sum := sha256.Sum256(input)
		return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	})
	provider, err := auth.NewOIDCProvider(auth.OIDCConfig{Enabled: true, IssuerURL: idp.URL, Audience: matrixJWTAudience})
	if err == nil {
		t.Cleanup(provider.Close)
		_, err = provider.ValidateJWT(token)
	}
	requireHomeVerdict(t, identity.OIDCBearer, rightAudience, err)
	return token
}

func oidcMatrixMux(key *rsa.PublicKey) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": base, "jwks_uri": base + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": matrixOIDCKeyID, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	return mux
}

// jwtClaims names the enrolled worker, so only the token type and audience differ.
func (k *matrixKit) jwtClaims(issuer string, rightAudience bool) map[string]any {
	aud := matrixJWTAudience
	if !rightAudience {
		aud = matrixWrongAudience
	}
	now := time.Now()
	return map[string]any{
		"iss": issuer, "aud": aud, "sub": k.binding.WorkerID, "org_id": k.binding.Tenant, "tenant": k.binding.Tenant,
		"role": "admin", "cordum_role": "admin",
		"iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func signMatrixJWT(t *testing.T, header map[string]string, claims map[string]any, sign func([]byte) ([]byte, error)) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	input := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	sig, err := sign([]byte(input))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// requireHomeVerdict proves a test-signed token is the real thing: its home
// verifier accepts the right audience and refuses the wrong one.
func requireHomeVerdict(t *testing.T, c identity.Credential, rightAudience bool, err error) {
	t.Helper()
	if (err == nil) != rightAudience {
		t.Fatalf("%s home verifier: err=%v, want accepted=%v", c, err, rightAudience)
	}
}

func (k *matrixKit) delegationToken(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate delegation key: %v", err)
	}
	service, err := delegation.NewTokenService(delegation.SigningKey{KID: "matrix-delegation", PrivateKey: priv}, nil, nil, nil)
	if err != nil {
		t.Fatalf("delegation token service: %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant: k.binding.Tenant, DelegatingAgentID: "agent-delegator", TargetAgentID: k.binding.AgentID,
	})
	return mustMint(t, identity.DelegationToken, token, err)
}

// scimBearer is the operator SCIM token: scim.go reads CORDUM_SCIM_BEARER_TOKEN
// (rotation, the other source, needs a licensed SCIM entitlement), in the
// shape scim.go's newSCIMBearerToken generates.
func scimBearer(t *testing.T) string {
	t.Helper()
	token := "scim-" + randomMatrixValue(t)
	t.Setenv("CORDUM_SCIM_BEARER_TOKEN", token)
	return token
}

func randomMatrixValue(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// matrixProductionMode sets the production profile the way cordum-scheduler
// boots it: handshake enforce, set explicitly, with WORKER_ATTESTATION off
// (worker_security_modes.go refuses both active), and returns the parsed mode.
func matrixProductionMode(t *testing.T) HandshakeMode {
	t.Helper()
	t.Setenv(EnvHandshakeMode, "enforce")
	t.Setenv(EnvWorkerAttestation, "off")
	mode, err := ParseHandshakeModeStrict(os.Getenv(EnvHandshakeMode))
	attestation, attErr := ParseWorkerAttestationModeStrict(os.Getenv(EnvWorkerAttestation))
	if err != nil || attErr != nil || mode != HandshakeModeEnforce || attestation.Enabled() {
		t.Fatalf("production profile: mode=%q err=%v attestation=%q err=%v", mode, err, attestation, attErr)
	}
	return mode
}
