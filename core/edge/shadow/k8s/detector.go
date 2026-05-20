// Package k8s implements the observe-only Kubernetes shadow-agent
// detector specified in cordum/docs/edge/kubernetes-ci-shadow-detector-design.md.
//
// The detector polls a Kubernetes cluster with read-only RBAC, extracts
// the 9 signals enumerated in design doc §7.1, maps each candidate to a
// tenant + principal via the §6.1/§6.2 precedence chain, applies
// extraction-time redaction per §5, and persists findings through the
// shared shadow.Store API. It NEVER mutates cluster state: the K8s
// client surface is constrained to read methods by a narrow internal
// adapter (kubeReader) and no Create/Update/Patch/Delete verb is
// reachable from any code path in this package.
package k8s

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/cordum/cordum/core/edge/shadow"
)

// Config is the operator-tunable detector configuration.
//
// Empty fields take per-field defaults via fillDefaults so a zero-value
// Config remains usable for unit tests and bootstrapping.
type Config struct {
	ClusterID                string
	ScanInterval             time.Duration
	TenantLabelKey           string
	PrincipalLabelKey        string
	HeartbeatLabelKey        string
	GatewayAdoptionLabel     string
	HeartbeatMissedThreshold int
	WorkloadAllowlist        []string
	ImageRegistryAllowlist   []string
	KnownAgentExecutables    []string
	KnownAgentImages         []string
	MCPPortNames             []string
	NamespaceAllowlist       []string
	ClusterTenantMap         map[string]string
	LLMProxyEndpoints        []string
	QuarantineTenantID       string
	// AdmissionLog is the operator-supplied feed of admission-controller
	// observations consumed by §7.1 row 7 (admission_observed). The
	// detector never installs a webhook — it only reads what the
	// operator has already captured. Nil = signal disabled.
	AdmissionLog AdmissionLogSource
}

// AdmissionEvent is one row from the operator's admission-controller
// log. Fields are populated from the operator's existing observability
// pipeline; the detector treats them as already-redacted boundary
// inputs and applies extraction-time redaction once more (defense in
// depth) before persistence.
type AdmissionEvent struct {
	Timestamp time.Time
	Namespace string
	Kind      string // workload kind, e.g. "Deployment", "Pod"
	Name      string
	Image     string // primary container image, if applicable
}

// AdmissionLogSource yields the admission events seen since `since`.
// Implementations MUST be observe-only — no webhook installation, no
// admission-decision mutation. Returning an empty slice or nil disables
// the signal for that scan cycle.
type AdmissionLogSource func(ctx context.Context, since time.Time) []AdmissionEvent

// QuarantineTenant is the terminal default tenant when §6.1 precedence
// fails to resolve any other source. Operators can override via
// Config.QuarantineTenantID; the default mirrors design doc §6.1.
const QuarantineTenant = "cordum.shadow.quarantine"

func (c *Config) fillDefaults() {
	if c.TenantLabelKey == "" {
		c.TenantLabelKey = "cordum.io/tenant-id"
	}
	if c.PrincipalLabelKey == "" {
		c.PrincipalLabelKey = "cordum.io/principal-id"
	}
	if c.HeartbeatLabelKey == "" {
		c.HeartbeatLabelKey = "cordum.io/edge-session-id"
	}
	if c.GatewayAdoptionLabel == "" {
		c.GatewayAdoptionLabel = "cordum.io/mcp-gateway"
	}
	if c.HeartbeatMissedThreshold <= 0 {
		c.HeartbeatMissedThreshold = 3
	}
	if c.ScanInterval <= 0 {
		c.ScanInterval = 60 * time.Second
	}
	if c.QuarantineTenantID == "" {
		c.QuarantineTenantID = QuarantineTenant
	}
	if len(c.KnownAgentExecutables) == 0 {
		c.KnownAgentExecutables = []string{"claude", "codex", "cursor", "mcp-server", "mcp-gateway"}
	}
	if len(c.MCPPortNames) == 0 {
		c.MCPPortNames = []string{"mcp", "mcp-stdio", "mcp-sse", "mcp-http"}
	}
}

// kubeReader is the narrow read-only surface of kubernetes.Interface
// the detector consumes. Because the Detector struct holds a kubeReader
// (not a kubernetes.Interface), no production code path in this package
// can reach a mutating verb — Create/Update/Patch/Delete are not
// declared on this interface, so calling them is a compile error per
// design doc §11 observe-mode contract.
type kubeReader interface {
	listPods(ctx context.Context, namespace string) ([]corev1.Pod, error)
	listNamespaces(ctx context.Context) ([]corev1.Namespace, error)
	listServices(ctx context.Context, namespace string) ([]corev1.Service, error)
	getServiceAccount(ctx context.Context, namespace, name string) (*corev1.ServiceAccount, error)
	listNetworkPolicies(ctx context.Context, namespace string) ([]networkingv1.NetworkPolicy, error)
}

// kubeClientAdapter wraps a kubernetes.Interface and exposes only the
// read methods kubeReader declares. The adapter is the single boundary
// between the broad K8s client interface (which has mutating methods)
// and the rest of this package (which cannot see them).
type kubeClientAdapter struct {
	client kubernetes.Interface
}

func newKubeAdapter(client kubernetes.Interface) kubeReader {
	return &kubeClientAdapter{client: client}
}

func (a *kubeClientAdapter) listPods(ctx context.Context, ns string) ([]corev1.Pod, error) {
	list, err := a.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (a *kubeClientAdapter) listNamespaces(ctx context.Context) ([]corev1.Namespace, error) {
	list, err := a.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (a *kubeClientAdapter) listServices(ctx context.Context, ns string) ([]corev1.Service, error) {
	list, err := a.client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (a *kubeClientAdapter) getServiceAccount(ctx context.Context, ns, name string) (*corev1.ServiceAccount, error) {
	return a.client.CoreV1().ServiceAccounts(ns).Get(ctx, name, metav1.GetOptions{})
}

func (a *kubeClientAdapter) listNetworkPolicies(ctx context.Context, ns string) ([]networkingv1.NetworkPolicy, error) {
	list, err := a.client.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Detector is the per-cluster observe-mode shadow-agent detector. It is
// safe for concurrent Scan invocations; scanState is mutex-guarded.
type Detector struct {
	config   Config
	reader   kubeReader
	store    shadow.Store
	observer Observer
	resolver TenantResolver

	mu       sync.Mutex
	clock    func() time.Time
	state    *scanState
	sourceID string
}

// scanState carries cross-scan-cycle bookkeeping required by §14
// false-positive controls (heartbeat-missed counters per pod) and the
// ephemeral indicator's diff-since-last-scan semantics.
type scanState struct {
	heartbeatMissCount map[string]int             // key: "<ns>/<name>"
	priorPodKeys       map[string]struct{}        // key: "<ns>/<name>" present last scan
	priorPodMetadata   map[string]ephemeralRecord // key: same; carries last-known kind+image for diff promotion
}

type ephemeralRecord struct {
	Image string
}

// NewDetector wires the Detector with its dependencies. Pass a nil
// resolver to use the design-doc §6.1/§6.2 default precedence chain;
// pass a custom resolver to bypass label-driven mapping in tests or
// for operator-managed override paths. A nil observer defaults to
// NoopObserver so callers that have not yet wired observability do
// not panic.
func NewDetector(cfg Config, client kubernetes.Interface, store shadow.Store, observer Observer, resolver TenantResolver) *Detector {
	cfg.fillDefaults()
	if observer == nil {
		observer = NewNoopObserver()
	}
	if resolver == nil {
		resolver = newDefaultResolver(cfg)
	}
	return &Detector{
		config:   cfg,
		reader:   newKubeAdapter(client),
		store:    store,
		observer: observer,
		resolver: resolver,
		clock:    time.Now,
		state: &scanState{
			heartbeatMissCount: map[string]int{},
			priorPodKeys:       map[string]struct{}{},
			priorPodMetadata:   map[string]ephemeralRecord{},
		},
		sourceID: "k8s-detector-" + cfg.ClusterID,
	}
}

// SetClock overrides the time source. Used by tests to pin DetectedAt
// + first_seen / last_seen so finding assertions remain deterministic
// across runs without monkey-patching time.Now.
func (d *Detector) SetClock(fn func() time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clock = fn
}

// Run blocks until ctx is canceled, invoking Scan on each
// config.ScanInterval tick. Returns ctx.Err() on cancellation; never
// returns a per-scan error (scan failures are logged before the next tick).
func (d *Detector) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.config.ScanInterval)
	defer ticker.Stop()
	for {
		if err := d.Scan(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Error("k8s detector: scan error", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Scan performs one full detection pass: lists pods, namespaces,
// services, and network policies; runs each §7.1 extractor; maps
// tenant + principal; redacts; and emits findings through the
// configured shadow.Store. Errors from individual List calls are
// surfaced once and abort the cycle — there is no partial-state emit.
func (d *Detector) Scan(ctx context.Context) error {
	pods, err := d.reader.listPods(ctx, "")
	if err != nil {
		return err
	}
	namespaces, err := d.reader.listNamespaces(ctx)
	if err != nil {
		return err
	}
	services, err := d.reader.listServices(ctx, "")
	if err != nil {
		return err
	}
	netpols, err := d.reader.listNetworkPolicies(ctx, "")
	if err != nil {
		return err
	}

	nsByName := indexNamespaces(namespaces)
	d.mu.Lock()
	now := d.clock()

	// collectSignals reads prior-scan state (priorPodKeys /
	// priorPodMetadata) for the ephemeral_indicator diff. Inventory
	// snapshot MUST happen after the read so the next cycle sees this
	// scan's pods, not the prior cycle's.
	candidates := d.collectSignals(ctx, pods, namespaces, services, netpols, now)
	candidates = applyEphemeralCorroboration(candidates)
	d.updateInventory(pods, now)
	d.mu.Unlock()
	for _, cand := range candidates {
		d.emit(ctx, cand, nsByName, now)
	}
	return nil
}

func indexNamespaces(in []corev1.Namespace) map[string]*corev1.Namespace {
	out := make(map[string]*corev1.Namespace, len(in))
	for i := range in {
		out[in[i].Name] = &in[i]
	}
	return out
}

func podKey(ns, name string) string { return ns + "/" + name }

// updateInventory snapshots the current pod set so the next Scan can
// compute the ephemeral-indicator diff (pods that disappeared between
// successive scans). §14 forbids auto-promoting ephemeral findings
// without corroboration, so the inventory drives signal recall, not a
// standalone finding.
func (d *Detector) updateInventory(pods []corev1.Pod, _ time.Time) {
	next := make(map[string]struct{}, len(pods))
	meta := make(map[string]ephemeralRecord, len(pods))
	for i := range pods {
		k := podKey(pods[i].Namespace, pods[i].Name)
		next[k] = struct{}{}
		if len(pods[i].Spec.Containers) > 0 {
			meta[k] = ephemeralRecord{Image: pods[i].Spec.Containers[0].Image}
		}
	}
	d.state.priorPodKeys = next
	d.state.priorPodMetadata = meta
}
