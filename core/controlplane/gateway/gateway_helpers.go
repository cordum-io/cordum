package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/memory"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
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

type submitJobRequest struct {
	Prompt             string            `json:"prompt"`
	Topic              string            `json:"topic"`
	AdapterId          string            `json:"adapter_id"`
	Priority           string            `json:"priority"`
	Context            any               `json:"context"`
	MemoryId           string            `json:"memory_id"`
	Mode               string            `json:"context_mode"`
	TenantId           string            `json:"tenant_id"`
	PrincipalId        string            `json:"principal_id"`
	ActorId            string            `json:"actor_id"`
	ActorType          string            `json:"actor_type"`
	IdempotencyKey     string            `json:"idempotency_key"`
	PackId             string            `json:"pack_id"`
	Capability         string            `json:"capability"`
	RiskTags           []string          `json:"risk_tags"`
	Requires           []string          `json:"requires"`
	OrgId              string            `json:"org_id"`
	TeamId             string            `json:"team_id"`
	ProjectId          string            `json:"project_id"`
	Labels             map[string]string `json:"labels"`
	MaxInputTokens     int32             `json:"max_input_tokens"`
	AllowSummarization bool              `json:"allow_summarization"`
	AllowRetrieval     bool              `json:"allow_retrieval"`
	Tags               []string          `json:"tags"`
	MaxOutputTokens    int64             `json:"max_output_tokens"`
	MaxTotalTokens     int64             `json:"max_total_tokens"`
	DeadlineMs         int64             `json:"deadline_ms"`
}

type policyMetaRequest struct {
	TenantId       string            `json:"tenant_id"`
	ActorId        string            `json:"actor_id"`
	ActorType      string            `json:"actor_type"`
	IdempotencyKey string            `json:"idempotency_key"`
	Capability     string            `json:"capability"`
	RiskTags       []string          `json:"risk_tags"`
	Requires       []string          `json:"requires"`
	PackId         string            `json:"pack_id"`
	Labels         map[string]string `json:"labels"`
}

type policyCheckRequest struct {
	JobId           string             `json:"job_id"`
	Topic           string             `json:"topic"`
	Tenant          string             `json:"tenant"`
	OrgId           string             `json:"org_id"`
	TeamId          string             `json:"team_id"`
	WorkflowId      string             `json:"workflow_id"`
	StepId          string             `json:"step_id"`
	PrincipalId     string             `json:"principal_id"`
	Priority        string             `json:"priority"`
	EstimatedCost   float64            `json:"estimated_cost"`
	Budget          *pb.Budget         `json:"budget"`
	Labels          map[string]string  `json:"labels"`
	MemoryId        string             `json:"memory_id"`
	EffectiveConfig any                `json:"effective_config"`
	Meta            *policyMetaRequest `json:"meta"`
}

func (r *submitJobRequest) applyDefaults(defaultTenant string) {
	if r.MaxInputTokens == 0 {
		r.MaxInputTokens = 8000
	}
	if r.MaxOutputTokens == 0 {
		r.MaxOutputTokens = 1024
	}
	if r.Topic == "" {
		r.Topic = "job.default"
	}
	// Prioritize OrgId, then TenantId, then default
	if r.OrgId == "" {
		if r.TenantId != "" {
			r.OrgId = r.TenantId
		} else {
			r.OrgId = defaultTenant
		}
	}
	r.TenantId = r.OrgId // Ensure TenantId is consistent with OrgId
}

func (r *submitJobRequest) validate(defaultTenant string) error {
	if r == nil {
		return errors.New("request required")
	}
	if len(r.Prompt) == 0 {
		return errors.New("prompt is required")
	}
	if len(r.Prompt) > maxPromptChars {
		return fmt.Errorf("prompt too long (>%d chars)", maxPromptChars)
	}
	if r.Topic == "" {
		return errors.New("topic is required")
	}
	// SECURITY: Strict topic validation to prevent injection attacks
	if !validTopicRegex.MatchString(r.Topic) {
		return errors.New("invalid topic format: must match job.name.segments (alphanumeric, dots, hyphens, underscores only)")
	}
	if len(r.Topic) > 256 {
		return errors.New("topic too long (max 256 chars)")
	}
	if r.MaxInputTokens < 0 || r.MaxOutputTokens < 0 || r.MaxTotalTokens < 0 {
		return errors.New("token limits must be non-negative")
	}
	if r.DeadlineMs < 0 {
		return errors.New("deadline_ms must be non-negative")
	}
	if r.ActorType != "" && parseActorType(r.ActorType) == pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		return errors.New("actor_type must be 'human' or 'service'")
	}
	if len(r.Tags) > 50 {
		return errors.New("too many tags (max 50)")
	}
	if len(r.Labels) > 50 {
		return errors.New("too many labels (max 50)")
	}
	// SECURITY: Validate label key and value lengths to prevent DoS
	for k, v := range r.Labels {
		if len(k) > maxLabelKeyLen {
			return fmt.Errorf("label key too long (max %d chars)", maxLabelKeyLen)
		}
		if len(v) > maxLabelValueLen {
			return fmt.Errorf("label value too long (max %d chars)", maxLabelValueLen)
		}
	}
	if r.OrgId == "" {
		if r.TenantId != "" {
			r.OrgId = r.TenantId
		} else {
			r.OrgId = defaultTenant
		}
	}
	return nil
}

func buildJobMetadata(metaReq *policyMetaRequest, tenant, principal string) *pb.JobMetadata {
	if metaReq == nil && tenant == "" && principal == "" {
		return nil
	}
	meta := &pb.JobMetadata{
		TenantId: tenant,
	}
	if metaReq != nil {
		if metaReq.TenantId != "" {
			meta.TenantId = metaReq.TenantId
		}
		meta.ActorId = strings.TrimSpace(metaReq.ActorId)
		meta.ActorType = parseActorType(metaReq.ActorType)
		meta.IdempotencyKey = strings.TrimSpace(metaReq.IdempotencyKey)
		meta.Capability = strings.TrimSpace(metaReq.Capability)
		meta.RiskTags = append(meta.RiskTags, metaReq.RiskTags...)
		meta.Requires = append(meta.Requires, metaReq.Requires...)
		meta.PackId = strings.TrimSpace(metaReq.PackId)
		if len(metaReq.Labels) > 0 {
			meta.Labels = metaReq.Labels
		}
	}
	if meta.ActorId == "" {
		meta.ActorId = principal
	}
	return meta
}

func buildPolicyCheckRequest(ctx context.Context, req *policyCheckRequest, cfgSvc *configsvc.Service, defaultTenant string) (*pb.PolicyCheckRequest, error) {
	if req == nil {
		return nil, errors.New("request required")
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = strings.TrimSpace(req.OrgId)
	}
	if tenant == "" {
		tenant = defaultTenant
	}
	meta := buildJobMetadata(req.Meta, tenant, strings.TrimSpace(req.PrincipalId))

	checkReq := &pb.PolicyCheckRequest{
		JobId:         strings.TrimSpace(req.JobId),
		Topic:         topic,
		Tenant:        tenant,
		Priority:      parsePriority(req.Priority),
		EstimatedCost: req.EstimatedCost,
		Budget:        req.Budget,
		PrincipalId:   strings.TrimSpace(req.PrincipalId),
		Labels:        req.Labels,
		MemoryId:      strings.TrimSpace(req.MemoryId),
		Meta:          meta,
	}

	if req.EffectiveConfig != nil {
		if data, err := json.Marshal(req.EffectiveConfig); err == nil {
			checkReq.EffectiveConfig = data
		}
	} else if cfgSvc != nil {
		orgID := req.OrgId
		if orgID == "" {
			orgID = tenant
		}
		if snap, err := cfgSvc.EffectiveSnapshot(ctx, orgID, req.TeamId, req.WorkflowId, req.StepId); err == nil && snap != nil {
			if data, err := json.Marshal(snap); err == nil {
				checkReq.EffectiveConfig = data
			}
		}
	}

	return checkReq, nil
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

func addrFromEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func intFromEnv(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func dialSafetyKernel(addr string) (*grpc.ClientConn, pb.SafetyKernelClient, error) {
	creds, err := safetyTransportCredentials()
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewSafetyKernelClient(conn), nil
}

func safetyTransportCredentials() (credentials.TransportCredentials, error) {
	caPath := strings.TrimSpace(os.Getenv("SAFETY_KERNEL_TLS_CA"))
	requireTLS := env.IsProduction() || env.Bool("SAFETY_KERNEL_TLS_REQUIRED")
	insecureAllowed := env.Bool("SAFETY_KERNEL_INSECURE")

	if caPath == "" {
		if requireTLS {
			return nil, fmt.Errorf("safety_kernel_tls_ca required")
		}
		if insecureAllowed || !env.IsProduction() {
			return insecure.NewCredentials(), nil
		}
		return nil, fmt.Errorf("safety kernel tls required")
	}

	// #nosec G304 -- CA path is configured by the operator.
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("safety kernel tls ca read: %w", err)
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pem); !ok {
		return nil, fmt.Errorf("safety kernel tls ca parse: %s", caPath)
	}
	cfg := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	if env.TLSMinVersion() == tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	return credentials.NewTLS(cfg), nil
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

func parsePriority(priority string) pb.JobPriority {
	switch strings.ToLower(priority) {
	case "batch":
		return pb.JobPriority_JOB_PRIORITY_BATCH
	case "critical":
		return pb.JobPriority_JOB_PRIORITY_CRITICAL
	case "interactive":
		return pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	default:
		return pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	}
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func idempotencyKeyFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	candidates := []string{
		r.Header.Get("Idempotency-Key"),
		r.Header.Get("X-Idempotency-Key"),
		r.URL.Query().Get("idempotency_key"),
		r.URL.Query().Get("idempotency-key"),
	}
	for _, raw := range candidates {
		if val := strings.TrimSpace(raw); val != "" {
			return val
		}
	}
	return ""
}

func tenantFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tenant := headerValue(r, "X-Tenant-ID"); tenant != "" {
		return tenant
	}
	if websocket.IsWebSocketUpgrade(r) {
		if tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id")); tenant != "" {
			return tenant
		}
		if tenant := strings.TrimSpace(r.URL.Query().Get("tenant")); tenant != "" {
			return tenant
		}
	}
	// Fall back to auth context tenant (e.g. from session token)
	if authCtx := authFromRequest(r); authCtx != nil && authCtx.Tenant != "" {
		return authCtx.Tenant
	}
	return ""
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

func artifactMaxBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv(envArtifactMaxBytes)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return defaultArtifactMaxBytes
}

func artifactRequestedMaxBytes(r *http.Request) int64 {
	if r == nil {
		return 0
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_bytes")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	if raw := strings.TrimSpace(r.Header.Get("X-Max-Artifact-Bytes")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

func artifactMaxBytesLimit(r *http.Request) int64 {
	maxBytes := artifactMaxBytes()
	if requested := artifactRequestedMaxBytes(r); requested > 0 && requested < maxBytes {
		return requested
	}
	return maxBytes
}

func (s *server) tenantForBusPacket(ctx context.Context, evt *pb.BusPacket) (string, bool) {
	if evt == nil {
		return "", false
	}
	if req := evt.GetJobRequest(); req != nil {
		if tenant := strings.TrimSpace(req.GetTenantId()); tenant != "" {
			return tenant, true
		}
		if meta := req.GetMeta(); meta != nil {
			if tenant := strings.TrimSpace(meta.GetTenantId()); tenant != "" {
				return tenant, true
			}
		}
	}
	if res := evt.GetJobResult(); res != nil {
		return s.tenantForJobID(ctx, res.GetJobId())
	}
	if prog := evt.GetJobProgress(); prog != nil {
		return s.tenantForJobID(ctx, prog.GetJobId())
	}
	if cancel := evt.GetJobCancel(); cancel != nil {
		return s.tenantForJobID(ctx, cancel.GetJobId())
	}
	return "", false
}

func jobIDForBusPacket(evt *pb.BusPacket) string {
	if evt == nil {
		return ""
	}
	if req := evt.GetJobRequest(); req != nil {
		return strings.TrimSpace(req.GetJobId())
	}
	if res := evt.GetJobResult(); res != nil {
		return strings.TrimSpace(res.GetJobId())
	}
	if prog := evt.GetJobProgress(); prog != nil {
		return strings.TrimSpace(prog.GetJobId())
	}
	if cancel := evt.GetJobCancel(); cancel != nil {
		return strings.TrimSpace(cancel.GetJobId())
	}
	return ""
}

func (s *server) tenantForJobID(ctx context.Context, jobID string) (string, bool) {
	if s == nil || s.jobStore == nil {
		return "", false
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tenant, err := s.jobStore.GetTenant(ctx, jobID)
	if err != nil {
		return "", false
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", false
	}
	return tenant, true
}

func (s *server) maxConcurrentRuns(ctx context.Context, orgID, teamID string) int {
	if s.configSvc == nil {
		return 0
	}
	cfg, err := s.configSvc.Effective(ctx, orgID, teamID, "", "")
	if err != nil || cfg == nil {
		return 0
	}
	if limit := lookupIntPath(cfg, "limits", "max_concurrent_runs"); limit > 0 {
		return limit
	}
	if limit := lookupIntPath(cfg, "rate_limits", "concurrent_workflows"); limit > 0 {
		return limit
	}
	return 0
}

type jobBackpressureError struct {
	active int
	limit  int
}

func (e jobBackpressureError) Error() string {
	return fmt.Sprintf("job queue full (active=%d, limit=%d)", e.active, e.limit)
}

func (s *server) enforceJobBackpressure(ctx context.Context, orgID, teamID string) error {
	if s == nil || s.jobStore == nil {
		return nil
	}
	if s.configSvc == nil {
		return nil
	}
	cfg, err := s.configSvc.Effective(ctx, orgID, teamID, "", "")
	if err != nil || cfg == nil {
		return nil
	}
	limit := lookupIntPath(cfg, "limits", "max_concurrent_jobs")
	if limit <= 0 {
		limit = lookupIntPath(cfg, "rate_limits", "concurrent_jobs")
	}
	if limit <= 0 {
		return nil
	}
	queueSize := lookupIntPath(cfg, "rate_limits", "queue_size")
	if queueSize < 0 {
		queueSize = 0
	}
	maxActive := limit + queueSize
	active, err := s.jobStore.CountActiveByTenant(ctx, orgID)
	if err != nil {
		return fmt.Errorf("active job count: %w", err)
	}
	if active >= maxActive {
		return jobBackpressureError{active: active, limit: maxActive}
	}
	return nil
}

func lookupIntPath(data map[string]any, keys ...string) int {
	if data == nil || len(keys) == 0 {
		return 0
	}
	var cur any = data
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[key]
	}
	switch v := cur.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

func parseActorType(raw string) pb.ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "human":
		return pb.ActorType_ACTOR_TYPE_HUMAN
	case "service":
		return pb.ActorType_ACTOR_TYPE_SERVICE
	default:
		return pb.ActorType_ACTOR_TYPE_UNSPECIFIED
	}
}

func appendUniqueTag(tags []string, tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return tags
	}
	for _, existing := range tags {
		if strings.EqualFold(existing, tag) {
			return tags
		}
	}
	return append(tags, tag)
}

func parseContextMode(topic, explicit string) string {
	switch strings.ToLower(explicit) {
	case "chat":
		return "chat"
	case "rag":
		return "rag"
	case "raw":
		return "raw"
	}
	return "raw"
}

type memoryPolicyError struct {
	status int
	msg    string
}

func (e memoryPolicyError) Error() string {
	return e.msg
}

func (s *server) enforceMemoryID(ctx context.Context, orgID, teamID, workflowID, stepID, memoryID string) error {
	memoryID = memory.NormalizeMemoryID(memoryID)
	if memoryID == "" {
		return nil
	}
	if s == nil || s.configSvc == nil {
		return memoryPolicyError{status: http.StatusServiceUnavailable, msg: "config service unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if _, err := s.configSvc.Get(cctx, configsvc.ScopeSystem, "default"); err != nil && !errors.Is(err, redis.Nil) {
		return memoryPolicyError{status: http.StatusServiceUnavailable, msg: "config service unavailable"}
	}
	cfgMap, err := s.configSvc.Effective(cctx, orgID, teamID, workflowID, stepID)
	if err != nil {
		return memoryPolicyError{status: http.StatusServiceUnavailable, msg: "config service unavailable"}
	}
	cfg, ok := config.ParseEffectiveContextMap(cfgMap)
	if !ok {
		return nil
	}
	allowed, reason := config.MemoryIDAllowed(cfg, memoryID)
	if !allowed {
		return memoryPolicyError{status: http.StatusForbidden, msg: reason}
	}
	return nil
}

func deriveMemoryIDFromReq(topic, explicit, jobID string) string {
	if explicit != "" {
		return memory.NormalizeMemoryID(explicit)
	}
	return strings.TrimSpace(jobID)
}

func normalizeTimestampMicrosLower(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts * microsPerSecond
	case ts < millisThreshold:
		return ts * microsPerMillisecond
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

func normalizeTimestampMicrosUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts*microsPerSecond + (microsPerSecond - 1)
	case ts < millisThreshold:
		return ts*microsPerMillisecond + (microsPerMillisecond - 1)
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

func normalizeTimestampSecondsUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts
	case ts < millisThreshold:
		return ts / 1_000
	case ts < microsThreshold:
		return ts / 1_000_000
	default:
		return ts / 1_000_000_000
	}
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

func parseLockMode(raw string) locks.Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shared":
		return locks.ModeShared
	default:
		return locks.ModeExclusive
	}
}

func parseRetention(raw string) artifacts.RetentionClass {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short":
		return artifacts.RetentionShort
	case "audit":
		return artifacts.RetentionAudit
	default:
		return artifacts.RetentionStandard
	}
}
