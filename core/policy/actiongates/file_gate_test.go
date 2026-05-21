package actiongates

import (
	"context"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// fileGateCase drives a single FileGate.Evaluate invocation. The test runner
// builds a PolicyInput from {path, verb, args}, runs FileGate, and asserts
// against expected decision + sub_reason.
type fileGateCase struct {
	name         string
	path         string
	verb         config.ActionVerb
	wantDecision pb.DecisionType
	wantCode     string
	subReasonHas string // substring match on SubReason; "" skips
	args         map[string]any
}

func runFileGate(t *testing.T, tc fileGateCase) {
	t.Helper()
	gate := NewFileGate()
	in := &config.PolicyInput{
		Tenant: "tnt_a",
		Action: &config.ActionDescriptor{
			Kind:       config.ActionKindFile,
			Verb:       tc.verb,
			TargetPath: tc.path,
			Args:       tc.args,
		},
	}
	dec := gate.Evaluate(context.Background(), in)
	if dec.Decision != tc.wantDecision {
		t.Fatalf("decision = %v, want %v (path=%q verb=%q reason=%q subReason=%q)", dec.Decision, tc.wantDecision, tc.path, tc.verb, dec.Reason, dec.SubReason)
	}
	if tc.wantCode != "" && dec.Code != tc.wantCode {
		t.Fatalf("code = %q, want %q", dec.Code, tc.wantCode)
	}
	if tc.subReasonHas != "" && !strings.Contains(dec.SubReason, tc.subReasonHas) {
		t.Fatalf("subReason = %q, want substring %q", dec.SubReason, tc.subReasonHas)
	}
}

func TestFileGate_SkipsNonFileKind(t *testing.T) {
	t.Parallel()
	gate := NewFileGate()

	// nil action -> ALLOW (skip)
	dec := gate.Evaluate(context.Background(), &config.PolicyInput{})
	if dec.Fired() {
		t.Fatalf("nil action: gate fired, want zero decision (Decision=%v Reason=%q)", dec.Decision, dec.Reason)
	}

	// non-file kind -> ALLOW (skip)
	dec = gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindURL, TargetURL: "https://example.com"},
	})
	if dec.Fired() {
		t.Fatalf("url kind: gate fired, want zero decision")
	}
}

func TestFileGate_DenySensitiveUnixRead(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "shadow_write", path: "/etc/shadow", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "sensitive_path"},
		{name: "shadow_read", path: "/etc/shadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "sensitive_path"},
		{name: "ssh_id_rsa_read", path: "/root/.ssh/id_rsa", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "ssh_id_ed25519", path: "/root/.ssh/id_ed25519", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "root_aws_creds", path: "/root/.aws/credentials", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "home_user_aws_creds", path: "/home/alice/.aws/credentials", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "var_lib_secrets", path: "/var/lib/secrets/foo.key", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "proc_self_environ", path: "/proc/self/environ", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "etc_passwd_write", path: "/etc/passwd", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "sensitive_path"},
		{name: "etc_passwd_read_allowed", path: "/etc/passwd", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

func TestFileGate_DenyWindowsSensitivePaths(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "system32_sam", path: `C:\Windows\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "system32_sam_lower", path: `c:\windows\system32\config\sam`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "userprofile_aws_creds_lit", path: `C:\Users\bob\.aws\credentials`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "userprofile_env_aws_creds", path: `%USERPROFILE%\.aws\credentials`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "userprofile_env_ssh", path: `%USERPROFILE%\.ssh\id_rsa`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "ssh_id_rsa_backslash", path: `D:\projects\.ssh\id_rsa`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "unc_long_prefix", path: `\\?\C:\Windows\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "unc_dos_namespace", path: `\\.\PhysicalDrive0`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "device"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

func TestFileGate_DenyTraversal(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "double_dot_unix", path: "../../etc/passwd", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "traversal"},
		{name: "double_dot_windows", path: `..\..\Windows\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "traversal"},
		{name: "url_encoded_slash", path: "..%2f..%2fetc%2fshadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "traversal"},
		{name: "url_encoded_backslash", path: "..%5c..%5cWindows%5cSystem32", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "traversal"},
		{name: "double_encoded_slash", path: "..%252f..%252fetc%252fshadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "traversal"},
		{name: "mixed_encoding", path: "..%2f..\\etc/shadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "traversal"},
		{name: "null_byte_truncation", path: "/etc/passwd%00.txt", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "null_byte"},
		{name: "leading_traversal_hidden", path: "/workspace/../../../etc/shadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
		{name: "home_tilde_escape", path: "~/../../etc/shadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

func TestFileGate_DenyCredentialPatterns(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "pem_anywhere", path: "/workspace/secret.pem", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "key_anywhere", path: "/tmp/myapp.key", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "id_rsa_bare", path: "/workspace/id_rsa", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "id_ed25519_bare", path: "/workspace/id_ed25519", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "npmrc", path: "/build/.npmrc", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "netrc", path: "/home/alice/.netrc", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "dotenv", path: "/workspace/.env", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "credentials_json", path: "/workspace/credentials.json", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "service_account_json", path: "/workspace/service_account-foo.json", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "kdbx", path: "/var/data/store.kdbx", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "pgp", path: "/tmp/foo.pgp", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "gpg", path: "/tmp/foo.gpg", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "keystore_jks", path: "/srv/keystore.jks", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_AllowLegitimateCodingPaths covers ≥5 over-refusal `coding`
// cases (per epic rail #5 and DoD #1). These paths must NEVER deny.
func TestFileGate_AllowLegitimateCodingPaths(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "tmp_build_output", path: "/tmp/build-output.txt", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "workspace_src_go", path: "/workspace/src/foo.go", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "workspace_src_go_write", path: "/workspace/src/foo.go", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "windows_user_projects", path: `C:\Users\u\Projects\foo.txt`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "windows_user_projects_read", path: `C:\Users\u\Projects\subdir\bar.md`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "macos_repo", path: "/Users/dev/repo/src/main.py", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "monorepo_package_json", path: "/workspace/repo/package.json", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "go_module_file", path: "/workspace/repo/go.mod", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "readme", path: "/workspace/README.md", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "test_fixture", path: "/workspace/test/fixtures/payload.json", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

func TestFileGate_EmptyPathSkips(t *testing.T) {
	t.Parallel()
	gate := NewFileGate()
	dec := gate.Evaluate(context.Background(), &config.PolicyInput{
		Action: &config.ActionDescriptor{Kind: config.ActionKindFile, Verb: config.ActionVerbWrite, TargetPath: ""},
	})
	if dec.Fired() {
		t.Fatalf("empty path: gate fired, want skip (Decision=%v)", dec.Decision)
	}
}
