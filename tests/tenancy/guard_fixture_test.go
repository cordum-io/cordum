package tenancy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/cordum/cordum/tests/contract"
)

var manifestFixtureCases = []struct {
	name   string
	change func([]storeKeyEntry) ([]storeKeyEntry, []notTenantKey)
	want   []string
}{
	{"clean", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) { return m, nil }, nil},
	{"planted", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) { return m[:2], nil },
		[]string{"unlisted core/other planted:secret:{tenant}"}},
	{"stale manifest", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return append(m, storeKeyEntry{"old:{id}", "core/store", "global-id", "pending:PR1-03G"}), nil
	}, []string{"stale manifest core/store old:{id}"}},
	{"stale classification", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return m, []notTenantKey{{"old:{id}", "core/store", "Process coordination token."}}
	}, []string{"stale classification core/store old:{id}"}},
	{"unknown test", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		m[0].Coverage = "TestUnknown"
		return m, nil
	}, []string{"coverage core/store document:tenant:record: TestUnknown declared in 0 directories"}},
	{"ambiguous test", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		m[0].Coverage = "TestDup"
		return m, nil
	}, []string{"coverage core/store document:tenant:record: TestDup declared in 2 directories"}},
	{"binding", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		m[0].Binding = "tenant-in-key, enforced at "
		return m, nil
	}, []string{"invalid binding core/store document:tenant:record"}},
	{"coverage", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		m[0].Coverage = "pending:PR1-03I"
		return m, nil
	}, []string{"invalid coverage core/store document:tenant:record"}},
	{"blank reason", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return m[:2], []notTenantKey{{m[2].Pattern, m[2].Package, " \t"}}
	}, []string{"empty reason core/other planted:secret:{tenant}"}},
	{"classification", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return m[:2], []notTenantKey{{m[2].Pattern, m[2].Package, "Synthetic coordination token."}}
	}, nil},
	{"collection literal", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return append(m, storeKeyEntry{"collection:{tenant}", "core/collection", "tenant-in-key", "pending:PR1-03H"}), nil
	}, nil},
	{"composed literals", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return append(m, storeKeyEntry{"namespace:keys:tenant:{id}", "core/composed", "tenant-in-key", "pending:PR1-03H"}), nil
	}, nil},
	{"stale composed literal", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		return append(m, storeKeyEntry{"namespace:keys:ten:{id}", "core/composed", "tenant-in-key", "pending:PR1-03H"}), nil
	}, []string{"stale manifest core/composed namespace:keys:ten:{id}"}},
	{"accepted annotations", func(m []storeKeyEntry) ([]storeKeyEntry, []notTenantKey) {
		m[0].Binding = "tenant-in-value, enforced at Store.Get"
		m[0].Coverage = "open-finding:PR1-03-FIX"
		return m, nil
	}, nil},
}

func TestTenantIsolationManifestGuardFixture(t *testing.T) {
	inventory, err := contract.LoadDataInventory("testdata/inventory.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base := []storeKeyEntry{
		{"document:tenant:record", "core/store", "tenant-in-value", "TestExisting"},
		{"single:{tenant}", "core/store", "tenant-in-key", "pending:PR1-03B"},
		{"planted:secret:{tenant}", "core/other", "global-id", "pending:PR1-03H"},
	}
	for _, tc := range manifestFixtureCases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, exclusions := tc.change(slices.Clone(base))
			got := checkManifest(inventoryKeys(inventory), manifest, exclusions,
				map[string][]string{"TestExisting": {"core/store"}, "TestDup": {"a", "b"}},
				map[string][]string{"core/collection": {"collection:%s"}, "core/composed": {"namespace:keys:", "tenant"}})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("messages = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTenantIsolationPatternMatching(t *testing.T) {
	cases := []struct {
		pattern, entry string
		want           bool
	}{
		{"key:{suffix}", "key:tenant:record", true},
		{"key:{tenant}", "key:tenant:record", false},
		{"key:{tenant}", "key:", false},
		{"key:{suffix}", "key:", false},
		{"key:{tenant}", "other:key:tenant", false},
		{"key:{tenant}:end", "key:tenant:end:extra", false},
		{"key.v1:{tenant}", "keyXv1:tenant", false},
		{"key.v1:{tenant}", "key.v1:tenant", true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.entry, func(t *testing.T) {
			if got := patternCovers(tc.pattern, tc.entry); got != tc.want {
				t.Fatalf("patternCovers(%q, %q) = %v", tc.pattern, tc.entry, got)
			}
		})
	}
}

func TestTenantIsolationWalkModule(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":        "module fixture",
		"root.go":       "package fixture\nconst key = `root:key`",
		"root_test.go":  "package fixture\nfunc Test() {}",
		"a/a_test.go":   "package a\nfunc TestDup() {}\ntype T struct{}\nfunc (T) TestX() {}",
		"a/b_test.go":   "package a\nfunc TestDup() {}",
		"b/b_test.go":   "package b\nfunc TestDup() {}",
		"nested/go.mod": "module nested",
	}
	for _, dir := range []string{"testdata", "vendor", "node_modules", ".hidden", "_private", "nested"} {
		files[dir+"/skip_test.go"] = "not valid Go"
	}
	writeModuleFiles(t, root, files)
	var got []string
	if err := walkModule(root, func(rel string) error { got = append(got, rel); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{"a/a_test.go", "a/b_test.go", "b/b_test.go", "root.go", "root_test.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("walk = %q, want %q", got, want)
	}
	dirs, err := testFuncDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"Test": {"."}, "TestDup": {"a", "b"}}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("test directories = %v, want %v", dirs, want)
	}
	sentinel := errors.New("visitor stopped")
	if err := walkModule(root, func(string) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("visitor error = %v, want %v", err, sentinel)
	}
	writeModuleFiles(t, root, map[string]string{"bad_test.go": "not valid Go"})
	if _, err := testFuncDirs(root); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestTenantIsolationPackageLiterals(t *testing.T) {
	root := t.TempDir()
	writeModuleFiles(t, root, map[string]string{
		"collection/store.go":      "package collection\nconst key = `tenant:`\nvar other = \"quoted:key\"",
		"collection/store_test.go": "package collection\nconst fixture = `fixture:key`",
	})
	got, err := packageLiterals(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"collection": {"tenant:", "quoted:key"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("literals = %v, want %v", got, want)
	}
	writeModuleFiles(t, root, map[string]string{"bad.go": "not valid Go"})
	if _, err := packageLiterals(root); err == nil {
		t.Fatal("expected parse error")
	}
}

func writeModuleFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		name := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
