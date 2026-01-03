package safetykernel

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/coretex-os/core/configsvc"
	"github.com/yaront1111/coretex-os/core/infra/config"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

type server struct {
	pb.UnimplementedSafetyKernelServer
	mu        sync.RWMutex
	policy    *config.SafetyPolicy
	snapshot  string
	snapshots []string
}

const (
	defaultPolicyConfigID  = "policy"
	defaultPolicyConfigKey = "bundles"
)

// Run starts the Safety Kernel gRPC server and blocks until it exits.
func Run(cfg *config.Config) error {
	if cfg == nil {
		cfg = config.Load()
	}

	policySource := policySourceFromEnv(cfg.SafetyPolicyPath)
	loader := newPolicyLoader(cfg, policySource)
	defer loader.Close()
	policy, snapshot, err := loader.Load(context.Background())
	if err != nil {
		return fmt.Errorf("load safety policy: %w", err)
	}

	lis, err := net.Listen("tcp", cfg.SafetyKernelAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.SafetyKernelAddr, err)
	}

	serverCreds := grpc.Creds(insecure.NewCredentials())
	if cert := os.Getenv("SAFETY_KERNEL_TLS_CERT"); cert != "" {
		key := os.Getenv("SAFETY_KERNEL_TLS_KEY")
		if key == "" {
			log.Printf("safety-kernel: tls cert provided without key, continuing insecure")
		} else if creds, err := credentials.NewServerTLSFromFile(cert, key); err != nil {
			log.Printf("safety-kernel: failed to load tls credentials, continuing insecure: %v", err)
		} else {
			serverCreds = grpc.Creds(creds)
		}
	}

	srv := &server{}
	srv.setPolicy(policy, snapshot)
	if loader.ShouldWatch() {
		go srv.watchPolicy(loader)
	}

	grpcServer := grpc.NewServer(serverCreds)
	pb.RegisterSafetyKernelServer(grpcServer, srv)
	reflection.Register(grpcServer)

	log.Printf("safety-kernel: listening on %s", cfg.SafetyKernelAddr)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

func (s *server) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "check")
}

func (s *server) Evaluate(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "evaluate")
}

func (s *server) Explain(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "explain")
}

func (s *server) Simulate(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "simulate")
}

func (s *server) ListSnapshots(ctx context.Context, _ *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
	s.mu.RLock()
	snapshots := append([]string{}, s.snapshots...)
	s.mu.RUnlock()
	return &pb.ListSnapshotsResponse{Snapshots: snapshots}, nil
}

func (s *server) evaluate(_ context.Context, req *pb.PolicyCheckRequest, _ string) (*pb.PolicyCheckResponse, error) {
	decision := pb.DecisionType_DECISION_TYPE_ALLOW
	reason := ""

	topic := strings.TrimSpace(req.GetTopic())
	tenant := strings.TrimSpace(req.GetTenant())
	meta := req.GetMeta()
	if tenant == "" && meta != nil {
		tenant = strings.TrimSpace(meta.GetTenantId())
	}

	s.mu.RLock()
	policy := s.policy
	snapshot := s.snapshot
	defaultTenant := ""
	if policy != nil {
		defaultTenant = strings.TrimSpace(policy.DefaultTenant)
	}
	s.mu.RUnlock()

	if tenant == "" {
		tenant = defaultTenant
	}
	if tenant == "" {
		tenant = "default"
	}

	if topic == "" {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "missing topic"}, nil
	}
	if !strings.HasPrefix(topic, "job.") {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "unsupported topic"}, nil
	}

	input := config.PolicyInput{
		Tenant: tenant,
		Topic:  topic,
		Labels: req.GetLabels(),
		Meta:   policyMetaFromRequest(req),
		MCP:    extractMCPRequest(req.GetLabels()),
	}
	input.SecretsPresent = secretsPresent(input.Meta, req.GetLabels())

	policyDecision := config.PolicyDecision{Decision: "allow"}
	if policy != nil {
		policyDecision = policy.Evaluate(input)
		if tp, ok := policy.Tenants[tenant]; ok {
			if ok, mcpReason := config.MCPAllowed(tp.MCP, input.MCP); !ok {
				policyDecision.Decision = "deny"
				policyDecision.Reason = mcpReason
			}
		}
	}

	constraints := toProtoConstraints(policyDecision.Constraints)
	switch policyDecision.Decision {
	case "deny":
		decision = pb.DecisionType_DECISION_TYPE_DENY
		reason = policyDecision.Reason
	case "require_approval":
		decision = pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
		reason = policyDecision.Reason
	case "throttle":
		decision = pb.DecisionType_DECISION_TYPE_THROTTLE
		reason = policyDecision.Reason
	case "allow_with_constraints":
		decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
	case "allow":
		if constraints != nil {
			decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
		}
	}

	// Effective config can further restrict allowed topics or MCP access.
	if eff, ok := config.ParseEffectiveSafety(req.GetEffectiveConfig()); ok {
		if matchAny(eff.DeniedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic '%s' denied by effective config", topic)
		}
		if len(eff.AllowedTopics) > 0 && !matchAny(eff.AllowedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic '%s' not allowed by effective config", topic)
		}
		if ok, mcpReason := config.MCPAllowed(eff.MCP, input.MCP); !ok {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = mcpReason
		}
	}

	approvalRequired := policyDecision.ApprovalRequired || decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	approvalRef := ""
	if approvalRequired {
		approvalRef = req.GetJobId()
	}

	return &pb.PolicyCheckResponse{
		Decision:         decision,
		Reason:           reason,
		PolicySnapshot:   snapshot,
		RuleId:           policyDecision.RuleID,
		Constraints:      constraints,
		ApprovalRequired: approvalRequired,
		ApprovalRef:      approvalRef,
	}, nil
}

func policyMetaFromRequest(req *pb.PolicyCheckRequest) config.PolicyMeta {
	meta := req.GetMeta()
	out := config.PolicyMeta{}
	if meta == nil {
		if req.GetPrincipalId() != "" {
			out.ActorID = req.GetPrincipalId()
		}
		return out
	}
	out.ActorID = meta.GetActorId()
	out.ActorType = actorTypeString(meta.GetActorType())
	out.IdempotencyKey = meta.GetIdempotencyKey()
	out.Capability = meta.GetCapability()
	out.RiskTags = append(out.RiskTags, meta.GetRiskTags()...)
	out.Requires = append(out.Requires, meta.GetRequires()...)
	out.PackID = meta.GetPackId()
	if out.ActorID == "" {
		out.ActorID = req.GetPrincipalId()
	}
	return out
}

func actorTypeString(val pb.ActorType) string {
	switch val {
	case pb.ActorType_ACTOR_TYPE_HUMAN:
		return "human"
	case pb.ActorType_ACTOR_TYPE_SERVICE:
		return "service"
	default:
		return ""
	}
}

func secretsPresent(meta config.PolicyMeta, labels map[string]string) bool {
	if labels != nil {
		if v := strings.TrimSpace(labels["secrets_present"]); v != "" {
			return v == "true" || v == "1" || strings.EqualFold(v, "yes")
		}
	}
	for _, tag := range meta.RiskTags {
		if strings.EqualFold(tag, "secrets") {
			return true
		}
	}
	return false
}

func extractMCPRequest(labels map[string]string) config.MCPRequest {
	if len(labels) == 0 {
		return config.MCPRequest{}
	}
	return config.MCPRequest{
		Server:   pickLabel(labels, "mcp.server", "mcp_server", "mcpServer"),
		Tool:     pickLabel(labels, "mcp.tool", "mcp_tool", "mcpTool"),
		Resource: pickLabel(labels, "mcp.resource", "mcp_resource", "mcpResource"),
		Action:   strings.ToLower(pickLabel(labels, "mcp.action", "mcp_action", "mcpAction")),
	}
}

func pickLabel(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if val, ok := labels[key]; ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func toProtoConstraints(c config.PolicyConstraints) *pb.PolicyConstraints {
	if isConstraintsEmpty(c) {
		return nil
	}
	out := &pb.PolicyConstraints{
		Budgets: &pb.BudgetConstraints{
			MaxRuntimeMs:      c.Budgets.MaxRuntimeMs,
			MaxRetries:        c.Budgets.MaxRetries,
			MaxArtifactBytes:  c.Budgets.MaxArtifactBytes,
			MaxConcurrentJobs: c.Budgets.MaxConcurrentJobs,
		},
		Sandbox: &pb.SandboxProfile{
			Isolated:         c.Sandbox.Isolated,
			NetworkAllowlist: c.Sandbox.NetworkAllowlist,
			FsReadOnly:       c.Sandbox.FsReadOnly,
			FsReadWrite:      c.Sandbox.FsReadWrite,
		},
		Toolchain: &pb.ToolchainConstraints{
			AllowedTools:    c.Toolchain.AllowedTools,
			AllowedCommands: c.Toolchain.AllowedCommands,
		},
		Diff: &pb.DiffConstraints{
			MaxFiles:      c.Diff.MaxFiles,
			MaxLines:      c.Diff.MaxLines,
			DenyPathGlobs: c.Diff.DenyPathGlobs,
		},
		RedactionLevel: c.RedactionLevel,
	}
	return out
}

func isConstraintsEmpty(c config.PolicyConstraints) bool {
	return c.Budgets.MaxRuntimeMs == 0 && c.Budgets.MaxRetries == 0 && c.Budgets.MaxArtifactBytes == 0 && c.Budgets.MaxConcurrentJobs == 0 &&
		!c.Sandbox.Isolated && len(c.Sandbox.NetworkAllowlist) == 0 && len(c.Sandbox.FsReadOnly) == 0 && len(c.Sandbox.FsReadWrite) == 0 &&
		len(c.Toolchain.AllowedTools) == 0 && len(c.Toolchain.AllowedCommands) == 0 &&
		c.Diff.MaxFiles == 0 && c.Diff.MaxLines == 0 && len(c.Diff.DenyPathGlobs) == 0 &&
		strings.TrimSpace(c.RedactionLevel) == ""
}

func matchAny(patterns []string, value string) bool {
	if value == "" {
		return false
	}
	for _, pat := range patterns {
		if configMatch(pat, value) {
			return true
		}
	}
	return false
}

func configMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, _ := pathMatch(pattern, value)
	return ok
}

func pathMatch(pattern, value string) (bool, error) {
	return pathMatchImpl(pattern, value)
}

// path.Match is small; wrap to keep helpers testable.
func pathMatchImpl(pattern, value string) (bool, error) {
	return path.Match(pattern, value)
}

func (s *server) watchPolicy(loader *policyLoader) {
	interval := 30 * time.Second
	if raw := os.Getenv("SAFETY_POLICY_RELOAD_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		policy, snapshot, err := loader.Load(context.Background())
		if err != nil {
			log.Printf("safety-kernel: policy reload failed: %v", err)
			continue
		}
		s.mu.RLock()
		current := s.snapshot
		s.mu.RUnlock()
		if snapshot != "" && snapshot != current {
			s.setPolicy(policy, snapshot)
			log.Printf("safety-kernel: policy snapshot updated %s", snapshot)
		}
	}
}

func (s *server) setPolicy(policy *config.SafetyPolicy, snapshot string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = policy
	s.snapshot = snapshot
	if snapshot != "" {
		s.snapshots = append([]string{snapshot}, s.snapshots...)
		if len(s.snapshots) > 10 {
			s.snapshots = s.snapshots[:10]
		}
	}
}

type policyLoader struct {
	source      string
	configSvc   *configsvc.Service
	configScope configsvc.Scope
	configID    string
	configKey   string
}

func newPolicyLoader(cfg *config.Config, source string) *policyLoader {
	loader := &policyLoader{source: source}
	if strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_DISABLE")) != "" {
		return loader
	}
	scope := strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_SCOPE"))
	if scope == "" {
		scope = string(configsvc.ScopeSystem)
	}
	id := strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_ID"))
	if id == "" {
		id = defaultPolicyConfigID
	}
	key := strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_KEY"))
	if key == "" {
		key = defaultPolicyConfigKey
	}
	loader.configScope = configsvc.Scope(scope)
	loader.configID = id
	loader.configKey = key
	if cfg == nil {
		return loader
	}
	svc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		log.Printf("safety-kernel: config service disabled: %v", err)
		return loader
	}
	loader.configSvc = svc
	return loader
}

func (l *policyLoader) Close() {
	if l == nil || l.configSvc == nil {
		return
	}
	_ = l.configSvc.Close()
}

func (l *policyLoader) ShouldWatch() bool {
	if l == nil {
		return false
	}
	return l.source != "" || l.configSvc != nil
}

func (l *policyLoader) Load(ctx context.Context) (*config.SafetyPolicy, string, error) {
	basePolicy, baseSnapshot, err := loadPolicyBundle(l.source)
	if err != nil {
		return nil, "", err
	}
	fragmentPolicy, fragmentSnapshot, err := l.loadFragments(ctx)
	if err != nil {
		return nil, "", err
	}
	merged := mergePolicies(basePolicy, fragmentPolicy)
	return merged, combineSnapshots(baseSnapshot, fragmentSnapshot), nil
}

func (l *policyLoader) loadFragments(ctx context.Context) (*config.SafetyPolicy, string, error) {
	if l == nil || l.configSvc == nil {
		return nil, "", nil
	}
	doc, err := l.configSvc.Get(ctx, l.configScope, l.configID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if doc.Data == nil {
		return nil, "", nil
	}
	rawBundles, ok := doc.Data[l.configKey].(map[string]any)
	if !ok || len(rawBundles) == 0 {
		return nil, "", nil
	}
	keys := make([]string, 0, len(rawBundles))
	for key := range rawBundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	var merged *config.SafetyPolicy
	for _, key := range keys {
		content, ok := extractPolicyFragment(rawBundles[key])
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
		hasher.Write([]byte(content))
		policy, err := config.ParseSafetyPolicy([]byte(content))
		if err != nil {
			return nil, "", fmt.Errorf("parse policy fragment %q: %w", key, err)
		}
		merged = mergePolicies(merged, policy)
	}
	if merged == nil {
		return nil, "", nil
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	return merged, "cfg:" + hash, nil
}

func extractPolicyFragment(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case map[string]any:
		if raw, ok := v["content"].(string); ok {
			return raw, true
		}
		if raw, ok := v["policy"].(string); ok {
			return raw, true
		}
		if raw, ok := v["data"].(string); ok {
			return raw, true
		}
	}
	return "", false
}

func combineSnapshots(base, extra string) string {
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return base + "|" + extra
}

func mergePolicies(base, extra *config.SafetyPolicy) *config.SafetyPolicy {
	if base == nil {
		return clonePolicy(extra)
	}
	if extra == nil {
		return clonePolicy(base)
	}
	out := clonePolicy(base)
	if out.Version == "" {
		out.Version = extra.Version
	}
	if out.DefaultTenant == "" {
		out.DefaultTenant = extra.DefaultTenant
	}
	out.Rules = append(out.Rules, extra.Rules...)
	out.Tenants = mergeTenantPolicies(out.Tenants, extra.Tenants)
	return out
}

func clonePolicy(policy *config.SafetyPolicy) *config.SafetyPolicy {
	if policy == nil {
		return nil
	}
	out := &config.SafetyPolicy{
		Version:       policy.Version,
		DefaultTenant: policy.DefaultTenant,
		Rules:         append([]config.PolicyRule{}, policy.Rules...),
		Tenants:       map[string]config.TenantPolicy{},
	}
	if policy.Tenants != nil {
		for k, v := range policy.Tenants {
			out.Tenants[k] = cloneTenantPolicy(v)
		}
	}
	return out
}

func mergeTenantPolicies(base map[string]config.TenantPolicy, extra map[string]config.TenantPolicy) map[string]config.TenantPolicy {
	out := map[string]config.TenantPolicy{}
	for k, v := range base {
		out[k] = cloneTenantPolicy(v)
	}
	for tenant, add := range extra {
		current, ok := out[tenant]
		if !ok {
			out[tenant] = cloneTenantPolicy(add)
			continue
		}
		merged := current
		merged.AllowTopics = append(merged.AllowTopics, add.AllowTopics...)
		merged.DenyTopics = append(merged.DenyTopics, add.DenyTopics...)
		merged.AllowedRepoHosts = append(merged.AllowedRepoHosts, add.AllowedRepoHosts...)
		merged.DeniedRepoHosts = append(merged.DeniedRepoHosts, add.DeniedRepoHosts...)
		if add.MaxConcurrent > 0 && (merged.MaxConcurrent == 0 || add.MaxConcurrent < merged.MaxConcurrent) {
			merged.MaxConcurrent = add.MaxConcurrent
		}
		merged.MCP = mergeMCPPolicy(merged.MCP, add.MCP)
		out[tenant] = merged
	}
	return out
}

func cloneTenantPolicy(policy config.TenantPolicy) config.TenantPolicy {
	return config.TenantPolicy{
		AllowTopics:      append([]string{}, policy.AllowTopics...),
		DenyTopics:       append([]string{}, policy.DenyTopics...),
		AllowedRepoHosts: append([]string{}, policy.AllowedRepoHosts...),
		DeniedRepoHosts:  append([]string{}, policy.DeniedRepoHosts...),
		MaxConcurrent:    policy.MaxConcurrent,
		MCP:              policy.MCP,
	}
}

func mergeMCPPolicy(base, extra config.MCPPolicy) config.MCPPolicy {
	return config.MCPPolicy{
		AllowServers:   append(base.AllowServers, extra.AllowServers...),
		DenyServers:    append(base.DenyServers, extra.DenyServers...),
		AllowTools:     append(base.AllowTools, extra.AllowTools...),
		DenyTools:      append(base.DenyTools, extra.DenyTools...),
		AllowResources: append(base.AllowResources, extra.AllowResources...),
		DenyResources:  append(base.DenyResources, extra.DenyResources...),
		AllowActions:   append(base.AllowActions, extra.AllowActions...),
		DenyActions:    append(base.DenyActions, extra.DenyActions...),
	}
}

func policySourceFromEnv(path string) string {
	if raw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_URL")); raw != "" {
		return raw
	}
	return strings.TrimSpace(path)
}

func loadPolicyBundle(source string) (*config.SafetyPolicy, string, error) {
	if source == "" {
		return nil, "", nil
	}
	data, err := readPolicySource(source)
	if err != nil {
		return nil, "", err
	}
	if err := verifyPolicySignature(data, source); err != nil {
		return nil, "", err
	}
	policy, err := config.ParseSafetyPolicy(data)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	snapshot := hash
	if policy != nil && policy.Version != "" {
		snapshot = policy.Version + ":" + hash
	}
	return policy, snapshot, nil
}

func readPolicySource(source string) ([]byte, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return fetchPolicyURL(source)
	}
	return os.ReadFile(source)
}

func fetchPolicyURL(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("policy fetch status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func verifyPolicySignature(data []byte, source string) error {
	pubRaw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_PUBLIC_KEY"))
	if pubRaw == "" {
		return nil
	}
	pubKey, err := decodeKey(pubRaw)
	if err != nil {
		return fmt.Errorf("invalid SAFETY_POLICY_PUBLIC_KEY: %w", err)
	}
	sig, err := readSignature(source)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKey), data, sig) {
		return errors.New("policy signature verification failed")
	}
	return nil
}

func readSignature(source string) ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_SIGNATURE")); raw != "" {
		return decodeKey(raw)
	}
	if path := strings.TrimSpace(os.Getenv("SAFETY_POLICY_SIGNATURE_PATH")); path != "" {
		return os.ReadFile(path)
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return nil, errors.New("policy signature required but no signature provided")
	}
	sigPath := source + ".sig"
	if _, err := os.Stat(sigPath); err == nil {
		return os.ReadFile(sigPath)
	}
	return nil, errors.New("policy signature required but not found")
}

func decodeKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("empty key")
	}
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return data, nil
	}
	if data, err := hex.DecodeString(raw); err == nil {
		return data, nil
	}
	return nil, errors.New("invalid key encoding")
}
