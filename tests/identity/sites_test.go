package identity

// The scanners behind TestGuardInventory. Each parses a tree once and lists
// its sites, so TestGuardDetectsNewSites can run them on a fixture tree.

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var issuerNames = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(issue|mint|generate)[A-Za-z]*(token|key|credential|session|secret|nonce)`),
	regexp.MustCompile(`^new[A-Za-z]*(Token|Session)$`),
}

var tokenType = regexp.MustCompile(`^Typ[A-Z]`)

var subscribeCalls = []string{"Subscribe", "SubscribeWithContext", "QueueSubscribe", "ChanSubscribe", "QueueRespond"}

func (g goFile) text(n ast.Node) string {
	b := g.src[g.fset.Position(n.Pos()).Offset:g.fset.Position(n.End()).Offset]
	return strings.Join(strings.Fields(string(b)), " ")
}

// funcName names the declaration that encloses a site: "(*T).Name",
// "Name", or "package" for a declaration outside any function.
func (g goFile) funcName(d ast.Decl) string {
	fn, ok := d.(*ast.FuncDecl)
	switch {
	case !ok:
		return "package"
	case fn.Recv == nil || len(fn.Recv.List) == 0:
		return fn.Name.Name
	}
	return "(" + g.text(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

// productSites parses the non-test files under root/core and root/cmd and
// lists their sites.
func productSites(t *testing.T, root string) []site {
	t.Helper()
	files := parseTree(t, root, func(rel string) bool {
		return (strings.HasPrefix(rel, "core/") || strings.HasPrefix(rel, "cmd/")) && !strings.HasSuffix(rel, "_test.go")
	})
	consts := map[string]map[string]ast.Expr{} // package directory -> const name -> value
	for _, g := range files {
		dir := path.Dir(g.fset.Position(g.file.Pos()).Filename)
		if consts[dir] == nil {
			consts[dir] = map[string]ast.Expr{}
		}
		for _, d := range g.file.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.CONST {
				for _, s := range gd.Specs {
					v := s.(*ast.ValueSpec)
					for i, n := range v.Names {
						if i < len(v.Values) {
							consts[dir][n.Name] = v.Values[i]
						}
					}
				}
			}
		}
	}
	var sites []site
	for _, g := range files {
		rel := g.fset.Position(g.file.Pos()).Filename
		s := &fileScan{g: g, rel: rel, consts: consts[path.Dir(rel)]}
		sites = append(sites, s.scan()...)
	}
	return sites
}

// fileScan lists the sites in one product file.
type fileScan struct {
	g               goFile
	rel, fn         string
	net, http, grpc string
	consts          map[string]ast.Expr
	seen            map[*ast.CompositeLit]bool
	sites           []site
}

func (s *fileScan) add(kind, subject string, p token.Pos, auto Entry) {
	s.sites = append(s.sites, site{s.rel, s.fn, kind, subject, s.g.at(p), auto})
}

func (s *fileScan) scan() []site {
	s.net, _ = importName(s.g.file, "net")
	s.http, _ = importName(s.g.file, "net/http")
	s.grpc, _ = importName(s.g.file, "google.golang.org/grpc")
	s.seen = map[*ast.CompositeLit]bool{}
	for _, d := range s.g.file.Decls {
		s.fn = s.g.funcName(d)
		s.source(d)
		ast.Inspect(d, s.node)
	}
	return s.sites
}

// source records credential sources: issuer functions, AuthSource consts in
// the gateway auth package and token types in core/auth/servicetoken.
func (s *fileScan) source(d ast.Decl) {
	if fn, ok := d.(*ast.FuncDecl); ok {
		if slices.ContainsFunc(issuerNames, func(re *regexp.Regexp) bool { return re.MatchString(fn.Name.Name) }) {
			s.add("issuer", fn.Name.Name, fn.Pos(), "")
		}
		return
	}
	gd, ok := d.(*ast.GenDecl)
	if !ok || gd.Tok != token.CONST {
		return
	}
	dir := path.Dir(s.rel)
	for _, spec := range gd.Specs {
		v := spec.(*ast.ValueSpec)
		typ, _ := v.Type.(*ast.Ident)
		for _, n := range v.Names {
			switch {
			case dir == "core/controlplane/gateway/auth" && typ != nil && typ.Name == "AuthSource":
				s.add("auth_source", n.Name, n.Pos(), "")
			case dir == "core/auth/servicetoken" && tokenType.MatchString(n.Name):
				s.add("token_type", n.Name, n.Pos(), "")
			}
		}
	}
}

// node records listeners, bus subscriptions and gateway routes.
func (s *fileScan) node(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.AssignStmt:
		for i, r := range n.Rhs {
			if lit := s.serverLit(r); lit != nil && i < len(n.Lhs) {
				s.seen[lit] = true
				s.add("listen", "http.Server{} "+s.g.text(n.Lhs[i]), lit.Pos(), "")
			}
		}
	case *ast.CompositeLit:
		if s.serverLit(n) != nil && !s.seen[n] {
			s.add("listen", "http.Server{}", n.Pos(), "")
		}
	case *ast.CallExpr:
		s.call(n)
	}
	return true
}

// serverLit returns e as an http.Server literal, or nil.
func (s *fileScan) serverLit(e ast.Expr) *ast.CompositeLit {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	if name, ok := pkgSel(lit.Type, s.http); !ok || name != "Server" {
		return nil
	}
	return lit
}

func (s *fileScan) call(c *ast.CallExpr) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	name := sel.Sel.Name
	if pkg, isPkg := sel.X.(*ast.Ident); isPkg && (pkg.Name == s.net || pkg.Name == s.http || pkg.Name == s.grpc) {
		switch {
		case pkg.Name == s.net && strings.HasPrefix(name, "Listen"),
			pkg.Name == s.http && (name == "ListenAndServe" || name == "ListenAndServeTLS"),
			pkg.Name == s.grpc && name == "NewServer":
			s.add("listen", s.g.text(sel), c.Pos(), "")
		}
		return
	}
	switch {
	case name == "ListenAndServe" || name == "ListenAndServeTLS":
		s.add("listen", s.g.text(sel), c.Pos(), "")
	case slices.Contains(subscribeCalls, name) && len(c.Args) > 0:
		s.add("subscribe", s.render(c.Args[0]), c.Pos(), "")
	case strings.HasPrefix(s.rel, "core/controlplane/gateway/"):
		s.route(c, name)
	}
}

// route records a gateway route registration with its evaluated pattern.
func (s *fileScan) route(c *ast.CallExpr, name string) {
	var arg ast.Expr
	switch {
	case name == "registerRoute" && len(c.Args) == 3:
		arg = c.Args[1]
	case (name == "HandleFunc" || name == "Handle") && len(c.Args) == 2:
		arg = c.Args[0]
	default:
		return
	}
	pattern, ok := s.constString(arg)
	if !ok {
		pattern = s.g.text(arg)
	}
	s.add("route", pattern, c.Pos(), routeEntry(pattern))
}

// render writes a subscription's first argument as source text: a string
// literal unquoted, a func literal as "func".
func (s *fileScan) render(e ast.Expr) string {
	if _, ok := e.(*ast.FuncLit); ok {
		return "func"
	}
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if v, err := strconv.Unquote(lit.Value); err == nil {
			return v
		}
	}
	return s.g.text(e)
}

// constString evaluates a string built from literals, same-package consts and +.
func (s *fileScan) constString(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		v, err := strconv.Unquote(e.Value)
		return v, e.Kind == token.STRING && err == nil
	case *ast.Ident:
		if v, ok := s.consts[e.Name]; ok {
			return s.constString(v)
		}
	case *ast.ParenExpr:
		return s.constString(e.X)
	case *ast.BinaryExpr:
		l, lok := s.constString(e.X)
		r, rok := s.constString(e.Y)
		return l + r, e.Op == token.ADD && lok && rok
	}
	return "", false
}

// routeEntry maps a gateway route pattern to its Entry by path prefix, or "".
func routeEntry(pattern string) Entry {
	p := pattern[strings.IndexByte(pattern, ' ')+1:] // drop a "METHOD " prefix
	switch {
	case strings.HasPrefix(p, "/api/v1/edge/"):
		return EdgeAPI
	case strings.HasPrefix(p, "/api/v1/scim/v2"):
		return SCIMAPI
	case strings.HasPrefix(p, "/mcp/"):
		return MCPGateway
	case strings.HasPrefix(p, "/api/"):
		return HTTPAPI
	}
	return ""
}
