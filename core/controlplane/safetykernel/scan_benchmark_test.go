package safetykernel

import (
	"fmt"
	"strings"
	"testing"
)

// scanBenchInput builds a 2 MiB input with a credential on every secretEvery-th
// line and filler text on the rest.
func scanBenchInput(secretEvery int) []byte {
	const size = 2 << 20
	var b strings.Builder
	b.Grow(size + 64)
	for i := 0; b.Len() < size; i++ {
		if i%secretEvery == 0 {
			fmt.Fprintf(&b, "password=%q\n", fmt.Sprintf("synthetic-bench-%06d", i))
			continue
		}
		b.WriteString("lorem ipsum dolor sit amet, consectetur\n")
	}
	return []byte(b.String()[:size])
}

// BenchmarkScan2MiB uses only APIs that exist before the saturation change, so
// the same file measures both sides.
func BenchmarkScan2MiB(b *testing.B) {
	scanners := defaultOutputScanners()
	opts := ContentScanOptions{IncludeSecrets: true, IncludePII: true}
	for _, in := range []struct {
		name  string
		every int
	}{{"sparse", 8192}, {"dense", 1}} {
		content := scanBenchInput(in.every)
		b.Run("redact_content/"+in.name, func(b *testing.B) {
			b.SetBytes(int64(len(content)))
			for b.Loop() {
				RedactContent(content, opts)
			}
		})
		b.Run("output_scanners/"+in.name, func(b *testing.B) {
			b.SetBytes(int64(len(content)))
			for b.Loop() {
				scanWithScanners(content, []string{"secret", "pii"}, scanners)
			}
		})
	}
}
