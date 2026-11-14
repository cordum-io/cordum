package contract

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DataInventory records source-reviewed storage facts, not cleanup guarantees.
type DataInventory struct {
	SchemaVersion int    `yaml:"schema_version"`
	ReviewedAt    string `yaml:"reviewed_at"`
	Classes       []DataClass
	EndpointOnly  []DataEntry    `yaml:"endpoint_only"`
	ControlPlane  []DataEntry    `yaml:"control_plane"`
	NotTenantData []DataKeyClass `yaml:"not_tenant_data"`
}

// DataKeyClass marks key-shaped literals in one Go file as holding no tenant
// data. Each pattern's text before its first '{' must prefix a literal there.
type DataKeyClass struct {
	Path, Reason string
	Patterns     []string
}

type DataClass struct{ ID, Title string }
type CodeRef = EnforcementPoint
type DataLiteralRef struct{ Name, Path string }
type DataKeyRef struct{ Pattern, Path string }
type DataEntry struct {
	ID, Class string
	Storage   *DataStorage
	Content   *DataContent
	Retention *DataRetention
	Deletion  *DataDeletion
	LegalHold *DataLegalHold `yaml:"legal_hold"`
}
type DataStorage struct {
	Kind, Location string
	RedisKeys      []DataKeyRef `yaml:"redis_keys"`
	Streams        []DataLiteralRef
	Writers        []CodeRef
}
type DataContent struct {
	Description  string
	PersonalData string `yaml:"personal_data"`
}
type DataRetention struct {
	Settings      []DataLiteralRef
	Default, None string
}
type DataDeletion struct {
	Paths []CodeRef
	None  string
}
type DataLegalHold struct {
	Applies *bool
	Reason  string
	Refs    []CodeRef
}

var requiredDataClasses = []string{"raw-local-inputs", "redacted-events", "evidence-audit", "hashes-commitments", "artifacts", "telemetry", "backups", "exports"}
var dataIDRE = regexp.MustCompile(`^DI-[EC][0-9]{2}$`)

// LoadDataInventory accepts exactly one schema-v1 document, with no unknown keys.
func LoadDataInventory(file string) (*DataInventory, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var d DataInventory
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if d.SchemaVersion != 1 {
		return nil, fmt.Errorf("%s: schema_version must be 1", file)
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one YAML document", file)
	}
	return &d, nil
}

// Validate reuses Issue.ClaimID for the data entry id; document issues have no id.
func (d *DataInventory) Validate(root string) []Issue {
	var is issues
	if d == nil || d.SchemaVersion != 1 || !shaRE.MatchString(d.ReviewedAt) {
		is.add("schema", "", "schema_version must be 1 and reviewed_at a 40-hex SHA")
	}
	if d == nil {
		return is
	}
	classes, used, seen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range d.Classes {
		if blank(c.ID) || blank(c.Title) || classes[c.ID] {
			is.add("classes", "", "invalid or duplicate class: "+c.ID)
		}
		classes[c.ID] = true
	}
	for _, id := range requiredDataClasses {
		if !classes[id] {
			is.add("classes", "", "required class missing: "+id)
		}
	}
	for i, section := range [][]DataEntry{d.EndpointOnly, d.ControlPlane} {
		for _, e := range section {
			prefix := "DI-C"
			if i == 0 {
				prefix = "DI-E"
			}
			if !dataIDRE.MatchString(e.ID) || !strings.HasPrefix(e.ID, prefix) || seen[e.ID] || !classes[e.Class] {
				is.add("entry-fields", e.ID, "invalid/duplicate id or unknown class: "+e.Class)
			}
			seen[e.ID], used[e.Class] = true, true
			e.validateData(root, i == 0, &is)
		}
	}
	for _, c := range d.Classes {
		if !used[c.ID] {
			is.add("classes", "", "class has no entry: "+c.ID)
		}
	}
	for _, c := range d.NotTenantData {
		c.validate(root, &is)
	}
	return is
}

var dataPlaceholderRE = regexp.MustCompile(`\{([^{}]*)\}`)
var dataTokenRE = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`)

// identityPlaceholders lists the placeholders of a pattern that name a tenant,
// user, email, session, agent, IP address or job, in any case or plural, alone
// or with an id/addr/address suffix ({tenantID}, {client_ip}, {jobs}).
func identityPlaceholders(pattern string) []string {
	var out []string
	for _, m := range dataPlaceholderRE.FindAllStringSubmatch(pattern, -1) {
		if slices.ContainsFunc(dataTokenRE.FindAllString(m[1], -1), isIdentityToken) {
			out = append(out, m[0])
		}
	}
	return out
}

func isIdentityToken(tok string) bool {
	for _, base := range []string{"tenant", "user", "email", "session", "agent", "ip", "job"} {
		if rest, ok := strings.CutPrefix(strings.ToLower(tok), base); ok && slices.Contains([]string{"", "s", "id", "ids", "addr", "address", "v4", "v6"}, rest) {
			return true
		}
	}
	return false
}

func (c DataKeyClass) validate(root string, is *issues) {
	id := "not_tenant_data:" + c.Path
	if len(c.Patterns) == 0 || len(strings.TrimSpace(c.Reason)) < 20 {
		is.add("not-tenant-data", id, "patterns and a reason of at least 20 characters required")
	}
	for _, p := range c.Patterns {
		if ph := identityPlaceholders(p); len(ph) > 0 {
			is.add("not-tenant-data", id, fmt.Sprintf("%q names %v; keys linked to a tenant, user, email, session, agent, IP or job need an entry", p, ph))
		}
		prefix, _, _ := strings.Cut(p, "{")
		dataReference(is, id, "not_tenant_data.patterns", c.Path, p, prefix != "" && hasStringLiteral(root, c.Path, prefix, true))
	}
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func (e DataEntry) validateData(root string, endpoint bool, is *issues) {
	if e.Content == nil || blank(e.Content.Description) || !slices.Contains([]string{"yes", "no", "possible"}, e.Content.PersonalData) {
		is.add("entry-fields", e.ID, "content description and personal_data (yes/no/possible) required")
	}
	e.validateStorage(root, endpoint, is)
	if r := e.Retention; r == nil {
		is.add("retention", e.ID, "retention missing")
	} else {
		if !dataAlternative(len(r.Settings), r.None) {
			is.add("retention", e.ID, "settings OR none with a reason of at least 20 characters required")
		}
		for _, s := range r.Settings {
			dataReference(is, e.ID, "retention.settings", s.Path, s.Name, hasStringLiteral(root, s.Path, s.Name))
		}
	}
	if d := e.Deletion; d == nil {
		is.add("deletion", e.ID, "deletion missing")
	} else {
		if !dataAlternative(len(d.Paths), d.None) {
			is.add("deletion", e.ID, "paths OR none with a reason of at least 20 characters required")
		}
		checkDataCode(root, e.ID, "deletion.paths", d.Paths, is)
	}
	if h := e.LegalHold; h == nil {
		is.add("legal-hold", e.ID, "legal_hold missing")
	} else {
		if h.Applies == nil || blank(h.Reason) || (*h.Applies && len(h.Refs) == 0) {
			is.add("legal-hold", e.ID, "applies and reason required; true also needs refs")
		}
		checkDataCode(root, e.ID, "legal_hold.refs", h.Refs, is)
	}
}

func dataAlternative(count int, none string) bool {
	return (count > 0 && blank(none)) || (count == 0 && len(strings.TrimSpace(none)) >= 20)
}

func (e DataEntry) validateStorage(root string, endpoint bool, is *issues) {
	s := e.Storage
	if s == nil {
		is.add("entry-fields", e.ID, "storage missing")
		return
	}
	if blank(s.Location) || !slices.Contains([]string{"file", "process_memory", "redis_key", "redis_stream", "jetstream", "external"}, s.Kind) {
		is.add("entry-fields", e.ID, "storage location and a known kind required")
	}
	if endpoint && (s.Kind != "file" && s.Kind != "process_memory" || len(s.RedisKeys) > 0 || len(s.Streams) > 0) {
		is.add("endpoint", e.ID, "endpoint_only requires file/process_memory and no Redis keys or streams")
	}
	checkDataCode(root, e.ID, "storage.writers", s.Writers, is)
	for _, k := range s.RedisKeys {
		prefix, _, _ := strings.Cut(k.Pattern, "{")
		ok := strings.Contains(prefix, ":") && hasStringLiteral(root, k.Path, prefix, true)
		dataReference(is, e.ID, "storage.redis_keys", k.Path, k.Pattern, ok)
	}
	for _, s := range s.Streams {
		dataReference(is, e.ID, "storage.streams", s.Path, s.Name, hasStringLiteral(root, s.Path, s.Name))
	}
}

func checkDataCode(root, id, field string, refs []CodeRef, is *issues) {
	for _, ref := range refs {
		dataReference(is, id, field, ref.Path, ref.Symbol, hasSymbol(root, ref.Path, ref.Symbol))
	}
}

func dataReference(is *issues, id, field, path, value string, ok bool) {
	if !ok {
		is.add("reference", id, fmt.Sprintf("%s: %q not found in %s", field, value, path))
	}
}

// hasStringLiteral matches exact literals by default; the optional prefix mode
// is reserved for Redis key patterns (not settings or stream names).
func hasStringLiteral(root, rel, value string, prefix ...bool) bool {
	full, err := repoFile(root, rel)
	if err != nil || value == "" || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	f, err := goFile(full)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			text, err := strconv.Unquote(lit.Value)
			if err == nil && (text == value || len(prefix) == 1 && prefix[0] && strings.HasPrefix(text, value)) {
				found = true
			}
		}
		return !found
	})
	return found
}
