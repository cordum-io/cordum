package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cordum/cordum/core/edge/shadow"
	"github.com/cordum/cordum/core/edge/shadow/k8s"
)

const (
	shadowClusterPreviewMode = "shadow_cluster_preview"
	shadowCIPreviewMode      = "shadow_ci_preview"
	shadowPreviewTimeout     = 30 * time.Second
)

// edgeDoctorShadowJSONEnvelope is the --json output shape for the
// --shadow-cluster / --shadow-ci preview flags. DryRun is always true —
// nothing in these code paths ever persists to the EDGE-141 store or
// mutates remote state. Findings is always present (empty slice, not
// nil) so JSON consumers can iterate without nil-guards.
type edgeDoctorShadowJSONEnvelope struct {
	Mode     string                      `json:"mode"`
	DryRun   bool                        `json:"dry_run"`
	Provider string                      `json:"provider,omitempty"`
	Findings []shadow.ShadowAgentFinding `json:"findings"`
}

// edgeDoctorKubeClientBuilder constructs a kubernetes.Interface from an
// operator-supplied kubeconfig path. Tests override this with a fake
// clientset so K8s API traffic stays in-process.
var edgeDoctorKubeClientBuilder = defaultKubeClientBuilder

func defaultKubeClientBuilder(kubeconfigPath string) (kubernetes.Interface, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfigPath
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q: %w", kubeconfigPath, err)
	}
	return kubernetes.NewForConfig(restCfg)
}

// edgeDoctorCIHTTPTransport is the http.RoundTripper the CI dry-run
// detectors WILL use once EDGE-143.2/.3 ship. Tests inject a recording
// transport so the read-only invariant (zero POST/PUT/PATCH/DELETE)
// stays asserted as soon as those tasks land.
var edgeDoctorCIHTTPTransport http.RoundTripper = http.DefaultTransport

// supportedCIProviders is the closed enum accepted by --shadow-ci. The
// list MUST stay in sync with the §10.1 CIProvider* constants and the
// EDGE-143.2/.3 provider modules; unknown providers print a clear list.
var supportedCIProviders = []string{"github", "gitlab", "jenkins", "buildkite", "circleci"}

// newEdgeDoctorFlagSet returns a stand-alone flagSet exposing only the
// two new shadow flags. Tests use it to introspect flag registration
// without driving the full doctor pipeline; production calls
// registerEdgeDoctorShadowFlags directly on the existing flagSet.
func newEdgeDoctorFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("edge doctor", flag.ContinueOnError)
	_, _ = registerEdgeDoctorShadowFlags(fs)
	return fs
}

func registerEdgeDoctorShadowFlags(fs *flag.FlagSet) (*string, *string) {
	cluster := fs.String("shadow-cluster", "",
		"path to a kubeconfig file; runs the EDGE-143.1 K8s shadow detector in dry-run mode "+
			"(findings printed to stdout, never persisted, never mutates the cluster). "+
			"Example: --shadow-cluster ~/.kube/config")
	ci := fs.String("shadow-ci", "",
		"<provider>:<token-or-config-path>; runs the EDGE-143.2/.3 CI shadow detector "+
			"for the named provider in dry-run mode. Supported providers: "+
			strings.Join(supportedCIProviders, "/")+". Example: --shadow-ci github:$GITHUB_TOKEN")
	return cluster, ci
}

// dryRunStore is the in-memory shadow.Store impl used by every preview
// path. CreateFinding records to findings; every other method either
// returns ErrNotFound or an empty page so no caller can mistake dry-run
// state for the persisted EDGE-141 store. The struct is single-call —
// concurrent Scan invocations are not expected, so no mutex.
type dryRunStore struct {
	findings []shadow.ShadowAgentFinding
}

func (s *dryRunStore) CreateFinding(_ context.Context, req shadow.CreateFindingRequest) (*shadow.ShadowAgentFinding, error) {
	now := time.Now().UTC()
	id := strings.TrimSpace(req.FindingID)
	if id == "" {
		id = fmt.Sprintf("preview_%d", len(s.findings)+1)
	}
	f := shadow.ShadowAgentFinding{
		FindingID:           id,
		TenantID:            req.TenantID,
		OwnerPrincipalID:    req.OwnerPrincipalID,
		PrincipalID:         req.PrincipalID,
		AgentProduct:        req.AgentProduct,
		AgentID:             req.AgentID,
		Hostname:            req.Hostname,
		Risk:                req.Risk,
		Status:              shadow.FindingStatusDetected,
		EvidenceType:        req.EvidenceType,
		EvidenceSummary:     req.EvidenceSummary,
		EvidenceArtifact:    req.EvidenceArtifact,
		RedactedPath:        req.RedactedPath,
		DetectedAt:          req.DetectedAt,
		CreatedAt:           now,
		UpdatedAt:           now,
		Metadata:            req.Metadata,
		SourceType:          req.SourceType,
		SourceID:            req.SourceID,
		ClusterID:           req.ClusterID,
		Namespace:           req.Namespace,
		WorkloadKind:        req.WorkloadKind,
		WorkloadName:        req.WorkloadName,
		PodUID:              req.PodUID,
		CIProvider:          req.CIProvider,
		Repo:                req.Repo,
		Ref:                 req.Ref,
		WorkflowID:          req.WorkflowID,
		JobID:               req.JobID,
		RunID:               req.RunID,
		RunnerID:            req.RunnerID,
		TenantSource:        req.TenantSource,
		PrincipalSource:     req.PrincipalSource,
		SignalSet:           req.SignalSet,
		Confidence:          req.Confidence,
		FirstSeen:           req.FirstSeen,
		LastSeen:            req.LastSeen,
		FalsePositiveReason: req.FalsePositiveReason,
		ExceptionID:         req.ExceptionID,
		RetentionClass:      req.RetentionClass,
	}
	s.findings = append(s.findings, f)
	out := f
	return &out, nil
}

func (s *dryRunStore) GetFinding(context.Context, string, string) (*shadow.ShadowAgentFinding, error) {
	return nil, shadow.ErrNotFound
}

func (s *dryRunStore) ListFindings(context.Context, shadow.ListFindingsQuery) (shadow.FindingPage, error) {
	return shadow.FindingPage{}, nil
}

func (s *dryRunStore) ResolveFinding(context.Context, string, string, shadow.ResolveRequest) (*shadow.ShadowAgentFinding, error) {
	return nil, shadow.ErrNotFound
}

func (s *dryRunStore) SuppressFinding(context.Context, string, string, shadow.SuppressRequest) (*shadow.ShadowAgentFinding, error) {
	return nil, shadow.ErrNotFound
}

func (s *dryRunStore) CreateException(context.Context, shadow.CreateExceptionRequest) (*shadow.Exception, error) {
	return nil, shadow.ErrNotFound
}

func (s *dryRunStore) GetException(context.Context, string, string) (*shadow.Exception, error) {
	return nil, shadow.ErrNotFound
}

func (s *dryRunStore) ListExceptions(context.Context, shadow.ListExceptionsQuery) (shadow.ExceptionPage, error) {
	return shadow.ExceptionPage{}, nil
}

func (s *dryRunStore) RevokeException(context.Context, string, string, shadow.RevokeExceptionRequest) (*shadow.Exception, error) {
	return nil, shadow.ErrNotFound
}

func (s *dryRunStore) MatchActiveExceptions(context.Context, *shadow.ShadowAgentFinding) ([]shadow.Exception, error) {
	return nil, nil
}

// runShadowClusterPreview invokes the EDGE-143.1 K8s detector in
// dry-run mode against the operator-supplied kubeconfig. Bounded by
// shadowPreviewTimeout so a slow cluster never hangs the CLI; findings
// are emitted to stdout via JSON or human helpers.
func runShadowClusterPreview(kubeconfigPath string, asJSON bool, stdout, stderr io.Writer) int {
	if strings.TrimSpace(kubeconfigPath) == "" {
		_, _ = fmt.Fprintln(stderr, "cordumctl edge doctor: --shadow-cluster requires a kubeconfig path")
		return 2
	}
	client, err := edgeDoctorKubeClientBuilder(kubeconfigPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge doctor: --shadow-cluster: %s\n", err.Error())
		return 1
	}

	store := &dryRunStore{}
	detector := k8s.NewDetector(defaultEdgeDoctorShadowK8sConfig(), client, store, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), shadowPreviewTimeout)
	defer cancel()
	if err := detector.Scan(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge doctor: --shadow-cluster scan failed: %s\n", err.Error())
		return 1
	}

	if asJSON {
		return emitShadowFindingsJSON(stdout, shadowClusterPreviewMode, "", store.findings)
	}
	return emitShadowFindingsHuman(stdout, "shadow-cluster preview", store.findings)
}

// defaultEdgeDoctorShadowK8sConfig is the bootstrap config the preview
// path supplies to the K8s detector. Mirrors the test-fixture defaults
// (k8s/detector_test.go:71-82) so operator output during dry-run looks
// like what production wiring would surface.
func defaultEdgeDoctorShadowK8sConfig() k8s.Config {
	return k8s.Config{
		ClusterID:              "cordumctl-preview",
		KnownAgentImages:       []string{"anthropic/claude-code", "openai/codex", "cursor/agent"},
		KnownAgentExecutables:  []string{"claude", "codex", "cursor", "mcp-server", "mcp-gateway"},
		ImageRegistryAllowlist: []string{"anthropic", "openai", "cursor", "ghcr.io/cordum"},
		MCPPortNames:           []string{"mcp", "mcp-stdio", "mcp-sse", "mcp-http"},
	}
}

// runShadowCIPreview parses the operator-supplied <provider>:<token>
// spec and dispatches to the named CI provider's dry-run detector. As
// of EDGE-143.8 landing, neither EDGE-143.2 (github) nor EDGE-143.3
// (gitlab/jenkins/buildkite/circleci) has shipped a detector module —
// every supported provider therefore prints a clear actionable message
// instead of silently skipping.
func runShadowCIPreview(spec string, asJSON bool, stdout, stderr io.Writer) int {
	provider, configRef, ok := splitShadowCISpec(spec)
	if !ok {
		_, _ = fmt.Fprintf(stderr,
			"cordumctl edge doctor: --shadow-ci value must be in <provider>:<token-or-config-path> format; got %q\n",
			spec)
		return 2
	}
	knownProvider := false
	for _, p := range supportedCIProviders {
		if provider == p {
			knownProvider = true
			break
		}
	}
	if !knownProvider {
		_, _ = fmt.Fprintf(stderr,
			"cordumctl edge doctor: provider %s not recognized; supported: %s\n",
			provider, strings.Join(supportedCIProviders, "/"))
		return 2
	}

	// Keep the CI HTTP transport seam referenced so tests can record
	// any future mutating verb; today no detector is wired so no
	// request is ever issued.
	_ = edgeDoctorCIHTTPTransport
	_ = configRef

	_, _ = fmt.Fprintf(stderr,
		"cordumctl edge doctor: provider %s not supported in this build; EDGE-143.2/.3 detector(s) must DONE first\n",
		provider)
	_ = asJSON
	_ = stdout
	return 1
}

func splitShadowCISpec(spec string) (provider, configRef string, ok bool) {
	spec = strings.TrimSpace(spec)
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	provider = strings.ToLower(strings.TrimSpace(parts[0]))
	if provider == "" {
		return "", "", false
	}
	configRef = strings.TrimSpace(parts[1])
	return provider, configRef, true
}

func emitShadowFindingsHuman(w io.Writer, banner string, findings []shadow.ShadowAgentFinding) int {
	if _, err := fmt.Fprintf(w, "Cordum Edge doctor — %s (dry-run, no findings persisted)\n", banner); err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(w, "Findings: %d\n", len(findings)); err != nil {
		return 1
	}
	for _, f := range findings {
		if _, err := fmt.Fprintf(w, "  - [%s] source=%s ns=%s workload=%s signals=%s evidence=%s\n",
			f.Risk, f.SourceType, f.Namespace, f.WorkloadName,
			strings.Join(f.SignalSet, ","), f.EvidenceType); err != nil {
			return 1
		}
	}
	return 0
}

func emitShadowFindingsJSON(w io.Writer, mode, provider string, findings []shadow.ShadowAgentFinding) int {
	if findings == nil {
		findings = []shadow.ShadowAgentFinding{}
	}
	env := edgeDoctorShadowJSONEnvelope{
		Mode:     mode,
		DryRun:   true,
		Provider: provider,
		Findings: findings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		return 1
	}
	return 0
}
