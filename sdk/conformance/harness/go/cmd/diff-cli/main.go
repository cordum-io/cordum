// diff-cli invokes the Go harness's diff engine against a parity
// scenarios file and emits a per-scenario verdict JSON. Used by
// parity/run.sh to cross-check verdicts against the Python + TS
// diff implementations.
//
// Usage:
//
//	diff-cli < parity/scenarios.json > verdicts-go.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Scenario is the shared parity case shape.
type Scenario struct {
	Name     string `json:"name"`
	Actual   any    `json:"actual"`
	Expected any    `json:"expected"`
	WantPass bool   `json:"want_pass"`
	Reason   string `json:"reason"`
}

// Verdict is the per-case output the cross-harness runner compares.
type Verdict struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Error    string `json:"error,omitempty"`
	Expected bool   `json:"want_pass"`
	Agreed   bool   `json:"agreed_with_want"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "diff-cli: %v\n", err)
		os.Exit(2)
	}
}

func run() error {
	var scenarios []Scenario
	if err := json.NewDecoder(os.Stdin).Decode(&scenarios); err != nil {
		return fmt.Errorf("decode stdin: %w", err)
	}
	verdicts := make([]Verdict, 0, len(scenarios))
	for _, s := range scenarios {
		v := Verdict{Name: s.Name, Expected: s.WantPass}
		err := diffForParity(s.Actual, s.Expected)
		if err == nil {
			v.Passed = true
		} else {
			v.Passed = false
			v.Error = err.Error()
		}
		v.Agreed = v.Passed == s.WantPass
		verdicts = append(verdicts, v)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(verdicts)
}
