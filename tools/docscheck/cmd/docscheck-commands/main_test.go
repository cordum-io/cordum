package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quickstart.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func command(attrs, script string) string {
	return "<!-- docscheck: command " + attrs + " -->\n```bash\n" + script + "\n```\n"
}

func TestRunPass(t *testing.T) {
	doc := command(`exit="0" expect='"ok": true'`, `printf '"ok": true\n'`) +
		"<!-- docscheck: skip reason=\"requires a running cluster\" -->\n```sh\nexit 9\n```\n"
	var out bytes.Buffer
	if code := run([]string{"-doc", fixture(t, doc)}, &out); code != 0 ||
		!strings.Contains(out.String(), "checked=1 unchecked=1 failed=0") {
		t.Fatalf("code=%d output=%s", code, &out)
	}
}

func TestRunMismatch(t *testing.T) {
	cases := []struct{ name, attrs, script, want string }{
		{"exit", `exit="0"`, "exit 7", "exit code 7, expected 0"},
		{"fragment", `exit="0" expect="absent"`, "echo actual", `missing output fragment "absent"`},
		{"runner messages", `exit="0" expect="checked"`, "true", `missing output fragment "checked"`},
		{"pipefail", `exit="0"`, "false | cat", "exit code 1, expected 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := fixture(t, command(tc.attrs, tc.script))
			var out bytes.Buffer
			if code := run([]string{"-doc", path}, &out); code != 1 ||
				!strings.Contains(out.String(), path+":3: "+tc.want) ||
				!strings.Contains(out.String(), "checked=1 unchecked=0 failed=1") {
				t.Fatalf("code=%d output=%s", code, &out)
			}
		})
	}
}

func TestRunRefusesBeforeExecution(t *testing.T) {
	cases := []struct{ name, doc, line string }{
		{"unmarked", "```bash\ntrue\n```\n", "5"},
		{"endpoints only", "<!-- docscheck: endpoints -->\n```bash\ntrue\n```\n", "6"},
		{"missing exit", command("", "true"), "5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sentinel := filepath.Join(t.TempDir(), "must-not-exist")
			t.Setenv("SENTINEL", filepath.ToSlash(sentinel))
			path := fixture(t, command(`exit="0"`, `touch "$SENTINEL"`)+tc.doc)
			var out bytes.Buffer
			if code := run([]string{"-doc", path, "-env", "SENTINEL"}, &out); code != 1 ||
				!strings.Contains(out.String(), path+":"+tc.line+":") {
				t.Fatalf("code=%d output=%s", code, &out)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("sentinel exists or stat failed: %v", err)
			}
		})
	}
}

func TestRunIsolation(t *testing.T) {
	record := filepath.Join(t.TempDir(), "where")
	t.Setenv("RECORD", filepath.ToSlash(record))
	script := `test "$(cd "$HOME" && pwd -P)" = "$(pwd -P)"
printf '%s' "$HOME" > "$RECORD"
touch docscheck-isolation-sentinel`
	doc := command(`exit="0"`, script) + command(`exit="0"`, "test ! -e docscheck-isolation-sentinel")
	var out bytes.Buffer
	if code := run([]string{"-doc", fixture(t, doc), "-env", "RECORD", "-env", "HOME"}, &out); code != 0 {
		t.Fatalf("code=%d output=%s", code, &out)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.FromSlash(string(data))
	if dir == "" || filepath.Clean(dir) == cwd {
		t.Fatalf("not isolated: %q", dir)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remains: %v", err)
	}
	if _, err := os.Stat("docscheck-isolation-sentinel"); !os.IsNotExist(err) {
		t.Fatalf("file escaped temporary directory: %v", err)
	}
}

func TestRunUnlistedEnvironment(t *testing.T) {
	t.Setenv("DOCSCHECK_UNLISTED", "host-value")
	t.Setenv("DOCSCHECK_ALLOWED", "allowed-value")
	doc := command(`exit="0"`, `test -z "${DOCSCHECK_UNLISTED:-}"; test "$DOCSCHECK_ALLOWED" = allowed-value`)
	var out bytes.Buffer
	if code := run([]string{"-doc", fixture(t, doc), "-env", "DOCSCHECK_ALLOWED"}, &out); code != 0 {
		t.Fatalf("code=%d output=%s", code, &out)
	}
}

func TestRunCopies(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "value"), []byte("copied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CONFIG_OK=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := command(`exit="0" expect="copied"`, ". ./.env; test \"$CONFIG_OK\" = yes; cat config/value")
	var out bytes.Buffer
	if code := run([]string{"-doc", fixture(t, doc), "-root", root, "-copy", ".env", "-copy", "config/value"}, &out); code != 0 {
		t.Fatalf("code=%d output=%s", code, &out)
	}
}

func TestRunDotenv(t *testing.T) {
	const sentinel = "dotenv-sentinel-7f3a"
	t.Setenv("DOCSCHECK_DOTENV", sentinel)
	cases := []struct {
		name, before string
		env          []string
	}{
		{"file only", `test -z "${DOCSCHECK_DOTENV:-}"`, nil},
		{"file and env", `test "$DOCSCHECK_DOTENV" = ` + sentinel, []string{"-env", "DOCSCHECK_DOTENV"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := tc.before + "\nsource .env && test \"$DOCSCHECK_DOTENV\" = " + sentinel
			if runtime.GOOS != "windows" {
				script += "\ntest \"$(find .env -perm 0600)\" = .env"
			}
			args := append([]string{"-doc", fixture(t, command(`exit="0"`, script)), "-dotenv", "DOCSCHECK_DOTENV"}, tc.env...)
			var out bytes.Buffer
			if code := run(args, &out); code != 0 || strings.Contains(out.String(), sentinel) {
				t.Fatalf("code=%d output=%s", code, &out)
			}
		})
	}
}

func TestRunDotenvRefuses(t *testing.T) {
	t.Setenv("DOCSCHECK_DOTENV_NEWLINE", "dotenv-sentinel\nINJECTED=1")
	for _, name := range []string{"DOCSCHECK_DOTENV_UNSET", "DOCSCHECK_DOTENV_NEWLINE"} {
		t.Run(name, func(t *testing.T) {
			ran := filepath.Join(t.TempDir(), "must-not-exist")
			t.Setenv("SENTINEL", filepath.ToSlash(ran))
			path := fixture(t, command(`exit="0"`, `touch "$SENTINEL"`))
			var out bytes.Buffer
			if code := run([]string{"-doc", path, "-env", "SENTINEL", "-dotenv", name}, &out); code != 1 ||
				!strings.Contains(out.String(), `-dotenv "`+name+`"`) || strings.Contains(out.String(), "dotenv-sentinel") {
				t.Fatalf("code=%d output=%s", code, &out)
			}
			if _, err := os.Stat(ran); !os.IsNotExist(err) {
				t.Fatalf("command ran: %v", err)
			}
		})
	}
}

func TestRunCopyErrors(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"../escape", root, "missing"} {
		t.Run(name, func(t *testing.T) {
			sentinel := filepath.Join(t.TempDir(), "must-not-exist")
			t.Setenv("SENTINEL", filepath.ToSlash(sentinel))
			path := fixture(t, command(`exit="0"`, `touch "$SENTINEL"`))
			var out bytes.Buffer
			if code := run([]string{"-doc", path, "-root", root, "-copy", name, "-env", "SENTINEL"}, &out); code != 1 || !strings.Contains(out.String(), path+":3:") {
				t.Fatalf("code=%d output=%s", code, &out)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("command ran: %v", err)
			}
		})
	}
}

func TestRunTimeout(t *testing.T) {
	path := fixture(t, command(`exit="0"`, "while :; do :; done"))
	var out bytes.Buffer
	start := time.Now()
	if code := run([]string{"-doc", path, "-timeout", "100ms"}, &out); code != 1 || !strings.Contains(out.String(), path+":3: timeout") {
		t.Fatalf("code=%d output=%s", code, &out)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestRunOutput(t *testing.T) {
	cases := []struct {
		name, attrs, script, want string
		code                      int
	}{
		{"stderr and nonzero", `exit="7" expect="stderr"`, "echo stderr >&2; exit 7", "checked=1 unchecked=0 failed=0", 0},
		{"tail", `exit="0"`, "i=0; while [ $i -lt 25 ]; do echo line-$i; i=$((i+1)); done; exit 2", "line-5\n", 1},
		{"output limit", `exit="0"`, "head -c 1048577 /dev/zero", "command output exceeds 1 MiB", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run([]string{"-doc", fixture(t, command(tc.attrs, tc.script))}, &out); code != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("code=%d expected=%d; expected output %q", code, tc.code, tc.want)
			}
			if tc.name == "tail" && strings.Contains(out.String(), "line-4\n") {
				t.Fatal("more than 20 output lines reported")
			}
		})
	}
}

func TestRunNoCommands(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-doc", fixture(t, "# no commands\n")}, &out); code != 1 || !strings.Contains(out.String(), "checked=0 unchecked=0 failed=0") {
		t.Fatalf("code=%d output=%s", code, &out)
	}
}
