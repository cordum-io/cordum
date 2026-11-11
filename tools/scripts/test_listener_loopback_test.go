package scripts_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Test listeners must bind loopback only.
//
// Why: Windows Firewall keys its allow rules on the path of the test binary.
// Go builds every test binary at a new temp path, so a test that listens on
// all interfaces raises a firewall prompt on every local `go test` run, and no
// earlier answer ever matches. A loopback listener raises no prompt.
//
// What it catches, in every *_test.go file of the repository:
//   - R1: a nats-server `server.Options{...}` literal with no Host key, or a
//     Host string literal that is not loopback (nats-server binds 0.0.0.0 by
//     default);
//   - R2: net.Listen, net.ListenPacket or crypto/tls Listen with a string
//     literal address whose host is empty or not loopback;
//   - R3: net.ListenTCP, net.ListenUDP or net.ListenIP with a nil address or
//     an &net.TCPAddr, &net.UDPAddr or &net.IPAddr literal that has no IP or
//     whose IP is net.IPv4zero or net.IPv6unspecified.
//
// Limits: a non-literal address or Host is not evaluated, a dot-import hides
// the package name from the scanner, and a Host assigned after the literal is
// still flagged, so set Host inside the literal.
const (
	listenerRuleNATS   = "R1 nats-server-options"
	listenerRuleListen = "R2 listen-literal"
	listenerRuleAddr   = "R3 listen-addr"

	listenerNATSPath = "github.com/nats-io/nats-server/v2/server"
	listenerMinFiles = 100
	listenerAnchor   = "core/infra/bus/nats_test.go"
)

type listenerFinding struct {
	line int
	rule string
}

// listenerScanSource parses one Go source file and returns its findings.
func listenerScanSource(name string, src []byte) ([]listenerFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	imports := listenerImportNames(file)
	var out []listenerFinding
	add := func(pos token.Pos, rule string) {
		out = append(out, listenerFinding{line: fset.Position(pos).Line, rule: rule})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if listenerIsPkgSel(x.Type, imports, listenerNATSPath, "Options") && !listenerHostIsLoopback(x) {
				add(x.Pos(), listenerRuleNATS)
			}
		case *ast.CallExpr:
			if rule := listenerCallRule(x, imports); rule != "" {
				add(x.Pos(), rule)
			}
		}
		return true
	})
	return out, nil
}

// listenerImportNames maps each import name in the file to its import path.
func listenerImportNames(file *ast.File) map[string]string {
	names := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		p := strings.Trim(spec.Path.Value, "\"`")
		name := path.Base(p)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = p
	}
	return names
}

func listenerIsPkgSel(e ast.Expr, imports map[string]string, pkgPath, sel string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != sel {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && imports[id.Name] == pkgPath
}

func listenerStringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func listenerLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func listenerKeyedValue(lit *ast.CompositeLit, key string) (ast.Expr, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key {
			return kv.Value, true
		}
	}
	return nil, false
}

// listenerHostIsLoopback reports whether an Options literal keeps Host on
// loopback. A Host that is not a string literal is not evaluated.
func listenerHostIsLoopback(lit *ast.CompositeLit) bool {
	value, ok := listenerKeyedValue(lit, "Host")
	if !ok {
		return false
	}
	host, isLit := listenerStringLit(value)
	return !isLit || listenerLoopbackHost(host)
}

func listenerCallRule(call *ast.CallExpr, imports map[string]string) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	switch imports[id.Name] + "." + sel.Sel.Name {
	case "net.Listen", "net.ListenPacket", "crypto/tls.Listen":
		if listenerLiteralAddrIsWide(call.Args) {
			return listenerRuleListen
		}
	case "net.ListenTCP", "net.ListenUDP", "net.ListenIP":
		if len(call.Args) > 1 && listenerAddrIsWide(call.Args[1], imports) {
			return listenerRuleAddr
		}
	}
	return ""
}

// listenerLiteralAddrIsWide checks the (network, address) arguments of a
// Listen-style call. A host that cannot be split counts as all interfaces.
func listenerLiteralAddrIsWide(args []ast.Expr) bool {
	if len(args) < 2 {
		return false
	}
	if network, ok := listenerStringLit(args[0]); ok && strings.HasPrefix(network, "unix") {
		return false
	}
	addr, ok := listenerStringLit(args[1])
	if !ok {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	return err != nil || !listenerLoopbackHost(host)
}

// listenerAddrIsWide checks the address argument of ListenTCP, ListenUDP or
// ListenIP: nil, or an Addr literal with no IP or an unspecified IP.
func listenerAddrIsWide(arg ast.Expr, imports map[string]string) bool {
	if id, ok := arg.(*ast.Ident); ok {
		return id.Name == "nil"
	}
	u, ok := arg.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return false
	}
	lit, ok := u.X.(*ast.CompositeLit)
	if !ok {
		return false
	}
	if !listenerIsPkgSel(lit.Type, imports, "net", "TCPAddr") &&
		!listenerIsPkgSel(lit.Type, imports, "net", "UDPAddr") &&
		!listenerIsPkgSel(lit.Type, imports, "net", "IPAddr") {
		return false
	}
	ip, ok := listenerKeyedValue(lit, "IP")
	if !ok {
		return true
	}
	return listenerIsPkgSel(ip, imports, "net", "IPv4zero") ||
		listenerIsPkgSel(ip, imports, "net", "IPv6unspecified")
}

func TestTestListenerScannerRules(t *testing.T) {
	const std = `"net"; "crypto/tls"; "github.com/nats-io/nats-server/v2/server"`
	cases := []struct {
		name, imports, body string
		want                []string
	}{
		// R1: nats-server Options.
		{name: "r1 pointer literal without Host", body: `_ = &server.Options{Port: -1}`, want: []string{listenerRuleNATS}},
		{name: "r1 value literal without Host", body: `_ = server.Options{Port: -1, NoLog: true}`, want: []string{listenerRuleNATS}},
		{name: "r1 aliased import", imports: `natsserver "github.com/nats-io/nats-server/v2/server"`, body: `_ = &natsserver.Options{Port: -1}`, want: []string{listenerRuleNATS}},
		{name: "r1 Host 0.0.0.0", body: `_ = &server.Options{Host: "0.0.0.0", Port: -1}`, want: []string{listenerRuleNATS}},
		{name: "r1 Host empty", body: `_ = &server.Options{Host: "", Port: -1}`, want: []string{listenerRuleNATS}},
		{name: "r1 Host ipv6 unspecified", body: `_ = &server.Options{Host: "::", Port: -1}`, want: []string{listenerRuleNATS}},
		{name: "r1 clean Host 127.0.0.1", body: `_ = &server.Options{Host: "127.0.0.1", Port: -1}`},
		{name: "r1 clean Host localhost", body: `_ = &server.Options{Host: "localhost", Port: -1}`},
		{name: "r1 clean Host ::1", body: `_ = &server.Options{Host: "::1", Port: -1}`},
		{name: "r1 clean Host variable is not evaluated", body: `h := "0.0.0.0"; _ = &server.Options{Host: h}`},
		{name: "r1 clean Options from another package", imports: `"example.com/other/server"`, body: `_ = &server.Options{Port: -1}`},
		// R2: Listen-style calls with a literal address.
		{name: "r2 colon port", body: `_, _ = net.Listen("tcp", ":0")`, want: []string{listenerRuleListen}},
		{name: "r2 0.0.0.0", body: `_, _ = net.Listen("tcp", "0.0.0.0:0")`, want: []string{listenerRuleListen}},
		{name: "r2 ipv6 unspecified", body: `_, _ = net.Listen("tcp", "[::]:0")`, want: []string{listenerRuleListen}},
		{name: "r2 empty address", body: `_, _ = net.Listen("tcp", "")`, want: []string{listenerRuleListen}},
		{name: "r2 routable address", body: `_, _ = net.Listen("tcp", "192.168.1.5:0")`, want: []string{listenerRuleListen}},
		{name: "r2 tls Listen", body: `_, _ = tls.Listen("tcp", ":443", nil)`, want: []string{listenerRuleListen}},
		{name: "r2 ListenPacket", body: `_, _ = net.ListenPacket("udp", ":0")`, want: []string{listenerRuleListen}},
		{name: "r2 clean 127.0.0.1", body: `_, _ = net.Listen("tcp", "127.0.0.1:0")`},
		{name: "r2 clean localhost", body: `_, _ = net.Listen("tcp", "localhost:0")`},
		{name: "r2 clean ::1", body: `_, _ = net.Listen("tcp", "[::1]:0")`},
		{name: "r2 clean unix socket", body: `_, _ = net.Listen("unix", "/tmp/x.sock")`},
		{name: "r2 clean variable address", body: `a := ":0"; _, _ = net.Listen("tcp", a)`},
		{name: "r2 clean non-listener call", body: `_, _ = net.Dial("tcp", ":0")`},
		// R3: ListenTCP, ListenUDP and ListenIP with an address value.
		{name: "r3 ListenUDP nil", body: `_, _ = net.ListenUDP("udp", nil)`, want: []string{listenerRuleAddr}},
		{name: "r3 ListenIP nil", body: `_, _ = net.ListenIP("ip4:icmp", nil)`, want: []string{listenerRuleAddr}},
		{name: "r3 TCPAddr without IP", body: `_, _ = net.ListenTCP("tcp", &net.TCPAddr{Port: 0})`, want: []string{listenerRuleAddr}},
		{name: "r3 UDPAddr with IPv4zero", body: `_, _ = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero})`, want: []string{listenerRuleAddr}},
		{name: "r3 clean TCPAddr loopback IP", body: `_, _ = net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})`},
		{name: "r3 clean variable address", body: `_, _ = net.ListenUDP("udp", addr)`},
		// All rules in one file.
		{name: "all rules at once", body: `_ = &server.Options{}
_, _ = net.Listen("tcp", ":0")
_, _ = net.ListenUDP("udp", nil)`, want: []string{listenerRuleNATS, listenerRuleListen, listenerRuleAddr}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imports := tc.imports
			if imports == "" {
				imports = std
			}
			src := "package p\n\nimport (" + imports + ")\n\nfunc f() {\n" + tc.body + "\n}\n"
			found, err := listenerScanSource("snippet.go", []byte(src))
			if err != nil {
				t.Fatalf("parse snippet: %v\n%s", err, src)
			}
			got := make([]string, 0, len(found))
			for _, f := range found {
				got = append(got, f.rule)
			}
			sort.Strings(got)
			want := slices.Clone(tc.want)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Fatalf("rules = %v, want %v\n%s", got, want, src)
			}
		})
	}
}

func TestTestListenersBindLoopback(t *testing.T) {
	root := repositoryRoot(t)
	var findings []string
	scanned, sawAnchor := 0, false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && listenerSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		found, err := listenerScanSource(rel, src)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		scanned++
		sawAnchor = sawAnchor || rel == listenerAnchor
		for _, f := range found {
			findings = append(findings, fmt.Sprintf("%s:%d: %s", rel, f.line, f.rule))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan test files under %s: %v", root, err)
	}
	if scanned <= listenerMinFiles || !sawAnchor {
		t.Fatalf("scanned %d test files (anchor %s seen: %v) under %s; the repository root looks wrong", scanned, listenerAnchor, sawAnchor, root)
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Fatalf("test listeners bind all interfaces (Windows Firewall prompts on every run):\n%s\nbind test listeners to 127.0.0.1", strings.Join(findings, "\n"))
	}
}

func listenerSkipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".")
}
