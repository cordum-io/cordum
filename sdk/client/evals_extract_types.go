package client

// ExtractIncidentsRequest is the body for POST
// /api/v1/evals/datasets/from-incidents. Matches the wire contract
// documented under `docs/evals/extraction.md`. Field shapes were
// lifted from cmd/cordumctl/evals.go (the original definitions were
// dropped by an unresolved merge; this file restores them for build
// compatibility).
type ExtractIncidentsRequest struct {
	Since       string   `json:"since,omitempty"`
	Until       string   `json:"until,omitempty"`
	Topic       string   `json:"topic,omitempty"`
	RuleID      string   `json:"rule_id,omitempty"`
	Verdicts    []string `json:"verdicts,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
	MaxEntries  int      `json:"max_entries,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

// ExtractIncidentsResponse is the server response to
// POST /api/v1/evals/datasets/from-incidents. DryRun responses omit
// DatasetID/Version but always return the scan/entry/dedupe counts
// and any warnings.
type ExtractIncidentsResponse struct {
	Name             string   `json:"name"`
	EntryCount       int      `json:"entry_count"`
	DedupedCount     int      `json:"deduped_count"`
	ScannedDecisions int      `json:"scanned_decisions"`
	Version          int      `json:"version,omitempty"`
	DatasetID        string   `json:"dataset_id,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}
