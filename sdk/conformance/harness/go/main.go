// Command conformance-harness-go walks the shared fixture tree, runs
// each fixture against a subprocess-spawned gateway simulator, and
// emits a JUnit XML report at --report for CI ingestion.
//
// Usage:
//
//	conformance-harness-go \
//	  --fixtures ../../fixtures \
//	  --sim-bin ../../simulator/bin/cordum-gateway-sim \
//	  --report ../../reports/go.xml
//
// Flags default to the conformance repo layout so local runs are
// one-liner-able: `go run ./...` from harness/go/ finds fixtures and
// the sim binary automatically.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultAPIKey = "conformance-api-key" // #nosec G101 -- deterministic simulator-only test credential.
const defaultTenant = "default"

func main() {
	fixturesDir := flag.String("fixtures", "../../fixtures", "root dir of fixture .json files")
	simBin := flag.String("sim-bin", "../../simulator/bin/cordum-gateway-sim", "path to the built simulator binary")
	reportPath := flag.String("report", "../../reports/go.xml", "where to write the JUnit XML report")
	flag.Parse()

	// Spawn the simulator and read its URL from stdout.
	sim, url, err := startSimulator(*simBin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-go: failed to start simulator: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = sim.Cancel() }()

	// Wait for /healthz to come up so no fixture races the sim.
	if err := waitForReady(url, 5*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "harness-go: simulator never became ready: %v\n", err)
		os.Exit(2)
	}

	driver := NewDriver(url, defaultAPIKey, defaultTenant)

	fixtures, err := loadFixtures(*fixturesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness-go: load fixtures: %v\n", err)
		os.Exit(2)
	}

	suite := newReport("conformance-go")
	pass, fail := 0, 0
	for _, fx := range fixtures {
		// Reset the var bag per fixture so extracts don't leak across.
		driver.Vars = map[string]any{"apiKey": defaultAPIKey, "tenant": defaultTenant}
		start := time.Now()
		err := driver.RunFixture(fx)
		elapsed := time.Since(start)
		tc := TestCase{
			Name:      fx.Name,
			ClassName: fx.Name,
			TimeSec:   elapsed.Seconds(),
		}
		if err != nil {
			tc.Failure = &Failure{Message: err.Error(), Type: "AssertionError"}
			fail++
			fmt.Fprintf(os.Stderr, "FAIL %-50s %s\n", fx.Name, err)
		} else {
			pass++
			fmt.Fprintf(os.Stderr, "PASS %-50s (%.3fs)\n", fx.Name, elapsed.Seconds())
		}
		suite.TestCases = append(suite.TestCases, tc)
	}
	suite.Tests = len(suite.TestCases)
	suite.Failures = fail

	if err := writeReport(*reportPath, suite); err != nil {
		fmt.Fprintf(os.Stderr, "harness-go: write report: %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "\nharness-go: %d pass, %d fail — report=%s\n", pass, fail, *reportPath)
	if fail > 0 {
		os.Exit(1)
	}
}

type simProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (s *simProc) Cancel() error {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Kill()
	<-s.done
	return nil
}

// startSimulator launches the sim binary and returns its bound URL
// once the binary prints it to stdout.
func startSimulator(bin string) (*simProc, string, error) {
	if _, err := os.Stat(bin); err != nil {
		return nil, "", fmt.Errorf("simulator binary not found at %s: %w", bin, err)
	}
	cmd := exec.Command(bin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	reader := bufio.NewReader(stdout)
	urlLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, "", fmt.Errorf("read sim url: %w", err)
	}
	url := strings.TrimSpace(urlLine)
	// Drain remaining stdout in background so the child doesn't block.
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			// discard
		}
	}()
	return &simProc{cmd: cmd, done: done}, url, nil
}

func waitForReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				lastErr = closeErr
			}
			if resp.StatusCode == 200 {
				return nil
			}
		}
		if err != nil {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return lastErr
}

// loadFixtures walks dir and returns every *.json that decodes into a
// Fixture. Files are sorted by path for reproducibility.
func loadFixtures(dir string) ([]*Fixture, error) {
	var paths []string
	if err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var out []*Fixture
	for _, p := range paths {
		data, err := os.ReadFile(p) // #nosec G304 -- conformance dev/test harness reading fixture files from an operator-supplied fixtures dir
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		var fx Fixture
		if err := json.Unmarshal(data, &fx); err != nil {
			return nil, fmt.Errorf("decode %s: %w", p, err)
		}
		mu.Lock()
		out = append(out, &fx)
		mu.Unlock()
	}
	return out, nil
}

// --- JUnit XML report types ---
//
// Minimal subset of the JUnit schema CI ingestion tools expect. One
// testsuite, many testcase children, failure leaf for failed cases.

type TestSuite struct {
	XMLName   xml.Name   `xml:"testsuite"`
	Name      string     `xml:"name,attr"`
	Tests     int        `xml:"tests,attr"`
	Failures  int        `xml:"failures,attr"`
	TestCases []TestCase `xml:"testcase"`
}

type TestCase struct {
	Name      string   `xml:"name,attr"`
	ClassName string   `xml:"classname,attr"`
	TimeSec   float64  `xml:"time,attr"`
	Failure   *Failure `xml:"failure,omitempty"`
}

type Failure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

func newReport(name string) *TestSuite { return &TestSuite{Name: name} }

func writeReport(path string, suite *TestSuite) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.Create(path) // #nosec G304 -- conformance dev/test harness writing a JUnit report to an operator-supplied output path
	if err != nil {
		return err
	}
	defer f.Close()
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	if _, err := f.WriteString(xml.Header); err != nil {
		return err
	}
	return enc.Encode(suite)
}

// use context to keep the import used even when testing the harness
// directly without a subprocess.
var _ = context.Background
