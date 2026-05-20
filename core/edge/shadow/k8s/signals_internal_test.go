package k8s

import "testing"

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
