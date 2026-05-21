package k8s

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// Tenant + principal source labels per design doc §6.1 / §6.2. These
// strings appear on every emitted finding's TenantSource /
// PrincipalSource fields so operators can audit attribution drift.
const (
	TenantSourcePodLabel       = "pod_label"
	TenantSourceNamespaceLabel = "namespace_label"
	TenantSourceClusterConfig  = "cluster_config"
	TenantSourceSAConfig       = "sa_config"
	TenantSourceQuarantine     = "quarantine"

	PrincipalSourcePodLabel      = "pod_label"
	PrincipalSourcePodAnnotation = "pod_annotation"
	PrincipalSourceServiceAcct   = "sa_name"
	PrincipalSourceUnknown       = "unknown"
)

// TenantResolver maps a pod (and its enclosing namespace) to a tenant
// identifier + the source label that describes which precedence tier
// resolved the mapping. ResolvePrincipal does the equivalent for the
// principal identifier. Both methods MUST be deterministic for a given
// pod input; implementations are otherwise free to consult external
// configuration, service-account metadata, or operator-maintained
// allowlists.
type TenantResolver interface {
	ResolveTenant(ctx context.Context, pod *corev1.Pod, ns *corev1.Namespace, sa *corev1.ServiceAccount) (tenantID, source string)
	ResolvePrincipal(ctx context.Context, pod *corev1.Pod, ns *corev1.Namespace, sa *corev1.ServiceAccount) (principalID, source string)
}

// defaultTenantResolver implements the design-doc §6.1 precedence
// chain: pod label → namespace label → cluster_tenant_map[cluster_id]
// → service-account annotation → quarantine tenant. The §6.2
// principal chain is analogous.
type defaultTenantResolver struct {
	tenantLabelKey     string
	principalLabelKey  string
	clusterID          string
	clusterTenantMap   map[string]string
	quarantineTenantID string
}

func newDefaultResolver(cfg Config) TenantResolver {
	return &defaultTenantResolver{
		tenantLabelKey:     cfg.TenantLabelKey,
		principalLabelKey:  cfg.PrincipalLabelKey,
		clusterID:          cfg.ClusterID,
		clusterTenantMap:   cfg.ClusterTenantMap,
		quarantineTenantID: cfg.QuarantineTenantID,
	}
}

func (r *defaultTenantResolver) ResolveTenant(_ context.Context, pod *corev1.Pod, ns *corev1.Namespace, sa *corev1.ServiceAccount) (string, string) {
	if pod != nil {
		if v := pod.Labels[r.tenantLabelKey]; v != "" {
			return v, TenantSourcePodLabel
		}
	}
	if ns != nil {
		if v := ns.Labels[r.tenantLabelKey]; v != "" {
			return v, TenantSourceNamespaceLabel
		}
	}
	if r.clusterID != "" {
		if v := r.clusterTenantMap[r.clusterID]; v != "" {
			return v, TenantSourceClusterConfig
		}
	}
	if sa != nil {
		if v := sa.Annotations[r.tenantLabelKey]; v != "" {
			return v, TenantSourceSAConfig
		}
	}
	return r.quarantineTenantID, TenantSourceQuarantine
}

func (r *defaultTenantResolver) ResolvePrincipal(_ context.Context, pod *corev1.Pod, _ *corev1.Namespace, sa *corev1.ServiceAccount) (string, string) {
	if pod != nil {
		if v := pod.Labels[r.principalLabelKey]; v != "" {
			return v, PrincipalSourcePodLabel
		}
		if v := pod.Annotations[r.principalLabelKey]; v != "" {
			return v, PrincipalSourcePodAnnotation
		}
		if pod.Spec.ServiceAccountName != "" {
			return "sa:" + pod.Namespace + ":" + pod.Spec.ServiceAccountName, PrincipalSourceServiceAcct
		}
		if sa != nil {
			return "sa:" + sa.Namespace + ":" + sa.Name, PrincipalSourceServiceAcct
		}
	}
	return PrincipalSourceUnknown, PrincipalSourceUnknown
}
