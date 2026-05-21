package k8s

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyEphemeralCorroboration_RejectsEmptyNamespace(t *testing.T) {
	in := []signalCandidate{
		{Signal: "namespace_untenanted", Namespace: "", WorkloadName: "cluster-corroborator"},
		{Signal: "ephemeral_indicator", Namespace: "", WorkloadName: "cluster-ephemeral"},
		{Signal: "unmanaged_process", Namespace: "foo", WorkloadName: "foo-corroborator"},
		{Signal: "ephemeral_indicator", Namespace: "foo", WorkloadName: "foo-ephemeral"},
		{Signal: "ephemeral_indicator", Namespace: "bar", WorkloadName: "bar-ephemeral"},
	}

	got := applyEphemeralCorroboration(in)

	if hasSignalCandidate(got, "ephemeral_indicator", "", "cluster-ephemeral") {
		t.Fatalf("empty-namespace ephemeral was corroborated by empty-namespace non-ephemeral: %#v", got)
	}
	if !hasSignalCandidate(got, "ephemeral_indicator", "foo", "foo-ephemeral") {
		t.Fatalf("matching namespace ephemeral was not corroborated: %#v", got)
	}
	if hasSignalCandidate(got, "ephemeral_indicator", "bar", "bar-ephemeral") {
		t.Fatalf("unmatched namespace ephemeral was corroborated: %#v", got)
	}
	if !hasSignalCandidate(got, "namespace_untenanted", "", "cluster-corroborator") {
		t.Fatalf("non-ephemeral cluster-scoped signal was dropped: %#v", got)
	}
}

func TestHeartbeatMissCount_BoundedAcrossCycles(t *testing.T) {
	cfg := Config{
		KnownAgentImages:         []string{"anthropic/claude-code"},
		HeartbeatMissedThreshold: 2,
	}
	cfg.fillDefaults()
	d := &Detector{
		config: cfg,
		state: &scanState{
			heartbeatMissCount: map[string]int{},
		},
	}

	for i := 0; i < 100; i++ {
		pod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "agents",
				Name:      fmt.Sprintf("agent-%03d", i),
				Labels:    map[string]string{},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "main",
				Image: "anthropic/claude-code:v1",
			}}},
		}
		_ = d.heartbeatMissingSignal([]corev1.Pod{pod})
		if got := len(d.state.heartbeatMissCount); got > 1 {
			t.Fatalf("heartbeatMissCount size after cycle %d = %d, want bounded to current pod set", i, got)
		}
	}
}

func hasSignalCandidate(candidates []signalCandidate, signal, namespace, workload string) bool {
	for _, candidate := range candidates {
		if candidate.Signal == signal &&
			candidate.Namespace == namespace &&
			candidate.WorkloadName == workload {
			return true
		}
	}
	return false
}
