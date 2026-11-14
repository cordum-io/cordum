package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// recorder implements tb and writes what run reports into a shared log, so
// the self-tests can watch run fail without failing themselves.
type recorder struct {
	t    *testing.T
	path string
	log  *[]string
}

func (r recorder) add(kind, msg string) { *r.log = append(*r.log, kind+" "+r.path+": "+msg) }

func (r recorder) Errorf(format string, args ...any) { r.add("error", fmt.Sprintf(format, args...)) }

func (r recorder) Skipf(format string, args ...any) { r.add("skip", fmt.Sprintf(format, args...)) }

func (r recorder) Logf(string, ...any) {}

func (r recorder) Run(name string, f func(tb)) bool {
	path := name
	if r.path != "" {
		path = r.path + "/" + name
	}
	*r.log = append(*r.log, "run "+path)
	f(recorder{t: r.t, path: path, log: r.log})
	return true
}

func (r recorder) testingT() *testing.T { return r.t }

// record drives run, which is Run's whole body, through a recorder.
func record(t *testing.T, m matrix, e Entry, present Presenter, opts ...Option) []string {
	var log []string
	run(recorder{t: t, log: &log}, m, e, present, opts...)
	return log
}

const (
	ca Credential = "a"
	cb Credential = "b"
	cc Credential = "c"
	e1 Entry      = "e1"
	e2 Entry      = "e2"
)

func fixture() matrix {
	return matrix{
		credentials: []Credential{ca, cb, cc},
		entries:     []Entry{e1, e2},
		grants: map[Entry]map[Credential]string{
			e1: {ca: "meant for e1 (docs/fixture.md)"},
			e2: {},
		},
	}
}

func truthful(m matrix, e Entry) Presenter {
	return func(_ *testing.T, c Credential) Result {
		ok := m.allowed(c, e)
		return Result{Accepted: ok, Effect: ok, Evidence: "fixture snapshot"}
	}
}

// lieAt is truthful except at credential at, where it returns res.
func lieAt(at Credential, res Result) func(matrix, Entry) Presenter {
	return func(m matrix, e Entry) Presenter {
		honest := truthful(m, e)
		return func(t *testing.T, c Credential) Result {
			if c == at {
				return res
			}
			return honest(t, c)
		}
	}
}

func probe(res Result) func(*testing.T) Result {
	return func(*testing.T) Result { return res }
}

var goodBaseline = probe(Result{Accepted: true, Effect: true, Evidence: "fixture baseline"})

type misuse struct {
	name    string
	mutate  func(*matrix)
	entry   Entry
	present func(matrix, Entry) Presenter // nil means truthful
	opts    []Option
	path    string // where the one error is reported; "" is the top level
	phrase  string
	cells   bool // whether cells run; contract and call problems run none
}

func misuses() []misuse {
	const fact = "fixture snapshot"
	return []misuse{
		{name: "accepted disallowed cell", entry: e1, present: lieAt(cb, Result{Accepted: true, Evidence: fact}),
			path: "e1/b", phrase: "credential not meant for this entry was accepted", cells: true},
		{name: "effect on a refused cell", entry: e1, present: lieAt(cc, Result{Effect: true, Evidence: fact}),
			path: "e1/c", phrase: "effect observed on a refused cell", cells: true},
		{name: "refused allowed cell", entry: e1, present: lieAt(ca, Result{Evidence: fact}),
			path: "e1/a", phrase: "allowed credential was refused", cells: true},
		{name: "allowed cell without effect", entry: e1, present: lieAt(ca, Result{Accepted: true, Evidence: fact}),
			path: "e1/a", phrase: "allowed credential accepted without an observed effect", cells: true},
		{name: "empty evidence", entry: e1, present: lieAt(cb, Result{}),
			path: "e1/b", phrase: "empty Evidence", cells: true},
		{name: "missing skip key", entry: e1, opts: []Option{Skip(cb, "")},
			phrase: `skip key "" is not a board key`},
		{name: "lower-case skip key", entry: e1, opts: []Option{Skip(cb, "pr1-01c")},
			phrase: `skip key "pr1-01c" is not a board key`},
		{name: "docs-row skip key", entry: e1, opts: []Option{Skip(cb, "PR1-01-DOCS")},
			phrase: `skip key "PR1-01-DOCS" is not a board key`},
		{name: "underscore skip key", entry: e1, opts: []Option{Skip(cb, "PR1_01C")},
			phrase: `skip key "PR1_01C" is not a board key`},
		{name: "table gap", entry: e1, mutate: func(m *matrix) { delete(m.grants, e2) },
			phrase: `no table row for entry "e2"`},
		{name: "stray row", entry: e1, mutate: func(m *matrix) { m.grants["ghost"] = map[Credential]string{} },
			phrase: `table row for unknown entry "ghost"`},
		{name: "stray cell", entry: e1, mutate: func(m *matrix) { m.grants[e1]["ghost"] = "stray (docs/fixture.md)" },
			phrase: `row "e1" names unknown credential "ghost"`},
		{name: "rationale without repo path", entry: e1, mutate: func(m *matrix) { m.grants[e1][ca] = "meant for e1" },
			phrase: "rationale for e1/a cites no repo path"},
		{name: "duplicate credential", entry: e1, mutate: func(m *matrix) { m.credentials = append(m.credentials, ca) },
			phrase: `duplicate credential "a"`},
		{name: "duplicate entry", entry: e1, mutate: func(m *matrix) { m.entries = append(m.entries, e1) },
			phrase: `duplicate entry "e1"`},
		{name: "no positive control", entry: e2,
			phrase: "no positive control: supply Baseline or an unskipped allowed credential"},
		{name: "positive control skipped", entry: e1, opts: []Option{Skip(ca, "PR1-01C")},
			phrase: "no positive control: supply Baseline or an unskipped allowed credential"},
		{name: "baseline without effect", entry: e2, opts: []Option{Baseline(probe(Result{Accepted: true, Evidence: "b"}))},
			path: "e2/baseline", phrase: "baseline must be Accepted with an observed Effect", cells: true},
		{name: "baseline without evidence", entry: e2, opts: []Option{Baseline(probe(Result{Accepted: true, Effect: true}))},
			path: "e2/baseline", phrase: "empty Evidence", cells: true},
		{name: "nil baseline probe", entry: e2, opts: []Option{Baseline(nil)},
			phrase: "Baseline probe is nil"},
		{name: "unknown entry", entry: "nope", phrase: `unknown entry "nope"`},
		{name: "nil presenter", entry: e1, present: func(matrix, Entry) Presenter { return nil },
			phrase: "nil Presenter"},
		{name: "skip for unknown credential", entry: e1, opts: []Option{Skip("ghost", "PR1-01C")},
			phrase: `Skip names unknown credential "ghost"`},
		{name: "skip given twice", entry: e1, opts: []Option{Skip(cb, "PR1-01C"), Skip(cb, "PR1-01C")},
			phrase: `Skip given twice for "b"`},
		{name: "baseline given twice", entry: e2, opts: []Option{Baseline(goodBaseline), Baseline(goodBaseline)},
			phrase: "Baseline given twice"},
	}
}

func TestRunReportsMisuse(t *testing.T) {
	for _, tc := range misuses() {
		t.Run(tc.name, func(t *testing.T) {
			m := fixture()
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			present := truthful(m, tc.entry)
			if tc.present != nil {
				present = tc.present(m, tc.entry)
			}
			log := record(t, m, tc.entry, present, tc.opts...)
			var errs []string
			ran := false
			for _, line := range log {
				ran = ran || strings.HasPrefix(line, "run ")
				if strings.HasPrefix(line, "error ") {
					errs = append(errs, line)
				}
			}
			want := "error " + tc.path + ": "
			if len(errs) != 1 || !strings.HasPrefix(errs[0], want) || !strings.Contains(errs[0], tc.phrase) {
				t.Fatalf("want exactly one %q line containing %q, got log %q", want, tc.phrase, log)
			}
			if ran != tc.cells {
				t.Fatalf("cells ran = %v, want %v; log %q", ran, tc.cells, log)
			}
		})
	}
}

func TestRunPassesHonestCells(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
		opts  []Option
		want  []string
	}{
		{name: "allowed credential", entry: e1,
			want: []string{"run e1", "run e1/a", "run e1/b", "run e1/c"}},
		{name: "baseline", entry: e2, opts: []Option{Baseline(goodBaseline)},
			want: []string{"run e2", "run e2/baseline", "run e2/a", "run e2/b", "run e2/c"}},
		{name: "keyed skip", entry: e1, opts: []Option{Skip(cb, "PR1-01C")},
			want: []string{"run e1", "run e1/a", "run e1/b", "skip e1/b: matrix skip PR1-01C", "run e1/c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixture()
			if log := record(t, m, tc.entry, truthful(m, tc.entry), tc.opts...); !slices.Equal(log, tc.want) {
				t.Fatalf("log %q, want %q", log, tc.want)
			}
		})
	}
}

func TestContractTable(t *testing.T) {
	if len(Credentials) != 16 || len(Entries) != 18 {
		t.Fatalf("got %d credentials and %d entries, want 16 and 18", len(Credentials), len(Entries))
	}
	if probs := (matrix{Credentials, Entries, grants}).problems(); len(probs) > 0 {
		t.Fatalf("contract problems: %q", probs)
	}
	gateway := []Credential{UserSession, APIKeyStatic, APIKeyManaged, GatewayJWT, OIDCBearer}
	golden := map[Entry][]Credential{
		HTTPAPI:                gateway,
		EdgeAPI:                gateway,
		SCIMAPI:                {SCIMBearer},
		MCPGateway:             gateway,
		GRPCAPI:                {APIKeyStatic, APIKeyManaged, GatewayJWT, OIDCBearer},
		BusGatewayHeartbeatTap: {WorkerSession},
		BusGatewayStream:       {WorkerSession, ServiceToken},
		BusJobSubmit:           {ServiceToken},
		BusWorker:              {WorkerSession},
		BusControl:             {ServiceToken},
		BusHandshake:           {WorkerCredential},
		BusWorkflowResult:      {ServiceToken},
		EdgeAgentdHook:         {AgentdNonce},
	}
	nowhere := []Credential{Anonymous, GatewayJWTWrongAudience, OIDCBearerWrongAudience,
		DelegationToken, WorkerSessionWrongAudience, AgentIDHeader}
	allowed := 0
	for _, e := range Entries {
		for _, c := range Credentials {
			got := Allowed(c, e)
			if got != slices.Contains(golden[e], c) {
				t.Errorf("Allowed(%s, %s) = %v, golden disagrees", c, e, got)
			}
			if got && slices.Contains(nowhere, c) {
				t.Errorf("%s is allowed at %s", c, e)
			}
			if got {
				allowed++
			}
		}
	}
	if allowed != 29 {
		t.Errorf("allowed cells = %d, want 29", allowed)
	}
	if Allowed("ghost", HTTPAPI) || Allowed(APIKeyStatic, "ghost") {
		t.Errorf("unknown values must give false")
	}
}

func TestRationaleCitationsExist(t *testing.T) {
	for _, e := range Entries {
		for _, c := range Credentials {
			why, ok := grants[e][c]
			if !ok {
				continue
			}
			paths := citation.FindAllString(why, -1)
			if len(paths) == 0 {
				t.Errorf("%s/%s cites no repo path", e, c)
			}
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(p))); err != nil {
					t.Errorf("%s/%s cites %s: %v", e, c, p, err)
				}
			}
		}
	}
}

// TestRunDrivesRealSubtests runs the exported Run over the real contract, so
// the realTB adapter and the subtest names are covered too.
func TestRunDrivesRealSubtests(t *testing.T) {
	calls := 0
	for _, e := range Entries {
		present := func(t *testing.T, c Credential) Result {
			calls++
			if want := "/" + string(e) + "/" + string(c); !strings.HasSuffix(t.Name(), want) {
				t.Errorf("subtest %q does not end in %q", t.Name(), want)
			}
			ok := Allowed(c, e)
			return Result{Accepted: ok, Effect: ok, Evidence: "self-test snapshot"}
		}
		var opts []Option
		if len(grants[e]) == 0 {
			opts = append(opts, Baseline(goodBaseline))
		}
		Run(t, e, present, opts...)
	}
	if want := len(Credentials) * len(Entries); calls != want {
		t.Errorf("presenter calls = %d, want %d", calls, want)
	}
}
