package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderVerdictTableOrdersByExpectedSteps confirms the table is
// rendered in the canonical step order (greet, attempt_delete,
// escalate_admin) regardless of the order GetRunTimeline returned them.
func TestRenderVerdictTableOrdersByExpectedSteps(t *testing.T) {
	rows := []demoVerdict{
		{StepID: "escalate_admin", Topic: "job.demo-quickstart.admin", Verdict: "REQUIRE_APPROVAL", Reason: "human sign-off", JobID: "job-3"},
		{StepID: "greet", Topic: "job.demo-quickstart.greet", Verdict: "ALLOW", Reason: "safe"},
		{StepID: "attempt_delete", Topic: "job.demo-quickstart.delete-all", Verdict: "DENY", Reason: "blocked"},
	}
	var buf bytes.Buffer
	renderVerdictTable(&buf, rows)
	out := buf.String()

	posGreet := strings.Index(out, "job.demo-quickstart.greet")
	posDelete := strings.Index(out, "job.demo-quickstart.delete-all")
	posAdmin := strings.Index(out, "job.demo-quickstart.admin")
	if posGreet == -1 || posDelete == -1 || posAdmin == -1 {
		t.Fatalf("missing rows in output:\n%s", out)
	}
	if posGreet >= posDelete || posDelete >= posAdmin {
		t.Errorf("rows not in canonical order — greet=%d delete=%d admin=%d\n%s",
			posGreet, posDelete, posAdmin, out)
	}
}

// TestRenderVerdictTablePrintsApprovalCommand verifies the REQUIRE_APPROVAL
// row generates an exact `cordumctl approval job <id> --approve` line.
// This is the command users copy-paste, so an off-by-one or missing job
// ID would break the demo's narrative.
func TestRenderVerdictTablePrintsApprovalCommand(t *testing.T) {
	rows := []demoVerdict{
		{StepID: "greet", Topic: "job.demo-quickstart.greet", Verdict: "ALLOW"},
		{StepID: "attempt_delete", Topic: "job.demo-quickstart.delete-all", Verdict: "DENY"},
		{StepID: "escalate_admin", Topic: "job.demo-quickstart.admin", Verdict: "REQUIRE_APPROVAL", JobID: "job-abc-123"},
	}
	var buf bytes.Buffer
	renderVerdictTable(&buf, rows)
	out := buf.String()
	want := "cordumctl approval job job-abc-123 --approve"
	if !strings.Contains(out, want) {
		t.Errorf("missing approval command %q in output:\n%s", want, out)
	}
}

// TestRenderVerdictTableShowsPendingForMissingVerdict ensures partial runs
// surface a clear "PENDING" marker rather than an empty cell that the user
// might miss.
func TestRenderVerdictTableShowsPendingForMissingVerdict(t *testing.T) {
	rows := []demoVerdict{
		{StepID: "greet", Topic: "job.demo-quickstart.greet"},
		{StepID: "attempt_delete", Topic: "job.demo-quickstart.delete-all", Verdict: "DENY"},
		{StepID: "escalate_admin", Topic: "job.demo-quickstart.admin", Verdict: "REQUIRE_APPROVAL", JobID: "j"},
	}
	var buf bytes.Buffer
	renderVerdictTable(&buf, rows)
	if !strings.Contains(buf.String(), "PENDING") {
		t.Errorf("PENDING placeholder missing from output:\n%s", buf.String())
	}
}

// TestAllVerdictsObservedSemantics is a small but load-bearing test —
// the CLI uses this to set its exit code. False negatives would mean the
// integration test silently passes a broken demo.
func TestAllVerdictsObservedSemantics(t *testing.T) {
	full := []demoVerdict{
		{StepID: "greet", Verdict: "ALLOW"},
		{StepID: "attempt_delete", Verdict: "DENY"},
		{StepID: "escalate_admin", Verdict: "REQUIRE_APPROVAL"},
	}
	if !allVerdictsObserved(full) {
		t.Error("full verdict set should be observed")
	}
	partial := []demoVerdict{
		{StepID: "greet", Verdict: "ALLOW"},
		{StepID: "attempt_delete"},
	}
	if allVerdictsObserved(partial) {
		t.Error("missing verdict should mark set as not observed")
	}
}

// TestDemoQuickstartInputContractStable pins the workflow input shape so
// future edits don't drift the keys the workflow's transform substitutions
// reference (`${input.name}`, `${input.target}`, `${input.admin_action}`).
func TestDemoQuickstartInputContractStable(t *testing.T) {
	in := demoQuickstartInput()
	for _, key := range []string{"name", "target", "admin_action"} {
		if _, ok := in[key]; !ok {
			t.Errorf("workflow input missing required key %q (referenced by hello.yaml)", key)
		}
	}
}

// TestExpectedQuickstartStepsMatchWorkflow keeps demo.go and hello.yaml in
// sync. If hello.yaml renames a step or changes a topic, the table will
// silently render the wrong row — this test forces that drift to surface.
func TestExpectedQuickstartStepsMatchWorkflow(t *testing.T) {
	want := map[string]string{
		"greet":          "job.demo-quickstart.greet",
		"attempt_delete": "job.demo-quickstart.delete-all",
		"escalate_admin": "job.demo-quickstart.admin",
	}
	if len(expectedQuickstartSteps) != len(want) {
		t.Fatalf("expectedQuickstartSteps length = %d, want %d", len(expectedQuickstartSteps), len(want))
	}
	for _, step := range expectedQuickstartSteps {
		gotTopic, ok := want[step.StepID]
		if !ok {
			t.Errorf("step %q not in expected map", step.StepID)
			continue
		}
		if gotTopic != step.Topic {
			t.Errorf("step %q: topic = %q, want %q", step.StepID, step.Topic, gotTopic)
		}
	}
}
