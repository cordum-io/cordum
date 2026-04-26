package gateway

import (
	"bufio"
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/env"
	cordumotel "github.com/cordum/cordum/core/infra/otel"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	rateLimitKeyTTL          = 30 * time.Minute
	rateLimitCleanupInterval = 5 * time.Minute
)

type rateBucket struct {
	tokens float64
	last   time.Time
}

type keyedRateLimiter struct {
	mu          sync.Mutex
	rps         float64
	burst       float64
	buckets     map[string]*rateBucket
	nextCleanup time.Time
}

func newKeyedRateLimiter(rps, burst int) *keyedRateLimiter {
	if rps <= 0 || burst <= 0 {
		return nil
	}
	return &keyedRateLimiter{
		rps:     float64(rps),
		burst:   float64(burst),
		buckets: make(map[string]*rateBucket),
	}
}

// rateLimitFromEnv reads rate limit values from environment variables, falling
// back to the provided defaults when the env vars are unset or invalid.
func rateLimitFromEnv(rpsEnv, burstEnv string, defaultRPS, defaultBurst int) (int, int) {
	rps, burst := defaultRPS, defaultBurst
	if val := os.Getenv(rpsEnv); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			rps = parsed
		}
	}
	if val := os.Getenv(burstEnv); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			burst = parsed
		}
	}
	return rps, burst
}

func newKeyedRateLimiterFromEnvWithDefaults(defaultRPS, defaultBurst int) *keyedRateLimiter {
	rps, burst := rateLimitFromEnv("API_RATE_LIMIT_RPS", "API_RATE_LIMIT_BURST", defaultRPS, defaultBurst)
	return newKeyedRateLimiter(rps, burst)
}

func newPublicRateLimiterFromEnvWithDefaults(defaultRPS, defaultBurst int) *keyedRateLimiter {
	rps, burst := rateLimitFromEnv("API_PUBLIC_RATE_LIMIT_RPS", "API_PUBLIC_RATE_LIMIT_BURST", defaultRPS, defaultBurst)
	return newKeyedRateLimiter(rps, burst)
}

func (rl *keyedRateLimiter) Allow(key string) bool {
	if rl == nil {
		return true
	}
	if key == "" {
		key = "global"
	}
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.nextCleanup.IsZero() {
		rl.nextCleanup = now.Add(rateLimitCleanupInterval)
	}
	if now.After(rl.nextCleanup) {
		for k, bucket := range rl.buckets {
			if now.Sub(bucket.last) > rateLimitKeyTTL {
				delete(rl.buckets, k)
			}
		}
		rl.nextCleanup = now.Add(rateLimitCleanupInterval)
	}

	bucket := rl.buckets[key]
	if bucket == nil {
		bucket = &rateBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = bucket
	} else {
		elapsed := now.Sub(bucket.last).Seconds()
		if elapsed > 0 {
			bucket.tokens = math.Min(rl.burst, bucket.tokens+(elapsed*rl.rps))
		}
	}

	if bucket.tokens < 1 {
		bucket.last = now
		return false
	}
	bucket.tokens -= 1
	bucket.last = now
	return true
}

// rateLimitScript is an atomic Lua sliding-window counter.
// INCR the key; if it's the first increment, set an expiry.
// Returns the current count for the window.
var rateLimitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// redisRateLimiter enforces distributed rate limits via Redis sliding-window
// counters. Falls back to an in-memory keyedRateLimiter when Redis is
// unavailable.
type redisRateLimiter struct {
	client   redis.UniversalClient
	burst    int
	fallback *keyedRateLimiter
}

const (
	rateLimitRedisTimeout = 200 * time.Millisecond
	rateLimitKeyPrefix    = "cordum:rl:"
	rateLimitTTLSec       = 2 // 2× window for clock-skew safety
)

func newRedisRateLimiter(client redis.UniversalClient, rps, burst int) *redisRateLimiter {
	return &redisRateLimiter{
		client:   client,
		burst:    burst,
		fallback: newKeyedRateLimiter(rps, burst),
	}
}

// Allow checks whether the given key is within the rate limit. It runs a Lua
// script against Redis; if Redis is unavailable, it falls back to the
// in-memory token bucket.
func (rl *redisRateLimiter) Allow(key string) bool {
	if rl == nil {
		return true
	}
	if rl.client == nil {
		return rl.fallback.Allow(key)
	}

	now := time.Now().Unix()
	redisKey := fmt.Sprintf("%s%s:%d", rateLimitKeyPrefix, key, now)

	ctx, cancel := context.WithTimeout(context.Background(), rateLimitRedisTimeout)
	defer cancel()

	count, err := rateLimitScript.Run(ctx, rl.client, []string{redisKey}, rateLimitTTLSec).Int64()
	if err != nil {
		slog.Warn("redis rate limit unavailable, denying request", "error", err)
		return false
	}
	return count <= int64(rl.burst)
}

// rateLimiter is the interface satisfied by both redisRateLimiter and
// keyedRateLimiter, used by the middleware/interceptor functions.
type rateLimiter interface {
	Allow(key string) bool
}

func entitlementBodyBytesLimit(resolvers ...*licensing.EntitlementResolver) int64 {
	for _, resolver := range resolvers {
		if resolver == nil {
			continue
		}
		if limit := resolver.Entitlements().MaxBodyBytes; limit > 0 {
			return limit
		}
	}
	return maxJSONBodyBytes()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.TLS != nil && env.IsProduction() {
			w.Header().Set("Strict-Transport-Security", fmt.Sprintf("max-age=%d; includeSubDomains", hstsMaxAge()))
		}

		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !isAllowedOrigin(r) {
				writeErrorJSON(w, http.StatusForbidden, "origin not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Tenant-ID, X-Principal-Id, X-Principal-Role, X-Request-Id, Idempotency-Key, X-Idempotency-Key")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id, X-Trace-Id")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAllowedOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser clients often omit Origin; treat as allowed.
		return true
	}

	allowed, allowAll := allowedOriginsFromEnv()
	if allowAll {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	if len(allowed) == 0 {
		host := strings.ToLower(u.Hostname())
		switch host {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
		reqHost := strings.ToLower(requestHostname(r.Host))
		if reqHost != "" && host == reqHost {
			return true
		}
		return false
	}

	_, ok := allowed[origin]
	return ok
}

func allowedOriginsFromEnv() (map[string]struct{}, bool) {
	for _, key := range []string{
		"CORDUM_ALLOWED_ORIGINS",
		"CORDUM_CORS_ALLOW_ORIGINS",
		"CORS_ALLOW_ORIGINS",
	} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		if raw == "*" {
			return nil, true
		}
		set := make(map[string]struct{})
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			set[p] = struct{}{}
		}
		return set, false
	}
	return nil, false
}

func requestHostname(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostport); err == nil && host != "" {
		return host
	}
	return hostport
}

func apiKeyUnaryInterceptor(provider auth.AuthProvider) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if provider == nil {
			return handler(ctx, req)
		}
		authCtx, err := provider.AuthenticateGRPC(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "unauthorized")
		}
		ctx = context.WithValue(ctx, auth.ContextKey{}, authCtx)
		return handler(ctx, req)
	}
}

var grpcPublicMethods = map[string]bool{
	"/grpc.health.v1.Health/Check": true,
	"/grpc.health.v1.Health/Watch": true,
}

func rateLimitUnaryInterceptor(auth auth.AuthProvider, apiRL, publicRL rateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if apiRL == nil && publicRL == nil {
			return handler(ctx, req)
		}
		if info == nil {
			return handler(ctx, req)
		}
		if grpcPublicMethods[info.FullMethod] {
			if publicRL != nil && !publicRL.Allow(grpcPublicRateLimitKey(ctx)) {
				return nil, status.Error(codes.ResourceExhausted, "rate limited")
			}
			return handler(ctx, req)
		}
		if apiRL != nil && !apiRL.Allow(grpcRateLimitKey(ctx)) {
			return nil, status.Error(codes.ResourceExhausted, "rate limited")
		}
		return handler(ctx, req)
	}
}

func grpcRateLimitKey(ctx context.Context) string {
	// SECURITY: The rate limiter runs after auth, so prefer the authenticated
	// tenant. Fall back to client IP if auth context is missing.
	if authCtx := auth.FromContext(ctx); authCtx != nil && strings.TrimSpace(authCtx.Tenant) != "" {
		return "tenant:" + strings.TrimSpace(authCtx.Tenant)
	}
	if ip := grpcClientIP(ctx); ip != "" {
		return "ip:" + ip
	}
	return "ip:unknown"
}

func grpcPublicRateLimitKey(ctx context.Context) string {
	if ip := grpcClientIP(ctx); ip != "" {
		return "ip:" + ip
	}
	return "ip:unknown"
}

func grpcClientIP(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	peerInfo, ok := peer.FromContext(ctx)
	if !ok || peerInfo == nil || peerInfo.Addr == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(peerInfo.Addr.String())); err == nil && host != "" {
		return host
	}
	if tcpAddr, ok := peerInfo.Addr.(*net.TCPAddr); ok && tcpAddr.IP != nil {
		return tcpAddr.IP.String()
	}
	return strings.TrimSpace(peerInfo.Addr.String())
}

func rateLimitMiddleware(auth auth.AuthProvider, apiRL, publicRL rateLimiter, next http.Handler) http.Handler {
	if apiRL == nil && publicRL == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/health" {
			if publicRL != nil && !publicRL.Allow(publicRateLimitKey(r)) {
				writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/mcp/") {
			next.ServeHTTP(w, r)
			return
		}
		if isAllowedPublicPath(auth, r.URL.Path) {
			if publicRL != nil && !publicRL.Allow(publicRateLimitKey(r)) {
				writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if apiRL != nil && !apiRL.Allow(rateLimitKey(r)) {
			writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitKey(r *http.Request) string {
	// SECURITY: The rate limiter runs after auth, so prefer the authenticated
	// tenant. Fall back to client IP if auth context is missing.
	if authCtx := auth.FromRequest(r); authCtx != nil && strings.TrimSpace(authCtx.Tenant) != "" {
		return "tenant:" + strings.TrimSpace(authCtx.Tenant)
	}
	if ip := clientIP(r); ip != "" {
		return "ip:" + ip
	}
	return "ip:unknown"
}

func publicRateLimitKey(r *http.Request) string {
	if ip := clientIP(r); ip != "" {
		return "ip:" + ip
	}
	return "ip:unknown"
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// maxPublicPaths is the hardcoded ceiling of paths that may be public.
// Even if a PublicPathProvider claims a path is public, it must be in this
// set. This prevents a buggy or malicious provider from bypassing auth on
// sensitive endpoints.
var maxPublicPaths = map[string]bool{
	"/api/v1/auth/config":            true,
	"/api/v1/auth/login":             true,
	"/api/v1/auth/sso/oidc/login":    true,
	"/api/v1/auth/sso/oidc/callback": true,
	"/api/v1/auth/sso/saml/metadata": true,
	"/api/v1/auth/sso/saml/login":    true,
	"/api/v1/auth/sso/saml/acs":      true,
}

var maxPublicPathPrefixes = []string{
	auth.SCIMBasePath,
}

func publicPathWithinCeiling(path string) bool {
	if maxPublicPaths[path] {
		return true
	}
	for _, prefix := range maxPublicPathPrefixes {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// isAllowedPublicPath returns true only when BOTH the provider AND the
// hardcoded ceiling agree the path is public.
func isAllowedPublicPath(provider auth.AuthProvider, path string) bool {
	if !publicPathWithinCeiling(path) {
		return false
	}
	if pp, ok := provider.(auth.PublicPathProvider); ok {
		return pp.IsPublicPath(path)
	}
	return false
}

// apiKeyMiddleware enforces API key auth and injects auth context.
// An optional auditSender emits audit events on authentication failures.
func apiKeyMiddleware(provider auth.AuthProvider, next http.Handler, auditSender ...audit.AuditSender) http.Handler {
	if provider == nil {
		return next
	}
	var aSender audit.AuditSender
	if len(auditSender) > 0 {
		aSender = auditSender[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if isAllowedPublicPath(provider, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		authCtx, err := provider.AuthenticateHTTP(r)
		if err != nil {
			var scopeErr *auth.ScopeError
			if errors.As(err, &scopeErr) {
				if aSender != nil {
					aSender.Send(audit.SIEMEvent{
						Timestamp: time.Now().UTC(),
						EventType: audit.EventSystemAuth,
						Severity:  audit.SeverityMedium,
						Action:    "auth.failure",
						Reason:    "key_scope_insufficient",
						Extra: map[string]string{
							"source_ip":   clientIP(r),
							"auth_method": "middleware",
							"path":        r.URL.Path,
						},
					})
				}
				writeKeyScopeInsufficient(w, scopeErr)
				return
			}
			if aSender != nil {
				aSender.Send(audit.SIEMEvent{
					Timestamp: time.Now().UTC(),
					EventType: audit.EventSystemAuth,
					Severity:  audit.SeverityMedium,
					Action:    "auth.failure",
					Reason:    "request_auth_failed",
					Extra: map[string]string{
						"source_ip":   clientIP(r),
						"auth_method": "middleware",
						"path":        r.URL.Path,
					},
				})
			}
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		loggerFromContext(r.Context()).Debug("auth resolved",
			"tenant", authCtx.Tenant,
			"principal", authCtx.PrincipalID,
			"authSource", string(authCtx.AuthSource),
		)
		ctx := context.WithValue(r.Context(), auth.ContextKey{}, authCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeKeyScopeInsufficient(w http.ResponseWriter, scopeErr *auth.ScopeError) {
	granted := []string{}
	required := ""
	if scopeErr != nil {
		granted = append(granted, scopeErr.Granted...)
		required = scopeErr.Required
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	writeJSON(w, map[string]any{
		"error":  "forbidden",
		"status": http.StatusForbidden,
		"reason": "key_scope_insufficient",
		"details": map[string]any{
			"required": required,
			"granted":  granted,
		},
	})
}

// sensitiveReadPaths are read endpoints that are always audited regardless of
// sample rate. These are security-sensitive paths where access should be tracked.
var sensitiveReadPaths = []string{
	"/api/v1/policy",
	"/api/v1/users",
	"/api/v1/auth",
	"/api/v1/approvals",
}

// isSensitiveRead returns true if the request is a GET to a security-sensitive path.
func isSensitiveRead(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	for _, prefix := range sensitiveReadPaths {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return true
		}
	}
	return false
}

// auditReadMiddleware emits audit events for read access to API endpoints.
// The sampleRate controls how often non-sensitive reads are audited (0.0 = off, 1.0 = all).
// Sensitive reads (policy, users, approvals) are always audited regardless of rate.
func auditReadMiddleware(sender audit.AuditSender, sampleRate float64, next http.Handler) http.Handler {
	if sender == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only audit GET requests to API paths.
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		mandatory := isSensitiveRead(r)
		shouldAudit := mandatory

		if !shouldAudit && sampleRate > 0 {
			// Use crypto/rand for unbiased sampling.
			var b [1]byte
			if _, err := crand.Read(b[:]); err == nil {
				if float64(b[0])/256.0 < sampleRate {
					shouldAudit = true
				}
			}
		}

		if shouldAudit {
			authCtx, _ := r.Context().Value(auth.ContextKey{}).(*auth.AuthContext)
			identity := ""
			tenantID := ""
			if authCtx != nil {
				identity = authCtx.PrincipalID
				tenantID = authCtx.Tenant
			}
			sender.Send(audit.SIEMEvent{
				Timestamp: time.Now().UTC(),
				EventType: audit.EventSystemAuth,
				Severity:  audit.SeverityInfo,
				TenantID:  tenantID,
				Action:    "data.read",
				Identity:  identity,
				Extra: map[string]string{
					"source_ip": clientIP(r),
					"path":      r.URL.Path,
					"mandatory": fmt.Sprintf("%t", mandatory),
				},
			})
		}

		next.ServeHTTP(w, r)
	})
}

func tenantMiddleware(provider auth.AuthProvider, next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if isAllowedPublicPath(provider, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		tenantID := tenantFromRequest(r)
		if tenantID == "" {
			writeErrorJSON(w, http.StatusForbidden, "tenant id required")
			return
		}
		loggerFromContext(r.Context()).Debug("tenant resolved", "tenantId", tenantID)
		if authCtx := auth.FromRequest(r); authCtx != nil && authCtx.Tenant != "" && !authCtx.AllowCrossTenant {
			if strings.TrimSpace(authCtx.Tenant) != tenantID {
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hstsMaxAge() int64 {
	const defaultHSTSMaxAge int64 = 63072000
	if raw := strings.TrimSpace(os.Getenv("CORDUM_HSTS_MAX_AGE")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 0 {
			return v
		}
	}
	return defaultHSTSMaxAge
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack forwards websocket hijacking support to the underlying writer when available.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacker not supported")
	}
	return hj.Hijack()
}

// Flush preserves streaming support if the wrapped writer implements it.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// loggerKey is the context key for the request-scoped logger.
type loggerKey struct{}

// requestIdKey is the context key for the request ID string.
type requestIdKey struct{}

// requestIdFromContext returns the request ID from the context, or empty string.
func requestIdFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIdKey{}).(string); ok {
		return id
	}
	return ""
}

// loggerFromContext returns the request-scoped *slog.Logger, falling back to
// slog.Default() if no logger is attached.
func loggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// requestLoggingMiddleware injects a request-scoped logger into context and
// logs request entry (debug) and completion (info) with method, path, status,
// and duration.
func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Echo X-Request-Id in all responses so callers can correlate.
		w.Header().Set("X-Request-Id", requestID)

		logger := slog.Default().With(
			"requestId", requestID,
			"method", r.Method,
			"path", r.URL.Path,
		)

		logger.Debug("request received", "remoteAddr", r.RemoteAddr)

		ctx := context.WithValue(r.Context(), loggerKey{}, logger)
		ctx = context.WithValue(ctx, requestIdKey{}, requestID)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(rec, r.WithContext(ctx))

		duration := time.Since(start)
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/mcp/") {
			logger.Info("request completed",
				"status", rec.status,
				"duration", duration.String(),
			)
		}
	})
}

// generateRequestID creates a short random request ID using crypto/rand.
func generateRequestID() string {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
}

// tracingMiddleware creates an OTEL span per HTTP request. When OTEL is
// disabled the middleware is a no-op passthrough with zero overhead.
// Slots into the middleware chain AFTER requestLoggingMiddleware so that
// request ID and logger are already available in the context.
func tracingMiddleware(next http.Handler) http.Handler {
	if !cordumotel.Enabled() {
		return next
	}
	return tracingMiddlewareWithProvider(cordumotel.Provider(), next)
}

// tracingMiddlewareWithProvider creates tracing middleware using the given provider.
// Exposed for testing with in-memory span exporters.
func tracingMiddlewareWithProvider(tp oteltrace.TracerProvider, next http.Handler) http.Handler {
	tracer := tp.Tracer("cordum-gateway")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanName := r.Method + " " + r.URL.Path
		ctx, span := tracer.Start(r.Context(), spanName,
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		)
		defer span.End()

		span.SetAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.url", r.URL.String()),
			attribute.String("http.user_agent", r.UserAgent()),
		)

		if reqID := requestIdFromContext(ctx); reqID != "" {
			span.SetAttributes(attribute.String("cordum.request_id", reqID))
		}
		if tenant := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); tenant != "" {
			span.SetAttributes(attribute.String("cordum.tenant", tenant))
		}
		if ac := auth.FromRequest(r); ac != nil && ac.PrincipalID != "" {
			span.SetAttributes(attribute.String("cordum.principal_id", ac.PrincipalID))
		}
		// Agent identity is resolved authoritatively at the handler level
		// (via resolveAgentForAudit) and written onto the span there.
		// Do NOT read X-Agent-ID header here — it is client-controlled and spoofable.

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.status_code", rec.status))
		if rec.status >= 500 {
			span.SetStatus(otelcodes.Error, http.StatusText(rec.status))
		}
	})
}
