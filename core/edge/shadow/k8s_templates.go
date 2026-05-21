// EDGE-143.7 — Kubernetes-scope remediation templates extending the
// EDGE-142 generator per design doc §12.1.
//
// Each template emits operator-applicable text only — `kubectl`
// commands, YAML patches, or guidance strings the operator runs under
// their own cluster credentials. Q5 enforce-scope-out (governor ruling
// comment-a17f4f1c) is structural: template functions take only
// findingFeatures + audience, NEVER a kubernetes client / discovery
// interface / dynamic client / REST mapper. The Cordum side does not
// touch the cluster.
//
// Field plumbing reads from §10.1 fields on findingFeatures (clusterID,
// namespace, workloadKind, workloadName, podUID). Missing fields
// degrade to safe placeholders (`<cluster-id>`, `<namespace>`) rather
// than panic; operators still receive a runnable template they fill
// in by hand.
package shadow

import (
	"fmt"
	"strings"
)

// EDGE-143.7 — K8s signal constants. Bare signal names mirror what the
// EDGE-143.1 K8s detector emits in SignalSet (see core/edge/shadow/
// k8s/signals.go). Evidence types carry the `k8s_` prefix; signal-set
// entries do not, by convention.
const (
	signalK8sNamespaceUntenanted = "namespace_untenanted"
	signalK8sUnmanagedWorkload   = "unmanaged_workload"
	signalK8sUntrustedAgentImage = "untrusted_agent_image"
	signalK8sEgressBypass        = "egress_bypass"
)

// safeK8sField returns a finding field if non-empty, or a placeholder
// in the `<name>` shape so the resulting kubectl command stays
// runnable after operator substitution.
func safeK8sField(v, placeholder string) string {
	v = strings.TrimSpace(v)
	v = stripUnsafeRunes(v)
	if v == "" {
		return placeholder
	}
	return v
}

// safeK8sName trims a k8s name field; missing fields get the literal
// placeholder so kubectl-pasted commands fail loudly on a missing
// substitution rather than silently targeting the wrong workload.
func safeK8sName(v, placeholder string) string {
	return safeK8sField(v, placeholder)
}

// buildTenantLabelMissingSteps emits the §12.1 "tenant-label-missing"
// remediation — operator applies a kubectl label patch so the
// namespace (and downstream pods) carry the Cordum tenant id. NO
// Cordum-side cluster mutation; the patch is a string the operator
// runs locally.
func buildTenantLabelMissingSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep {
	ns := safeK8sField(f.namespace, "<namespace>")
	cluster := safeK8sField(f.clusterID, "<cluster-id>")
	return []RemediationStep{
		{
			ID:    "apply_tenant_label.namespace_patch",
			Title: "Label the namespace with the Cordum tenant id",
			Kind:  kind,
			Command: fmt.Sprintf(
				"kubectl --context %s label namespace %s cordum.io/tenant-id=<tenant-id> --overwrite",
				cluster, ns,
			),
			DocsURL: "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"requires kubectl access scoped to the namespace",
				"verify the tenant id is the value Cordum expects for this cluster",
			},
		},
		{
			ID:    "apply_tenant_label.verify_indicator_pods",
			Title: "Re-label or recreate indicator pods so the K8s detector observes the tenant binding",
			Kind:  kind,
			Command: fmt.Sprintf(
				"kubectl --context %s -n %s annotate pods --all cordum.io/tenant-source=namespace --overwrite",
				cluster, ns,
			),
			DocsURL: "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"only annotate pods you own; cross-tenant pods MUST be left untouched",
			},
		},
	}
}

// buildUnmanagedWorkloadSteps emits the §12.1 "unmanaged-workload"
// remediation. Two operator-chosen options: allowlist the workload OR
// inject the cordum-agentd sidecar via a Deployment patch.
func buildUnmanagedWorkloadSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep {
	ns := safeK8sField(f.namespace, "<namespace>")
	cluster := safeK8sField(f.clusterID, "<cluster-id>")
	workloadKind := safeK8sField(f.workloadKind, "<workload-kind>")
	workloadName := safeK8sName(f.workloadName, "<workload-name>")
	return []RemediationStep{
		{
			ID:    "adopt_unmanaged_workload.allowlist",
			Title: "Option A — add the workload to the operator's WorkloadAllowlist",
			Kind:  kind,
			Command: fmt.Sprintf(
				"# In cordum operator config (WorkloadAllowlist), append:\n- %s",
				workloadName,
			),
			DocsURL: "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"only acceptable when the workload is operator-vetted (not customer-tenant-owned)",
				"reload the K8s detector ConfigMap so the allowlist takes effect on the next poll",
			},
		},
		{
			ID:    "adopt_unmanaged_workload.sidecar_patch",
			Title: "Option B — inject the cordum-agentd sidecar so the workload becomes Cordum-managed",
			Kind:  kind,
			Command: fmt.Sprintf(
				"kubectl --context %s -n %s patch %s/%s --type=strategic --patch \"$(cat <<'PATCH'\nspec:\n  template:\n    metadata:\n      labels:\n        cordum.io/heartbeat: \"true\"\n    spec:\n      containers:\n        - name: cordum-agentd\n          image: <cordum-allowlisted-registry>/cordum-agentd:<version>\n          args: [\"--mode=sidecar\", \"--gateway=<gateway-url>\", \"--tenant-id=<tenant-id>\"]\nPATCH\n)\"",
				cluster, ns, strings.ToLower(workloadKind), workloadName,
			),
			RequiresBackup: true,
			DocsURL:        "docs/edge/managed-settings-deploy.md",
			Conditions: []string{
				"backup the workload manifest before applying (kubectl get -o yaml > backup.yaml)",
				"sidecar image MUST be on the operator's ImageRegistryAllowlist",
				"this is a rolling restart — schedule in a maintenance window for stateful workloads",
			},
		},
	}
}

// buildRebaseAgentImageSteps emits the §12.1 "untrusted-image"
// remediation: switch the workload to a manifest pointing at an
// allowlisted registry. Operator applies; Cordum NEVER mutates image
// pull references.
func buildRebaseAgentImageSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep {
	ns := safeK8sField(f.namespace, "<namespace>")
	cluster := safeK8sField(f.clusterID, "<cluster-id>")
	workloadKind := safeK8sField(f.workloadKind, "<workload-kind>")
	workloadName := safeK8sName(f.workloadName, "<workload-name>")
	currentImage := extractImageFromFeatures(f)
	return []RemediationStep{
		{
			ID:    "rebase_agent_image.identify_current",
			Title: "Identify the current (untrusted) image reference",
			Kind:  kind,
			Command: fmt.Sprintf(
				"# Current image: %s\nkubectl --context %s -n %s get %s/%s -o jsonpath='{.spec.template.spec.containers[*].image}'",
				currentImage, cluster, ns, strings.ToLower(workloadKind), workloadName,
			),
			DocsURL: "docs/edge/shadow-remediation.md",
		},
		{
			ID:    "rebase_agent_image.patch_to_allowlisted",
			Title: "Rebase the workload onto an allowlisted registry",
			Kind:  kind,
			Command: fmt.Sprintf(
				"kubectl --context %s -n %s set image %s/%s <container-name>=<cordum-allowlisted-registry>/<image-name>:<tag>",
				cluster, ns, strings.ToLower(workloadKind), workloadName,
			),
			RequiresBackup: true,
			DocsURL:        "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"verify the destination digest matches the upstream image before applying",
				"replace <container-name> with the matching container from the previous step",
				"rolling restart applies — schedule for low-traffic windows on user-facing workloads",
			},
		},
	}
}

// buildExtendEgressPolicySteps emits the §12.1 "egress-bypass"
// remediation: extend the offending NetworkPolicy egress allowlist to
// add the operator's LLM-proxy CIDR / FQDN. Operator applies via
// kubectl; Cordum NEVER mutates NetworkPolicy resources.
func buildExtendEgressPolicySteps(kind RemediationActionKind, f findingFeatures) []RemediationStep {
	ns := safeK8sField(f.namespace, "<namespace>")
	cluster := safeK8sField(f.clusterID, "<cluster-id>")
	policy := safeK8sName(f.workloadName, "<network-policy-name>")
	patchYAML := strings.Join([]string{
		"apiVersion: networking.k8s.io/v1",
		"kind: NetworkPolicy",
		fmt.Sprintf("metadata:\n  name: %s\n  namespace: %s", policy, ns),
		"spec:",
		"  podSelector: {}  # keep the existing podSelector",
		"  policyTypes: [Egress]",
		"  egress:",
		"    - to:",
		"        - ipBlock:",
		"            cidr: <llm-proxy-cidr>           # e.g. 10.42.0.0/16",
		"        - namespaceSelector:",
		"            matchLabels:",
		"              cordum.io/llm-proxy: \"true\"",
		"      ports:",
		"        - protocol: TCP",
		"          port: 443",
	}, "\n")
	return []RemediationStep{
		{
			ID:    "extend_egress_policy.preview_diff",
			Title: "Preview the proposed NetworkPolicy patch",
			Kind:  kind,
			Command: fmt.Sprintf(
				"kubectl --context %s -n %s diff -f - <<'YAML'\n%s\nYAML",
				cluster, ns, patchYAML,
			),
			PreviewOnly: true,
			DocsURL:     "docs/edge/shadow-remediation.md",
		},
		{
			ID:    "extend_egress_policy.apply",
			Title: "Apply the egress allowlist extension once the diff is acceptable",
			Kind:  kind,
			Command: fmt.Sprintf(
				"kubectl --context %s -n %s apply -f - <<'YAML'\n%s\nYAML",
				cluster, ns, patchYAML,
			),
			RequiresBackup: true,
			DocsURL:        "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"backup the existing NetworkPolicy: kubectl get networkpolicy <name> -o yaml > backup.yaml",
				"verify <llm-proxy-cidr> matches the operator's LLM proxy egress CIDR",
				"this strictly extends egress (additive); existing rules MUST remain in place",
			},
		},
	}
}

// extractImageFromFeatures pulls the container image string out of
// finding evidence. The K8s detector emits `image=<image-ref>` in
// EvidenceSummary; the Metadata map may also carry a `container_image`
// key. Returns a placeholder when neither is available so the rebase
// template stays runnable.
func extractImageFromFeatures(f findingFeatures) string {
	if img, ok := f.metadata["container_image"]; ok {
		img = strings.TrimSpace(stripUnsafeRunes(img))
		if img != "" {
			return img
		}
	}
	const marker = "image="
	if idx := strings.Index(f.evidenceSummary, marker); idx >= 0 {
		rest := f.evidenceSummary[idx+len(marker):]
		end := len(rest)
		for i, r := range rest {
			if r == ' ' || r == ';' || r == ',' || r == '\n' {
				end = i
				break
			}
		}
		img := strings.TrimSpace(stripUnsafeRunes(rest[:end]))
		if img != "" {
			return img
		}
	}
	return "<container-image>"
}
