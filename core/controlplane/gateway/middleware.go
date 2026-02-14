package gateway

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
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

func newKeyedRateLimiterFromEnv() *keyedRateLimiter {
	rps := defaultRateLimitRPS
	burst := defaultRateLimitBurst
	if val := os.Getenv("API_RATE_LIMIT_RPS"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			rps = parsed
		}
	}
	if val := os.Getenv("API_RATE_LIMIT_BURST"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			burst = parsed
		}
	}
	return newKeyedRateLimiter(rps, burst)
}

func newPublicRateLimiterFromEnv() *keyedRateLimiter {
	rps := defaultPublicRateLimitRPS
	burst := defaultPublicRateLimitBurst
	if val := os.Getenv("API_PUBLIC_RATE_LIMIT_RPS"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			rps = parsed
		}
	}
	if val := os.Getenv("API_PUBLIC_RATE_LIMIT_BURST"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			burst = parsed
		}
	}
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

var apiLimiter = newKeyedRateLimiterFromEnv()
var publicLimiter = newPublicRateLimiterFromEnv()

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

func apiKeyUnaryInterceptor(auth AuthProvider) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if auth == nil {
			return handler(ctx, req)
		}
		authCtx, err := auth.AuthenticateGRPC(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "unauthorized")
		}
		ctx = context.WithValue(ctx, authContextKey{}, authCtx)
		return handler(ctx, req)
	}
}

var grpcPublicMethods = map[string]bool{
	"/grpc.health.v1.Health/Check": true,
	"/grpc.health.v1.Health/Watch": true,
}

func rateLimitUnaryInterceptor(auth AuthProvider) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if apiLimiter == nil && publicLimiter == nil {
			return handler(ctx, req)
		}
		if info == nil {
			return handler(ctx, req)
		}
		if grpcPublicMethods[info.FullMethod] {
			if publicLimiter != nil && !publicLimiter.Allow(grpcPublicRateLimitKey(ctx)) {
				return nil, status.Error(codes.ResourceExhausted, "rate limited")
			}
			return handler(ctx, req)
		}
		if apiLimiter != nil && !apiLimiter.Allow(grpcRateLimitKey(ctx)) {
			return nil, status.Error(codes.ResourceExhausted, "rate limited")
		}
		return handler(ctx, req)
	}
}

func grpcRateLimitKey(ctx context.Context) string {
	// SECURITY: The rate limiter runs after auth, so prefer the authenticated
	// tenant. Fall back to client IP if auth context is missing.
	if authCtx := authFromContext(ctx); authCtx != nil && strings.TrimSpace(authCtx.Tenant) != "" {
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

func rateLimitMiddleware(auth AuthProvider, next http.Handler) http.Handler {
	if apiLimiter == nil && publicLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/health" {
			if publicLimiter != nil && !publicLimiter.Allow(publicRateLimitKey(r)) {
				writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if isAllowedPublicPath(auth, r.URL.Path) {
			if publicLimiter != nil && !publicLimiter.Allow(publicRateLimitKey(r)) {
				writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if apiLimiter != nil && !apiLimiter.Allow(rateLimitKey(r)) {
			writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitKey(r *http.Request) string {
	// SECURITY: The rate limiter runs after auth, so prefer the authenticated
	// tenant. Fall back to client IP if auth context is missing.
	if authCtx := authFromRequest(r); authCtx != nil && strings.TrimSpace(authCtx.Tenant) != "" {
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
	"/api/v1/auth/config": true,
	"/api/v1/auth/login":  true,
}

// isAllowedPublicPath returns true only when BOTH the provider AND the
// hardcoded ceiling agree the path is public.
func isAllowedPublicPath(auth AuthProvider, path string) bool {
	if !maxPublicPaths[path] {
		return false
	}
	if pp, ok := auth.(PublicPathProvider); ok {
		return pp.IsPublicPath(path)
	}
	return false
}

// apiKeyMiddleware enforces API key auth and injects auth context.
func apiKeyMiddleware(auth AuthProvider, next http.Handler) http.Handler {
	if auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if isAllowedPublicPath(auth, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		authCtx, err := auth.AuthenticateHTTP(r)
		if err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func tenantMiddleware(auth AuthProvider, next http.Handler) http.Handler {
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
		if isAllowedPublicPath(auth, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		tenantID := tenantFromRequest(r)
		if tenantID == "" {
			writeErrorJSON(w, http.StatusForbidden, "tenant id required")
			return
		}
		if authCtx := authFromRequest(r); authCtx != nil && authCtx.Tenant != "" && !authCtx.AllowCrossTenant {
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
