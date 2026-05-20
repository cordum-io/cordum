package k8s

import (
	"context"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// signalCandidate is the pre-tenant-mapping, pre-emit representation
// of a detector finding. The emit pipeline (detector.emit) does the
// tenant + principal mapping, builds the shadow.CreateFindingRequest,
// applies extraction-time redaction once more as defense-in-depth, and
// calls store.CreateFinding.
//
// All string fields on this struct MUST already be redactField'ed by
// the extractor that produced them. The emit pipeline trusts the
// extractor + double-applies redactField to fail closed if a future
// regression skips it.
type signalCandidate struct {
	Signal          string // §7.1 enum: heartbeat_missing, unmanaged_process, ...
	EvidenceType    string // §10.1 evidence_type, e.g. k8s_heartbeat_missing
	Risk            shadow.FindingRisk
	AgentProduct    string
	WorkloadKind    string
	WorkloadName    string
	PodUID          string
	Namespace       string
	EvidenceSummary string
	SignalSet       []string

	// SourcePod + SourceSA are the inputs the tenant resolver needs.
	// SourceSA may be nil; mapping.ResolveTenant tolerates nil.
	SourcePod *corev1.Pod
	SourceSA  *corev1.ServiceAccount
}

// collectSignals runs every §7.1 extractor against the listed
// resources and returns the union of candidates. Order is deterministic
// across runs: heartbeat → process → mcp service → workload → image →
// untenanted ns → admission → egress → ephemeral. The ephemeral
// indicator emits per-disappeared-pod candidates that the
// applyEphemeralCorroboration pass filters against §14 FP rules.
func (d *Detector) collectSignals(
	ctx context.Context,
	pods []corev1.Pod,
	namespaces []corev1.Namespace,
	services []corev1.Service,
	netpols []networkingv1.NetworkPolicy,
	now time.Time,
) []signalCandidate {
	var out []signalCandidate
	out = append(out, d.heartbeatMissingSignal(pods)...)
	out = append(out, d.unmanagedProcessSignal(pods)...)
	out = append(out, d.unmanagedMCPServiceSignal(services)...)
	out = append(out, d.unmanagedWorkloadSignal(pods)...)
	out = append(out, d.untrustedAgentImageSignal(pods)...)
	out = append(out, d.namespaceUntenantedSignal(namespaces, pods)...)
	out = append(out, d.admissionObservedSignal(ctx, now)...)
	out = append(out, d.egressBypassSignal(netpols)...)
	out = append(out, d.ephemeralIndicatorSignal(pods)...)
	return out
}

// applyEphemeralCorroboration drops ephemeral_indicator candidates
// unless another signal in the same scan touches the same namespace.
// §14 FP control: a lone pod disappearance is noise (normal restarts,
// scale-down) — promote only when another agent-shaped signal is
// already firing in that namespace this cycle. Other signals pass
// through unchanged.
func applyEphemeralCorroboration(in []signalCandidate) []signalCandidate {
	if len(in) == 0 {
		return in
	}
	corroboratingNs := make(map[string]struct{}, len(in))
	for _, c := range in {
		if c.Signal == "ephemeral_indicator" {
			continue
		}
		if c.Namespace == "" {
			continue
		}
		corroboratingNs[c.Namespace] = struct{}{}
	}
	out := make([]signalCandidate, 0, len(in))
	for _, c := range in {
		if c.Signal != "ephemeral_indicator" {
			out = append(out, c)
			continue
		}
		if c.Namespace == "" {
			continue
		}
		if _, ok := corroboratingNs[c.Namespace]; ok {
			out = append(out, c)
		}
	}
	return out
}

// matchKnownAgentImage returns the product name if the image matches
// any known agent image prefix; otherwise empty.
func (d *Detector) matchKnownAgentImage(image string) string {
	low := strings.ToLower(image)
	for _, prefix := range d.config.KnownAgentImages {
		if strings.HasPrefix(low, strings.ToLower(prefix)) {
			return productFromImagePrefix(prefix)
		}
	}
	// Heuristic fallback for the untrusted-image extractor: substring
	// match against any known executable token. Distinguishes "evil/
	// claude-agent" from "ubuntu" without needing operator config.
	for _, tok := range d.config.KnownAgentExecutables {
		if strings.Contains(low, tok) {
			return productFromExecutable(tok)
		}
	}
	return ""
}

func productFromImagePrefix(prefix string) string {
	parts := strings.Split(strings.ToLower(prefix), "/")
	if len(parts) == 0 {
		return "unknown"
	}
	last := parts[len(parts)-1]
	if i := strings.IndexAny(last, ":@"); i >= 0 {
		last = last[:i]
	}
	return last
}

func productFromExecutable(tok string) string {
	switch tok {
	case "claude":
		return "claude-code"
	case "codex":
		return "codex"
	case "cursor":
		return "cursor"
	case "mcp-server", "mcp-gateway":
		return tok
	default:
		return tok
	}
}

// heartbeatMissingSignal: §7.1 row 1. Pods matching a known agent
// image but missing the Cordum heartbeat label. §14 N-poll gate: only
// promote after Config.HeartbeatMissedThreshold consecutive scans.
func (d *Detector) heartbeatMissingSignal(pods []corev1.Pod) []signalCandidate {
	if d.state == nil {
		d.state = &scanState{}
	}
	if d.state.heartbeatMissCount == nil {
		d.state.heartbeatMissCount = map[string]int{}
	}
	var out []signalCandidate
	current := make(map[string]struct{}, len(pods))
	for i := range pods {
		pod := &pods[i]
		key := podKey(pod.Namespace, pod.Name)
		current[key] = struct{}{}
		if len(pod.Spec.Containers) == 0 {
			continue
		}
		product := d.matchKnownAgentImage(pod.Spec.Containers[0].Image)
		if product == "" {
			delete(d.state.heartbeatMissCount, key)
			continue
		}
		if _, ok := pod.Labels[d.config.HeartbeatLabelKey]; ok {
			delete(d.state.heartbeatMissCount, key)
			continue
		}
		d.state.heartbeatMissCount[key]++
		if d.state.heartbeatMissCount[key] < d.config.HeartbeatMissedThreshold {
			continue
		}
		out = append(out, signalCandidate{
			Signal:          "heartbeat_missing",
			EvidenceType:    "k8s_heartbeat_missing",
			Risk:            shadow.FindingRiskMedium,
			AgentProduct:    product,
			WorkloadKind:    "Pod",
			WorkloadName:    redactField(pod.Name),
			PodUID:          string(pod.UID),
			Namespace:       redactField(pod.Namespace),
			EvidenceSummary: redactField("pod missing cordum heartbeat label; image=" + imageTagSafe(pod.Spec.Containers[0].Image)),
			SignalSet:       []string{"heartbeat_missing"},
			SourcePod:       pod,
		})
	}
	for key := range d.state.heartbeatMissCount {
		if _, ok := current[key]; !ok {
			delete(d.state.heartbeatMissCount, key)
		}
	}
	return out
}

// unmanagedProcessSignal: §7.1 row 2. Pod whose container command/args
// leading token matches a known agent executable AND the pod itself is
// not heartbeat-labeled (i.e., not under Cordum governance). Per §5.2
// MUST-NOT, only the leading token is captured — never subsequent args
// (e.g. --prompt VALUE).
func (d *Detector) unmanagedProcessSignal(pods []corev1.Pod) []signalCandidate {
	var out []signalCandidate
	for i := range pods {
		pod := &pods[i]
		if _, governed := pod.Labels[d.config.HeartbeatLabelKey]; governed {
			continue
		}
		token, product := d.extractAgentToken(pod)
		if token == "" {
			continue
		}
		out = append(out, signalCandidate{
			Signal:          "unmanaged_process",
			EvidenceType:    "k8s_unmanaged_process",
			Risk:            shadow.FindingRiskMedium,
			AgentProduct:    product,
			WorkloadKind:    "Pod",
			WorkloadName:    redactField(pod.Name),
			PodUID:          string(pod.UID),
			Namespace:       redactField(pod.Namespace),
			EvidenceSummary: redactField("unmanaged process leading token=" + token),
			SignalSet:       []string{"unmanaged_process"},
			SourcePod:       pod,
		})
	}
	return out
}

func (d *Detector) extractAgentToken(pod *corev1.Pod) (string, string) {
	for _, c := range pod.Spec.Containers {
		cands := []string{}
		if len(c.Command) > 0 {
			cands = append(cands, leadingToken(c.Command[0]))
		}
		if len(c.Args) > 0 {
			cands = append(cands, leadingToken(c.Args[0]))
		}
		for _, cand := range cands {
			for _, tok := range d.config.KnownAgentExecutables {
				if cand == tok {
					return tok, productFromExecutable(tok)
				}
			}
		}
	}
	return "", ""
}

// unmanagedMCPServiceSignal: §7.1 row 3. Service whose port name is
// one of the MCP transport keywords AND missing the gateway-adoption
// label that marks it as Cordum-governed.
func (d *Detector) unmanagedMCPServiceSignal(services []corev1.Service) []signalCandidate {
	var out []signalCandidate
	for i := range services {
		svc := &services[i]
		if _, ok := svc.Labels[d.config.GatewayAdoptionLabel]; ok {
			continue
		}
		hasMCPPort := false
		for _, p := range svc.Spec.Ports {
			for _, name := range d.config.MCPPortNames {
				if p.Name == name {
					hasMCPPort = true
					break
				}
			}
			if hasMCPPort {
				break
			}
		}
		if !hasMCPPort {
			continue
		}
		out = append(out, signalCandidate{
			Signal:          "unmanaged_mcp_service",
			EvidenceType:    "k8s_unmanaged_mcp_service",
			Risk:            shadow.FindingRiskMedium,
			AgentProduct:    "mcp-server",
			WorkloadKind:    "Service",
			WorkloadName:    redactField(svc.Name),
			Namespace:       redactField(svc.Namespace),
			EvidenceSummary: redactField("MCP-named service without gateway adoption label"),
			SignalSet:       []string{"unmanaged_mcp_service"},
		})
	}
	return out
}

// unmanagedWorkloadSignal: §7.1 row 4. Pod whose image matches an
// agent image but whose owning Deployment/DaemonSet/StatefulSet is not
// on the operator allowlist. Emit at the owner workload level so a
// 100-replica rogue deployment surfaces as ONE finding, not 100.
func (d *Detector) unmanagedWorkloadSignal(pods []corev1.Pod) []signalCandidate {
	seen := map[string]struct{}{}
	var out []signalCandidate
	for i := range pods {
		pod := &pods[i]
		if len(pod.Spec.Containers) == 0 {
			continue
		}
		product := d.matchKnownAgentImage(pod.Spec.Containers[0].Image)
		if product == "" {
			continue
		}
		for _, owner := range pod.OwnerReferences {
			if !isWorkloadKind(owner.Kind) {
				continue
			}
			if containsString(d.config.WorkloadAllowlist, owner.Name) {
				continue
			}
			key := owner.Kind + "/" + pod.Namespace + "/" + owner.Name
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, signalCandidate{
				Signal:          "unmanaged_workload",
				EvidenceType:    "k8s_unmanaged_workload",
				Risk:            shadow.FindingRiskMedium,
				AgentProduct:    product,
				WorkloadKind:    owner.Kind,
				WorkloadName:    redactField(owner.Name),
				Namespace:       redactField(pod.Namespace),
				EvidenceSummary: redactField("workload owns agent-image pod outside allowlist"),
				SignalSet:       []string{"unmanaged_workload"},
				SourcePod:       pod,
			})
		}
	}
	return out
}

func isWorkloadKind(kind string) bool {
	switch kind {
	case "Deployment", "DaemonSet", "StatefulSet", "Job", "CronJob", "ReplicaSet":
		return true
	}
	return false
}

func containsString(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}

// untrustedAgentImageSignal: §7.1 row 5. Pod whose image registry
// prefix is NOT in ImageRegistryAllowlist AND whose image name
// suggests an agent product. Risk=low per §7.1.
func (d *Detector) untrustedAgentImageSignal(pods []corev1.Pod) []signalCandidate {
	var out []signalCandidate
	for i := range pods {
		pod := &pods[i]
		if len(pod.Spec.Containers) == 0 {
			continue
		}
		image := pod.Spec.Containers[0].Image
		product := d.matchKnownAgentImage(image)
		if product == "" {
			continue
		}
		if d.imageTrusted(image) {
			continue
		}
		out = append(out, signalCandidate{
			Signal:          "untrusted_agent_image",
			EvidenceType:    "k8s_untrusted_agent_image",
			Risk:            shadow.FindingRiskLow,
			AgentProduct:    product,
			WorkloadKind:    "Pod",
			WorkloadName:    redactField(pod.Name),
			PodUID:          string(pod.UID),
			Namespace:       redactField(pod.Namespace),
			EvidenceSummary: redactField("image registry not in operator allowlist; image=" + imageTagSafe(image)),
			SignalSet:       []string{"untrusted_agent_image"},
			SourcePod:       pod,
		})
	}
	return out
}

func (d *Detector) imageTrusted(image string) bool {
	low := strings.ToLower(image)
	for _, prefix := range d.config.ImageRegistryAllowlist {
		if strings.HasPrefix(low, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// namespaceUntenantedSignal: §7.1 row 6. Namespace missing tenant
// label AND containing at least one shadow indicator (an agent-image
// pod). Per §14 the aggregation guards against single-pod noise: at
// least one corroborating indicator must be present.
func (d *Detector) namespaceUntenantedSignal(namespaces []corev1.Namespace, pods []corev1.Pod) []signalCandidate {
	var out []signalCandidate
	for i := range namespaces {
		ns := &namespaces[i]
		if _, ok := ns.Labels[d.config.TenantLabelKey]; ok {
			continue
		}
		if containsString(d.config.NamespaceAllowlist, ns.Name) {
			continue
		}
		var indicator *corev1.Pod
		for j := range pods {
			if pods[j].Namespace != ns.Name {
				continue
			}
			if len(pods[j].Spec.Containers) == 0 {
				continue
			}
			if d.matchKnownAgentImage(pods[j].Spec.Containers[0].Image) != "" {
				indicator = &pods[j]
				break
			}
		}
		if indicator == nil {
			continue
		}
		out = append(out, signalCandidate{
			Signal:          "namespace_untenanted",
			EvidenceType:    "k8s_namespace_untenanted",
			Risk:            shadow.FindingRiskLow,
			AgentProduct:    "unknown",
			WorkloadKind:    "Namespace",
			WorkloadName:    redactField(ns.Name),
			Namespace:       redactField(ns.Name),
			EvidenceSummary: redactField("namespace missing tenant label; has agent-image pod"),
			SignalSet:       []string{"namespace_untenanted"},
			// SourcePod intentionally nil — tenant_source resolves to
			// quarantine on namespace findings.
		})
	}
	return out
}

// egressBypassSignal: §7.1 row 8. NetworkPolicy egress rule whose To
// destination is broader than the operator's LLM proxy allowlist. The
// detector flags any policy that allows 0.0.0.0/0 — common "open
// internet" configuration that bypasses operator-mandated egress
// proxying. Risk=high per §7.1.
func (d *Detector) egressBypassSignal(netpols []networkingv1.NetworkPolicy) []signalCandidate {
	var out []signalCandidate
	for i := range netpols {
		np := &netpols[i]
		for _, rule := range np.Spec.Egress {
			if !d.egressIsBroad(rule) {
				continue
			}
			out = append(out, signalCandidate{
				Signal:          "egress_bypass",
				EvidenceType:    "k8s_egress_bypass",
				Risk:            shadow.FindingRiskHigh,
				AgentProduct:    "unknown",
				WorkloadKind:    "NetworkPolicy",
				WorkloadName:    redactField(np.Name),
				Namespace:       redactField(np.Namespace),
				EvidenceSummary: redactField("NetworkPolicy permits broad egress outside LLM-proxy scope"),
				SignalSet:       []string{"egress_bypass"},
			})
			break // one finding per policy, not per rule
		}
	}
	return out
}

func (d *Detector) egressIsBroad(rule networkingv1.NetworkPolicyEgressRule) bool {
	if len(rule.To) == 0 {
		return true // empty To means "all destinations" per K8s NetworkPolicy semantics
	}
	for _, peer := range rule.To {
		if peer.IPBlock != nil && (peer.IPBlock.CIDR == "0.0.0.0/0" || peer.IPBlock.CIDR == "::/0") {
			return true
		}
	}
	return false
}

// admissionObservedSignal: §7.1 row 7. Replays the operator's existing
// admission-controller log; the detector NEVER installs a webhook or
// emits an admission decision. A signal fires when an admission event
// references a known-agent image — a workload was admitted to the
// cluster carrying agent identity. Nil log source disables the signal.
func (d *Detector) admissionObservedSignal(ctx context.Context, now time.Time) []signalCandidate {
	if d.config.AdmissionLog == nil {
		return nil
	}
	since := now.Add(-d.config.ScanInterval)
	events := d.config.AdmissionLog(ctx, since)
	var out []signalCandidate
	for _, ev := range events {
		product := d.matchKnownAgentImage(ev.Image)
		if product == "" {
			continue
		}
		out = append(out, signalCandidate{
			Signal:          "admission_observed",
			EvidenceType:    "k8s_admission_observed",
			Risk:            shadow.FindingRiskLow,
			AgentProduct:    redactField(product),
			WorkloadKind:    redactField(ev.Kind),
			WorkloadName:    redactField(ev.Name),
			Namespace:       redactField(ev.Namespace),
			EvidenceSummary: redactField("admission webhook observed " + ev.Kind + "/" + ev.Name + " carrying agent image"),
			SignalSet:       []string{"admission_observed"},
		})
	}
	return out
}

// ephemeralIndicatorSignal: §7.1 row 9. Pods present last scan but
// absent this scan. Promotes the disappearance to a candidate when the
// prior image matched a known agent; applyEphemeralCorroboration then
// gates emission against §14 (other concurrent signals in same ns).
// Stand-alone disappearances stay candidates only — never emitted —
// per the design-doc no-auto-promotion contract.
func (d *Detector) ephemeralIndicatorSignal(currentPods []corev1.Pod) []signalCandidate {
	if len(d.state.priorPodKeys) == 0 {
		return nil
	}
	curr := make(map[string]struct{}, len(currentPods))
	for i := range currentPods {
		curr[podKey(currentPods[i].Namespace, currentPods[i].Name)] = struct{}{}
	}
	var out []signalCandidate
	for k := range d.state.priorPodKeys {
		if _, stillThere := curr[k]; stillThere {
			continue
		}
		meta := d.state.priorPodMetadata[k]
		product := d.matchKnownAgentImage(meta.Image)
		if product == "" {
			continue // disappearance of a non-agent pod is not interesting
		}
		ns, name := splitPodKey(k)
		out = append(out, signalCandidate{
			Signal:          "ephemeral_indicator",
			EvidenceType:    "k8s_ephemeral_indicator",
			Risk:            shadow.FindingRiskLow,
			AgentProduct:    redactField(product),
			WorkloadKind:    "Pod",
			WorkloadName:    redactField(name),
			Namespace:       redactField(ns),
			EvidenceSummary: redactField("agent-image pod disappeared between scans"),
			SignalSet:       []string{"ephemeral_indicator"},
		})
	}
	return out
}

func splitPodKey(k string) (ns, name string) {
	if i := strings.IndexByte(k, '/'); i >= 0 {
		return k[:i], k[i+1:]
	}
	return "", k
}

// buildRedactedPath returns the §7.2 stable identifier for a K8s
// finding: "k8s://<cluster>/<ns>/<kind>/<name>[/<pod>]". The pod-name
// suffix is only appended when the candidate references a specific
// pod beneath a higher-level workload (cand.WorkloadKind != "Pod").
// All segments are redactField'ed at boundary; the store also applies
// shadow.RedactPath as defense in depth.
func (d *Detector) buildRedactedPath(cand signalCandidate) string {
	cluster := strings.TrimSpace(d.config.ClusterID)
	if cluster == "" {
		cluster = "unknown"
	}
	ns := cand.Namespace
	if ns == "" {
		ns = "_"
	}
	kind := cand.WorkloadKind
	if kind == "" {
		kind = "Unknown"
	}
	name := cand.WorkloadName
	if name == "" {
		name = "_"
	}
	path := "k8s://" + cluster + "/" + ns + "/" + kind + "/" + name
	if cand.SourcePod != nil && kind != "Pod" && cand.SourcePod.Name != "" {
		path += "/" + redactField(cand.SourcePod.Name)
	}
	return path
}

// emit resolves tenant + principal, builds a CreateFindingRequest, and
// persists the finding via shadow.Store. Observer is notified on
// success only — failed persistence does not count toward emit
// metrics. All emission is best-effort: store failures are swallowed
// because the observe-mode contract forbids causing detector-cycle
// failures from sink outages.
func (d *Detector) emit(ctx context.Context, cand signalCandidate, nsByName map[string]*corev1.Namespace, now time.Time) {
	ns := nsByName[cand.Namespace]
	sa := cand.SourceSA
	if sa == nil && cand.SourcePod != nil && cand.SourcePod.Spec.ServiceAccountName != "" {
		// §6.1 tier 4 falls back to the pod's service account
		// annotations; fetch the SA lazily so extractors don't need
		// per-pod RBAC bookkeeping. Failures here are non-fatal —
		// missing-SA simply lets the resolver fall through to the
		// quarantine tier.
		if fetched, err := d.reader.getServiceAccount(ctx, cand.SourcePod.Namespace, cand.SourcePod.Spec.ServiceAccountName); err == nil {
			sa = fetched
		}
	}
	tenantID, tenantSource := d.resolver.ResolveTenant(ctx, cand.SourcePod, ns, sa)
	principalID, principalSource := d.resolver.ResolvePrincipal(ctx, cand.SourcePod, ns, sa)

	req := shadow.CreateFindingRequest{
		TenantID:         tenantID,
		OwnerPrincipalID: principalID,
		PrincipalID:      principalID,
		AgentProduct:     redactField(cand.AgentProduct),
		Risk:             cand.Risk,
		EvidenceType:     cand.EvidenceType,
		EvidenceSummary:  redactField(cand.EvidenceSummary),
		// §7.2: hostname = cluster-id, NOT the host that ran the
		// detector. The store also TrimSpace'es; redactField provides
		// boundary scrubbing of operator-supplied cluster names.
		Hostname:     redactField(d.config.ClusterID),
		RedactedPath: d.buildRedactedPath(cand),
		DetectedAt:   now,

		SourceType:      shadow.SourceTypeKubernetes,
		SourceID:        redactField(d.sourceID),
		ClusterID:       redactField(d.config.ClusterID),
		Namespace:       redactField(cand.Namespace),
		WorkloadKind:    redactField(cand.WorkloadKind),
		WorkloadName:    redactField(cand.WorkloadName),
		PodUID:          redactField(cand.PodUID),
		TenantSource:    tenantSource,
		PrincipalSource: principalSource,
		SignalSet:       cand.SignalSet,
		Confidence:      confidenceFor(cand.Signal),
		FirstSeen:       ptrTime(now),
		LastSeen:        ptrTime(now),
		RetentionClass:  shadow.ShadowRetentionDefault,
	}
	finding, err := d.store.CreateFinding(ctx, req)
	if err != nil || finding == nil {
		d.emitFindingStoreFailure(now, tenantID, tenantSource, principalSource, cand, err)
		return
	}
	d.observer.RecordFindingEmit(cand.Signal, string(cand.Risk))
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: now,
		EventType: "edge.shadow_finding_created",
		Severity:  severityForRisk(cand.Risk),
		TenantID:  tenantID,
		Action:    "shadow_agent.observed",
		Decision:  "observed",
		Extra: map[string]string{
			"finding_id":    finding.FindingID,
			"source_type":   shadow.SourceTypeKubernetes,
			"signal":        cand.Signal,
			"cluster_id":    redactField(d.config.ClusterID),
			"workload":      cand.WorkloadKind + "/" + cand.WorkloadName,
			"tenant_src":    tenantSource,
			"principal_src": principalSource,
		},
	})
}

func (d *Detector) emitFindingStoreFailure(
	now time.Time,
	tenantID string,
	tenantSource string,
	principalSource string,
	cand signalCandidate,
	err error,
) {
	errText := "nil_finding"
	if err != nil {
		errText = redactField(err.Error())
	}
	d.observer.RecordFindingEmit(cand.Signal, string(cand.Risk))
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: now,
		EventType: "edge.shadow_finding_create_failed",
		Severity:  severityForRisk(cand.Risk),
		TenantID:  tenantID,
		Action:    "shadow_agent.observe_failed",
		Decision:  "error",
		Extra: map[string]string{
			"source_type":   shadow.SourceTypeKubernetes,
			"signal":        cand.Signal,
			"cluster_id":    redactField(d.config.ClusterID),
			"workload":      cand.WorkloadKind + "/" + cand.WorkloadName,
			"tenant_src":    tenantSource,
			"principal_src": principalSource,
			"error":         errText,
		},
	})
}

func ptrTime(t time.Time) *time.Time { return &t }

func confidenceFor(signal string) float64 {
	switch signal {
	case "heartbeat_missing":
		return 0.7
	case "unmanaged_process", "unmanaged_workload":
		return 0.85
	case "unmanaged_mcp_service":
		return 0.6
	case "untrusted_agent_image":
		return 0.5
	case "namespace_untenanted":
		return 0.6
	case "egress_bypass":
		return 0.9
	case "admission_observed":
		return 0.4
	case "ephemeral_indicator":
		return 0.3
	default:
		return 0.5
	}
}

func severityForRisk(r shadow.FindingRisk) string {
	switch r {
	case shadow.FindingRiskHigh, shadow.FindingRiskCritical:
		return "HIGH"
	case shadow.FindingRiskMedium:
		return "MEDIUM"
	default:
		return "INFO"
	}
}
