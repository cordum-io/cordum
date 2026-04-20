package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRunEvalsExtractBuildsRequestAndPrintsSummary(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotQuery string
	var gotTenant string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotTenant = r.Header.Get("X-Tenant-ID")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dataset_id":"dataset-123","name":"incident-regressions","version":2,"entry_count":9,"deduped_count":4,"scanned_decisions":17,"warnings":["job job-9 missing from job store; skipped incident"]}`))
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runEvalsExtract([]string{
			"--gateway", srv.URL,
			"--tenant", "tenant-a",
			"--since", "2026-04-13",
			"--until", "2026-04-20",
			"--topic", "payments-*",
			"--rule", "rule-pii-leak-01",
			"--verdicts", "deny,require_approval,deny",
			"--agent-id", "agent-a",
			"--max-entries", "250",
			"--name", "incident-regressions",
			"--description", "Seeded from incidents",
		}); err != nil {
			t.Fatalf("runEvalsExtract returned error: %v", err)
		}
	})

	if gotMethod != http.MethodPost || gotPath != "/api/v1/evals/datasets/from-incidents" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if gotQuery != "" {
		t.Fatalf("expected no query string, got %q", gotQuery)
	}
	if gotTenant != "tenant-a" {
		t.Fatalf("expected tenant header tenant-a, got %q", gotTenant)
	}
	if gotBody["name"] != "incident-regressions" {
		t.Fatalf("unexpected body name: %+v", gotBody)
	}
	if gotBody["topic"] != "payments-*" || gotBody["rule_id"] != "rule-pii-leak-01" || gotBody["agent_id"] != "agent-a" {
		t.Fatalf("unexpected filters: %+v", gotBody)
	}
	if gotBody["description"] != "Seeded from incidents" {
		t.Fatalf("unexpected description: %+v", gotBody)
	}
	if gotBody["max_entries"] != float64(250) {
		t.Fatalf("unexpected max_entries: %+v", gotBody["max_entries"])
	}
	if gotBody["since"] != "2026-04-13T00:00:00Z" {
		t.Fatalf("unexpected since: %v", gotBody["since"])
	}
	if gotBody["until"] != "2026-04-20T23:59:59.999Z" {
		t.Fatalf("unexpected until: %v", gotBody["until"])
	}
	verdicts, ok := gotBody["verdicts"].([]any)
	if !ok {
		t.Fatalf("expected verdict array, got %#v", gotBody["verdicts"])
	}
	if len(verdicts) != 2 || verdicts[0] != "deny" || verdicts[1] != "require_approval" {
		t.Fatalf("unexpected verdicts: %#v", verdicts)
	}

	for _, want := range []string{
		"mode: created",
		"name: incident-regressions",
		"entry_count: 9",
		"deduped_count: 4",
		"scanned_decisions: 17",
		"version: 2",
		"dataset_id: dataset-123",
		"warnings:",
		"job job-9 missing from job store; skipped incident",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected output to contain %q, got %q", want, stdout)
		}
	}
}

func TestRunEvalsExtractDryRunUsesQueryOverride(t *testing.T) {
	var gotQuery string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"preview-dataset","entry_count":3,"deduped_count":1,"scanned_decisions":5}`))
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runEvalsExtract([]string{
			"--gateway", srv.URL,
			"--name", "preview-dataset",
			"--dry-run",
		}); err != nil {
			t.Fatalf("runEvalsExtract returned error: %v", err)
		}
	})

	if gotQuery != "dry_run=true" {
		t.Fatalf("expected dry_run query override, got %q", gotQuery)
	}
	if gotBody["dry_run"] != true {
		t.Fatalf("expected dry_run in body, got %+v", gotBody)
	}
	if strings.Contains(stdout, "dataset_id:") {
		t.Fatalf("dry-run output should not include dataset_id, got %q", stdout)
	}
	if !strings.Contains(stdout, "mode: preview") {
		t.Fatalf("expected preview mode in output, got %q", stdout)
	}
}

func TestRunEvalsExtractReturnsErrorOnGatewayFailure(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}))
			defer srv.Close()

			err := runEvalsExtract([]string{
				"--gateway", srv.URL,
				"--name", "failure-dataset",
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "unexpected status") || !strings.Contains(err.Error(), strconv.Itoa(status)) {
				t.Fatalf("expected status error, got %v", err)
			}
		})
	}
}

func TestRunEvalsExtractRejectsInvalidVerdict(t *testing.T) {
	err := runEvalsExtract([]string{
		"--name", "bad-verdicts",
		"--verdicts", "deny,nope",
	})
	if err == nil {
		t.Fatal("expected invalid verdict error")
	}
	if !strings.Contains(err.Error(), "unknown decision verdict") {
		t.Fatalf("unexpected error: %v", err)
	}
}
