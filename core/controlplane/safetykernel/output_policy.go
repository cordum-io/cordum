package safetykernel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

const (
	envOutputScannersPath     = "OUTPUT_SCANNERS_PATH"
	defaultOutputScannersPath = "config/output_scanners.yaml"
	maxOutputScanBytes        = 2 * 1024 * 1024
	outputReadTimeout         = 2 * time.Second
	redisPointerPrefix        = "redis://"
	maxRegexLen               = 256
	maxAlternations           = 5
)

// nestedQuantifierRe matches patterns like (.*)+, (a+)*, (.+){2,} that risk ReDoS.
var nestedQuantifierRe = regexp.MustCompile(`[+*]\)[+*?{]`)

var regexRejectedTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "cordum_safety_regex_rejected_total",
	Help: "Total output-rule regex patterns rejected for complexity",
})

func init() {
	prometheus.MustRegister(regexRejectedTotal)
}

// validateRegexComplexity rejects patterns that are excessively complex.
func validateRegexComplexity(pat string) error {
	if len(pat) > maxRegexLen {
		return fmt.Errorf("pattern length %d exceeds max %d", len(pat), maxRegexLen)
	}
	if nestedQuantifierRe.MatchString(pat) {
		return fmt.Errorf("nested quantifier detected (ReDoS risk)")
	}
	if strings.Count(pat, "|") > maxAlternations {
		return fmt.Errorf("too many alternations (%d, max %d)", strings.Count(pat, "|"), maxAlternations)
	}
	return nil
}

type compiledOutputRule struct {
	id             string
	decision       pb.OutputDecision
	reason         string
	severity       string
	tenants        []string
	topics         []string
	capabilities   []string
	riskTags       []string
	contentTypes   []string
	scanners       []string
	patterns       []compiledOutputPattern
	keywords       []string
	maxOutputBytes int64
	hasError       *bool
}

type compiledOutputPattern struct {
	raw string
	re  *regexp.Regexp
}

// OutputEvaluateRequest captures dereferenced output content and original request context.
type OutputEvaluateRequest struct {
	JobID           string
	Topic           string
	Tenant          string
	Labels          map[string]string
	ResultPtr       string
	ArtifactPtrs    []string
	ErrorMessage    string
	ErrorCode       string
	WorkerID        string
	ExecutionMs     int64
	OutputSizeBytes int64
	ContentHash     string
	WorkflowID      string
	StepID          string
	OutputContent   []byte
	Capabilities    []string
	RiskTags        []string
	PrincipalID     string
	PackID          string
	ContentType     string
	OriginalLabels  map[string]string
}

// OutputEvaluateResponse captures the result of EvaluateOutput().
type OutputEvaluateResponse struct {
	Decision       string
	Reason         string
	RuleID         string
	Findings       []outputFinding
	PolicySnapshot string
}

// EvaluateOutput evaluates output content against loaded output policy rules.
// This is the direct entrypoint (non-gRPC) matching the DoD requirement.
func (s *server) EvaluateOutput(ctx context.Context, req *OutputEvaluateRequest) (*OutputEvaluateResponse, error) {
	resp := &OutputEvaluateResponse{Decision: "allow"}
	if req == nil {
		resp.Reason = "missing request"
		return resp, nil
	}

	s.mu.RLock()
	policy := s.policy
	rules := append([]compiledOutputRule{}, s.outputRules...)
	snapshot := s.snapshot
	scanners := s.scanners
	s.mu.RUnlock()

	resp.PolicySnapshot = snapshot
	if !outputPolicyEnabled(policy, rules) {
		return resp, nil
	}

	// Dereference ResultPtr if no content provided.
	var contentTruncated bool
	if len(req.OutputContent) == 0 && req.ResultPtr != "" && s.resultClient != nil {
		key, err := resultKeyFromPointer(req.ResultPtr)
		if err == nil {
			rctx, cancel := context.WithTimeout(ctx, outputReadTimeout)
			defer cancel()
			data, err := s.resultClient.Get(rctx, key).Bytes()
			if err == nil {
				req.OutputContent, contentTruncated = truncateOutputContent(data)
			}
		}
	} else if len(req.OutputContent) > maxOutputScanBytes {
		req.OutputContent, contentTruncated = truncateOutputContent(req.OutputContent)
	}

	for _, rule := range rules {
		matched, findings := evaluateOutputRule(rule, req, scanners)
		if contentTruncated {
			findings = append(findings, outputFinding{
				Type:     "content_truncated",
				Severity: "info",
				Detail:   fmt.Sprintf("content exceeded max regex input size (%d bytes), truncated", maxOutputScanBytes),
				Scanner:  "size_check",
			})
		}
		if !matched {
			continue
		}
		resp.Decision = outputDecisionString(rule.decision)
		resp.Reason = outputRuleReason(rule, findings)
		resp.RuleID = rule.id
		resp.Findings = findings
		return resp, nil
	}

	return resp, nil
}

func outputDecisionString(d pb.OutputDecision) string {
	switch d {
	case pb.OutputDecision_OUTPUT_DECISION_QUARANTINE:
		return "quarantine"
	case pb.OutputDecision_OUTPUT_DECISION_REDACT:
		return "redact"
	default:
		return "allow"
	}
}

type outputScannersFile struct {
	Version  string                           `yaml:"version"`
	Scanners map[string]outputScannerSpecFile `yaml:"scanners"`
}

type outputScannerSpecFile struct {
	FindingType string                 `yaml:"finding_type"`
	Description string                 `yaml:"description"`
	Patterns    []outputScannerPattern `yaml:"patterns"`
}

type outputScannerPattern struct {
	Name            string  `yaml:"name"`
	Regex           string  `yaml:"regex"`
	Pattern         string  `yaml:"pattern"`
	Severity        string  `yaml:"severity"`
	Confidence      float32 `yaml:"confidence"`
	ContextRequired bool    `yaml:"context_required"`
}

func (s *server) CheckOutput(ctx context.Context, req *pb.OutputCheckRequest) (*pb.OutputCheckResponse, error) {
	resp := &pb.OutputCheckResponse{
		Decision: pb.OutputDecision_OUTPUT_DECISION_ALLOW,
	}
	if req == nil {
		resp.Reason = "missing request"
		return resp, nil
	}

	s.mu.RLock()
	policy := s.policy
	rules := append([]compiledOutputRule{}, s.outputRules...)
	snapshot := s.snapshot
	scanners := s.scanners
	s.mu.RUnlock()

	resp.PolicySnapshot = snapshot
	if !outputPolicyEnabled(policy, rules) {
		return resp, nil
	}

	content, contentTruncated := s.contentForScan(ctx, req)
	evalReq := outputEvaluateRequestFromProto(req, content)
	for _, rule := range rules {
		matched, findings := evaluateOutputRule(rule, evalReq, scanners)
		if !matched {
			continue
		}
		if contentTruncated {
			findings = append(findings, outputFinding{
				Type:     "content_truncated",
				Severity: "info",
				Detail:   fmt.Sprintf("content exceeded max regex input size (%d bytes), truncated", maxOutputScanBytes),
				Scanner:  "size_check",
			})
		}
		resp.Decision = rule.decision
		resp.Reason = outputRuleReason(rule, findings)
		resp.RuleId = rule.id
		resp.Findings = toProtoOutputFindings(findings)
		return resp, nil
	}

	return resp, nil
}

func outputPolicyEnabled(policy *config.SafetyPolicy, rules []compiledOutputRule) bool {
	if policy == nil || len(rules) == 0 {
		return false
	}
	if policy.OutputPolicy.Enabled {
		return true
	}
	// Backward compatibility: if output_rules exist but output_policy block is absent,
	// keep legacy enabled behavior.
	return strings.TrimSpace(policy.OutputPolicy.FailMode) == ""
}

func outputRuleReason(rule compiledOutputRule, findings []outputFinding) string {
	if reason := strings.TrimSpace(rule.reason); reason != "" {
		return reason
	}
	if len(findings) > 0 {
		return findings[0].Detail
	}
	switch rule.decision {
	case pb.OutputDecision_OUTPUT_DECISION_QUARANTINE:
		return "output quarantined by policy"
	case pb.OutputDecision_OUTPUT_DECISION_REDACT:
		return "output requires redaction by policy"
	default:
		return "output allowed by policy"
	}
}

func evaluateOutputRule(rule compiledOutputRule, req *OutputEvaluateRequest, scanners map[string]OutputScanner) (bool, []outputFinding) {
	if req == nil {
		return false, nil
	}
	if len(rule.tenants) > 0 && !containsAnyFold([]string{req.Tenant}, rule.tenants) {
		return false, nil
	}
	if len(rule.topics) > 0 && !matchAny(rule.topics, req.Topic) {
		return false, nil
	}
	if len(rule.capabilities) > 0 && !containsAnyFold(req.Capabilities, rule.capabilities) {
		return false, nil
	}
	if len(rule.riskTags) > 0 && !containsAnyFold(req.RiskTags, rule.riskTags) {
		return false, nil
	}
	if len(rule.contentTypes) > 0 && !containsAnyFold([]string{req.ContentType}, rule.contentTypes) {
		return false, nil
	}
	if rule.hasError != nil {
		hasError := strings.TrimSpace(req.ErrorMessage) != "" || strings.TrimSpace(req.ErrorCode) != ""
		if hasError != *rule.hasError {
			return false, nil
		}
	}
	if rule.maxOutputBytes > 0 {
		size := req.OutputSizeBytes
		if size <= 0 {
			size = int64(len(req.OutputContent))
		}
		if size <= rule.maxOutputBytes {
			return false, nil
		}
	}

	findings := make([]outputFinding, 0, 8)

	if len(rule.patterns) > 0 {
		if len(req.OutputContent) == 0 {
			return false, nil
		}
		patternFindings := scanWithContentPatterns(req.OutputContent, rule)
		if len(patternFindings) == 0 {
			return false, nil
		}
		findings = append(findings, patternFindings...)
	}

	if len(rule.keywords) > 0 {
		if len(req.OutputContent) == 0 {
			return false, nil
		}
		kwScanner := newKeywordScanner(rule.keywords)
		kwFindings := kwScanner.Scan(req.OutputContent)
		if len(kwFindings) == 0 {
			return false, nil
		}
		findings = append(findings, kwFindings...)
	}

	if len(rule.scanners) > 0 {
		if len(req.OutputContent) == 0 {
			return false, nil
		}
		scannerFindings := scanWithScanners(req.OutputContent, rule.scanners, scanners)
		if len(scannerFindings) == 0 {
			return false, nil
		}
		findings = append(findings, scannerFindings...)
	}

	return true, findings
}

func outputEvaluateRequestFromProto(req *pb.OutputCheckRequest, content []byte) *OutputEvaluateRequest {
	if req == nil {
		return &OutputEvaluateRequest{}
	}
	out := &OutputEvaluateRequest{
		JobID:           strings.TrimSpace(req.GetJobId()),
		Topic:           strings.TrimSpace(req.GetTopic()),
		Tenant:          strings.TrimSpace(req.GetTenant()),
		Labels:          req.GetLabels(),
		ResultPtr:       strings.TrimSpace(req.GetResultPtr()),
		ArtifactPtrs:    append([]string{}, req.GetArtifactPtrs()...),
		ErrorMessage:    strings.TrimSpace(req.GetErrorMessage()),
		ErrorCode:       strings.TrimSpace(req.GetErrorCode()),
		WorkerID:        strings.TrimSpace(req.GetWorkerId()),
		ExecutionMs:     req.GetExecutionMs(),
		OutputSizeBytes: req.GetOutputSizeBytes(),
		ContentHash:     strings.TrimSpace(req.GetContentHash()),
		WorkflowID:      strings.TrimSpace(req.GetWorkflowId()),
		StepID:          strings.TrimSpace(req.GetStepId()),
		OutputContent:   append([]byte{}, content...),
		Capabilities:    append([]string{}, req.GetCapabilities()...),
		RiskTags:        append([]string{}, req.GetRiskTags()...),
		PrincipalID:     strings.TrimSpace(req.GetPrincipalId()),
		PackID:          strings.TrimSpace(req.GetPackId()),
		ContentType:     strings.TrimSpace(req.GetContentType()),
		OriginalLabels:  req.GetOriginalLabels(),
	}
	if out.OutputSizeBytes <= 0 {
		out.OutputSizeBytes = int64(len(out.OutputContent))
	}
	return out
}

func (s *server) contentForScan(ctx context.Context, req *pb.OutputCheckRequest) ([]byte, bool) {
	content := req.GetOutputContent()
	if len(content) > 0 {
		return truncateOutputContent(content)
	}
	ptr := strings.TrimSpace(req.GetResultPtr())
	if ptr != "" && s != nil && s.resultClient != nil {
		key, err := resultKeyFromPointer(ptr)
		if err == nil {
			if ctx == nil {
				ctx = context.Background()
			}
			rctx, cancel := context.WithTimeout(ctx, outputReadTimeout)
			defer cancel()
			data, err := s.resultClient.Get(rctx, key).Bytes()
			if err == nil {
				return truncateOutputContent(data)
			}
			if !errors.Is(err, redis.Nil) {
				log.Printf("safety-kernel: output pointer fetch failed: %v", err)
			}
		} else {
			log.Printf("safety-kernel: invalid output pointer: %v", err)
		}
	}
	msg := strings.TrimSpace(req.GetErrorMessage())
	if msg == "" {
		return nil, false
	}
	return truncateOutputContent([]byte(msg))
}

func truncateOutputContent(content []byte) ([]byte, bool) {
	if len(content) <= maxOutputScanBytes {
		return content, false
	}
	return content[:maxOutputScanBytes], true
}

func resultKeyFromPointer(ptr string) (string, error) {
	if ptr == "" {
		return "", fmt.Errorf("empty pointer")
	}
	if !strings.HasPrefix(ptr, redisPointerPrefix) {
		return "", fmt.Errorf("invalid pointer prefix: %s", ptr)
	}
	key := strings.TrimPrefix(ptr, redisPointerPrefix)
	if key == "" {
		return "", fmt.Errorf("missing pointer key")
	}
	return key, nil
}

const regexEvalTimeout = 100 * time.Millisecond

// runRegexWithTimeout runs a regex match with a timeout. Returns nil if timed out.
func runRegexWithTimeout(re *regexp.Regexp, text string, n int) [][]int {
	type result struct {
		hits [][]int
	}
	ch := make(chan result, 1)
	go func() {
		ch <- result{hits: re.FindAllStringIndex(text, n)}
	}()
	select {
	case r := <-ch:
		return r.hits
	case <-time.After(regexEvalTimeout):
		return nil
	}
}

func scanWithContentPatterns(content []byte, rule compiledOutputRule) []outputFinding {
	if len(content) == 0 {
		return nil
	}
	text := string(content)
	out := make([]outputFinding, 0, 4)
	for _, pattern := range rule.patterns {
		hits := runRegexWithTimeout(pattern.re, text, maxFindingsPerPattern)
		if hits == nil {
			log.Printf("safety-kernel: regex timeout for rule %q pattern %q", rule.id, pattern.raw)
			continue
		}
		for _, hit := range hits {
			if len(hit) != 2 {
				continue
			}
			fragment := text[hit[0]:hit[1]]
			out = append(out, outputFinding{
				Type:           "pattern_match",
				Severity:       normalizeSeverity(rule.severity),
				Detail:         "content pattern matched",
				Offset:         int64(hit[0]),
				Length:         int64(hit[1] - hit[0]),
				Scanner:        "content_pattern",
				Confidence:     0.9,
				MatchedPattern: truncateFinding(fragment, 160),
			})
			if len(out) >= maxFindingsPerScanner {
				return out
			}
		}
	}
	return out
}

func scanWithScanners(content []byte, names []string, scanners map[string]OutputScanner) []outputFinding {
	if len(content) == 0 || len(names) == 0 || len(scanners) == 0 {
		return nil
	}
	out := make([]outputFinding, 0, 8)
	for _, name := range names {
		scanner, ok := scanners[normalizeScannerName(name)]
		if !ok || scanner == nil {
			continue
		}
		out = append(out, scanner.Scan(content)...)
		if len(out) >= maxFindingsPerScanner {
			return out[:maxFindingsPerScanner]
		}
	}
	return out
}

func toProtoOutputFindings(findings []outputFinding) []*pb.OutputFinding {
	if len(findings) == 0 {
		return nil
	}
	out := make([]*pb.OutputFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, &pb.OutputFinding{
			Type:           finding.Type,
			Severity:       finding.Severity,
			Detail:         finding.Detail,
			Offset:         finding.Offset,
			Length:         finding.Length,
			Scanner:        finding.Scanner,
			Confidence:     finding.Confidence,
			MatchedPattern: finding.MatchedPattern,
		})
	}
	return out
}

func compileOutputRules(policy *config.SafetyPolicy) []compiledOutputRule {
	if policy == nil || len(policy.OutputRules) == 0 {
		return nil
	}
	out := make([]compiledOutputRule, 0, len(policy.OutputRules))
	for _, rule := range policy.OutputRules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		decision, ok := parseOutputDecision(rule.Decision)
		if !ok {
			log.Printf("safety-kernel: skipping output rule %q: invalid decision %q", rule.ID, rule.Decision)
			continue
		}

		maxBytes := rule.Match.MaxOutputBytes
		if rule.Match.OutputSizeGt > maxBytes {
			maxBytes = rule.Match.OutputSizeGt
		}

		patterns := make([]compiledOutputPattern, 0, len(rule.Match.ContentPatterns))
		for _, raw := range rule.Match.ContentPatterns {
			pat := strings.TrimSpace(raw)
			if pat == "" {
				continue
			}
			if err := validateRegexComplexity(pat); err != nil {
				log.Printf("safety-kernel: rejecting output rule %q pattern %q: %v", rule.ID, pat, err)
				regexRejectedTotal.Inc()
				continue
			}
			compiled, err := regexp.Compile(pat)
			if err != nil {
				log.Printf("safety-kernel: skipping output rule %q pattern %q: %v", rule.ID, pat, err)
				continue
			}
			patterns = append(patterns, compiledOutputPattern{raw: pat, re: compiled})
		}
		if len(rule.Match.ContentPatterns) > 0 && len(patterns) == 0 {
			continue
		}

		scanners := mergeScannerLists(rule.Match.Scanners, rule.Match.Detectors)
		out = append(out, compiledOutputRule{
			id:             strings.TrimSpace(rule.ID),
			decision:       decision,
			reason:         strings.TrimSpace(rule.Reason),
			severity:       normalizeSeverity(rule.Severity),
			tenants:        normalizeList(rule.Match.Tenants),
			topics:         normalizeList(rule.Match.Topics),
			capabilities:   normalizeList(rule.Match.Capabilities),
			riskTags:       normalizeList(rule.Match.RiskTags),
			contentTypes:   normalizeList(rule.Match.ContentTypes),
			scanners:       scanners,
			patterns:       patterns,
			keywords:       normalizeList(rule.Match.Keywords),
			maxOutputBytes: maxBytes,
			hasError:       rule.Match.HasError,
		})
	}
	return out
}

func parseOutputDecision(raw string) (pb.OutputDecision, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow":
		return pb.OutputDecision_OUTPUT_DECISION_ALLOW, true
	case "quarantine", "deny":
		return pb.OutputDecision_OUTPUT_DECISION_QUARANTINE, true
	case "redact":
		return pb.OutputDecision_OUTPUT_DECISION_REDACT, true
	default:
		return pb.OutputDecision_OUTPUT_DECISION_ALLOW, false
	}
}

func normalizeSeverity(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "critical", "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "high"
	}
}

func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func mergeScannerLists(primary, secondary []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(primary)+len(secondary))
	add := func(values []string) {
		for _, value := range values {
			name := normalizeScannerName(value)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	add(primary)
	add(secondary)
	return out
}

func normalizeScannerName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	switch name {
	case "secret_leak":
		return "secret"
	case "code_injection":
		return "injection"
	default:
		return name
	}
}

func containsAnyFold(values, required []string) bool {
	if len(values) == 0 || len(required) == 0 {
		return false
	}
	for _, value := range values {
		for _, req := range required {
			if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(req)) {
				return true
			}
		}
	}
	return false
}

func loadOutputScanners() map[string]OutputScanner {
	scanners := cloneScanners(defaultOutputScanners())

	path := strings.TrimSpace(os.Getenv(envOutputScannersPath))
	if path == "" {
		path = defaultOutputScannersPath
	}
	if path == "" {
		return scanners
	}
	data, err := os.ReadFile(path) // #nosec G304 -- scanner path is operator-configurable.
	if err != nil {
		if os.IsNotExist(err) {
			return scanners
		}
		log.Printf("safety-kernel: scanner config read failed %q: %v", path, err)
		return scanners
	}

	var cfg outputScannersFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("safety-kernel: scanner config parse failed %q: %v", path, err)
		return scanners
	}
	if len(cfg.Scanners) == 0 {
		return scanners
	}

	for name, spec := range cfg.Scanners {
		scanner := compileScannerSpec(name, spec)
		if scanner == nil {
			continue
		}
		normalized := normalizeScannerName(name)
		scanners[normalized] = scanner
		switch normalized {
		case "secret":
			scanners["secret_leak"] = scanner
		case "injection":
			scanners["code_injection"] = scanner
		}
	}
	log.Printf("safety-kernel: loaded scanner config from %s (%d scanners)", filepath.Clean(path), len(cfg.Scanners))
	return scanners
}

func compileScannerSpec(name string, spec outputScannerSpecFile) OutputScanner {
	normalizedName := normalizeScannerName(name)
	if normalizedName == "" {
		return nil
	}
	findingType := strings.TrimSpace(spec.FindingType)
	if findingType == "" {
		switch normalizedName {
		case "secret":
			findingType = "secret_leak"
		case "injection":
			findingType = "code_injection"
		default:
			findingType = normalizedName
		}
	}

	patterns := make([]regexPattern, 0, len(spec.Patterns))
	for _, pattern := range spec.Patterns {
		raw := strings.TrimSpace(pattern.Regex)
		if raw == "" {
			raw = strings.TrimSpace(pattern.Pattern)
		}
		if raw == "" {
			continue
		}
		re, err := regexp.Compile(raw)
		if err != nil {
			log.Printf("safety-kernel: invalid scanner pattern %s/%s: %v", normalizedName, pattern.Name, err)
			continue
		}
		confidence := pattern.Confidence
		if confidence <= 0 {
			confidence = 0.9
		}
		label := strings.TrimSpace(pattern.Name)
		if label == "" {
			label = "pattern match"
		}
		patterns = append(patterns, regexPattern{
			Label:      label,
			Severity:   normalizeSeverity(pattern.Severity),
			Pattern:    raw,
			Expression: re,
			Confidence: confidence,
		})
	}
	if len(patterns) == 0 {
		return nil
	}
	return newRegexScanner(normalizedName, findingType, patterns)
}

func cloneScanners(in map[string]OutputScanner) map[string]OutputScanner {
	if len(in) == 0 {
		return map[string]OutputScanner{}
	}
	out := make(map[string]OutputScanner, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func normalizePolicyFailMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "closed":
		return "closed"
	default:
		return "open"
	}
}

func outputCheckTimestampMicros() int64 {
	return time.Now().UTC().UnixNano() / int64(time.Microsecond)
}
