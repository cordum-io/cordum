package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	infraschema "github.com/cordum/cordum/core/infra/schema"
)

// BenchmarkMeasure is one row of the benchmark specification's measures table.
type BenchmarkMeasure struct {
	ID, Dimension, Measure, Definition, Unit, Method string
}

// BenchmarkMeasures reads the fixed six-column table in benchmark-spec.md.
func BenchmarkMeasures(specPath string) ([]BenchmarkMeasure, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read benchmark specification: %w", err)
	}
	const header = "| ID | Dimension | Measure | Definition | Unit | Method |\n| --- | --- | --- | --- | --- | --- |\n"
	_, table, ok := strings.Cut(strings.ReplaceAll(string(data), "\r\n", "\n"), header)
	if !ok {
		return nil, fmt.Errorf("benchmark measures table not found")
	}
	var rows []BenchmarkMeasure
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		cells := strings.Split(line, "|")
		if len(cells) != 8 || cells[0] != "" || cells[7] != "" {
			return nil, fmt.Errorf("benchmark measures row must have six columns: %q", line)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, BenchmarkMeasure{ID: cells[1], Dimension: cells[2], Measure: cells[3],
			Definition: cells[4], Unit: cells[5], Method: cells[6]})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("benchmark measures table is empty")
	}
	return rows, nil
}

// ValidateBenchmarkResult checks JSON with the repository's result schema.
func ValidateBenchmarkResult(root string, data []byte) error {
	schema, err := os.ReadFile(filepath.Join(root, "docs/contract/benchmark-result.schema.json"))
	if err != nil {
		return fmt.Errorf("read benchmark result schema: %w", err)
	}
	if err := infraschema.ValidateSchema("benchmark-result.schema.json", schema, data); err != nil {
		return fmt.Errorf("benchmark result: %w", err)
	}
	return nil
}
