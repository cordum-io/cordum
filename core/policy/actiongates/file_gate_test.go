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


// TestFileGate_DenyLeadingSlashDriveLetterBypass closes the "/c:/..." bypass:
// canonicalization can yield a leading-slash drive spelling that stripDriveLetter
// previously left intact, so the Windows-hive and home-credential matchers missed
// it (fail-OPEN). These DENYs exercise the stripDriveLetter leading-slash fix —
// basenames are kept innocuous so the dir/hive matchers (not matchCredentialBasename)
// are what fire. The bare-drive case and the benign project path are guards.
func TestFileGate_DenyLeadingSlashDriveLetterBypass(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "slash_drive_sam", path: "/c:/windows/system32/config/sam", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "slash_drive_security_hive", path: "/c:/windows/system32/config/security", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "slash_drive_aws_dir", path: "/c:/users/alice/.aws/region.txt", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "user_aws_creds"},
		{name: "bare_drive_sam_guard", path: "c:/windows/system32/config/sam", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "slash_drive_project_allowed", path: "/c:/projects/app/main.go", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_DenyTrailingDotSpaceTraversalBypass closes the ".. "/". " bypass:
// hasTraversalSegment misses a dot-segment carrying a trailing space (".. ", ". "),
// and the old per-segment trim skipped any segment that trimmed to empty, so a
// literal ".. " survived path.Clean and evaded the matchers — yet Windows opens
// ".. " as ".." (traversal). The fix strips only the trailing spaces so path.Clean
// collapses the token. The interior-dot case is an over-block guard (must ALLOW).
func TestFileGate_DenyTrailingDotSpaceTraversalBypass(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "dotspace_traversal_to_shadow", path: "/workspace/.. /.. /etc/shadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "sensitive_path"},
		{name: "dotspace_traversal_to_sam", path: "c:/windows/system32/config/x/.. /sam", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "single_dotspace_to_shadow", path: "/etc/. /shadow", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "sensitive_path"},
		{name: "interior_dot_allowed", path: "/workspace/v1.0/main.go", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
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

// TestFileGate_DenyWindowsHiveTransactionLogs covers the registry hive
// write-ahead transaction logs (.LOG/.LOG1/.LOG2), the *.sav startup copies and
// the CLFS transactional backing files ({guid}.TM.blf / *.TMContainer*.regtrans-ms).
// These siblings carry the SAME credential-bearing data as the live hive and are
// a known SAM-dump alternative when the hive file is locked, so they must DENY
// for read AND write, drive-letter-agnostically and in both slash spellings.
func TestFileGate_DenyWindowsHiveTransactionLogs(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		// SAM transaction logs + startup copy.
		{name: "sam_log", path: `C:\Windows\System32\config\SAM.LOG`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "sam_log1", path: `C:\Windows\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "sam_log2_write", path: `C:\Windows\System32\config\SAM.LOG2`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "sam_sav", path: `C:\Windows\System32\config\SAM.sav`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		// SECURITY hive siblings.
		{name: "security_log1", path: `C:\Windows\System32\config\SECURITY.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "security_log2", path: `C:\Windows\System32\config\SECURITY.LOG2`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "security_sav", path: `C:\Windows\System32\config\SECURITY.sav`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		// SYSTEM hive siblings.
		{name: "system_log1", path: `C:\Windows\System32\config\SYSTEM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "system_log2", path: `C:\Windows\System32\config\SYSTEM.LOG2`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "system_sav", path: `C:\Windows\System32\config\SYSTEM.sav`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		// SOFTWARE hive siblings.
		{name: "software_log1", path: `C:\Windows\System32\config\SOFTWARE.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		{name: "software_log2", path: `C:\Windows\System32\config\SOFTWARE.LOG2`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		{name: "software_sav", path: `C:\Windows\System32\config\SOFTWARE.sav`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		// Drive-relative, other-drive, forward-slash, RegBack and UNC spellings.
		{name: "drive_relative_system_log1", path: `\Windows\System32\config\SYSTEM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "other_drive_sam_log1", path: `D:\Windows\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "forward_slash_lower_sam_log1", path: "/windows/system32/config/sam.log1", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "regback_sam_log1", path: `C:\Windows\System32\config\RegBack\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "unc_long_prefix_sam_log1", path: `\\?\C:\Windows\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "systemroot_env_sam_log1", path: `%SystemRoot%\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		// CLFS transactional backing files (guarded, strictly config-scoped).
		{name: "clfs_tm_blf", path: `C:\Windows\System32\config\{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.TM.blf`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_registry_clfs"},
		{name: "clfs_tmcontainer_regtrans", path: `C:\Windows\System32\config\TxR\{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.TMContainer00000000000000000001.regtrans-ms`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_registry_clfs"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_AllowHiveLookalikesNotOverBlocked pins that the extended hive
// matcher does NOT over-block legitimate non-hive files under System32\config:
// the segment anchoring requires the .log/.sav suffix on the EXACT hive word, so
// software_*/system*/sam* lookalikes and the systemprofile user-profile dir stay
// ALLOW (epic rail #5 / over-refusal guard).
func TestFileGate_AllowHiveLookalikesNotOverBlocked(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "config_systemprofile_desktop", path: `C:\Windows\System32\config\systemprofile\Desktop\x.txt`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "config_software_report_txt", path: `C:\Windows\System32\config\software_report.txt`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "config_systeminfo_log", path: `C:\Windows\System32\config\systeminfo.log`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "config_samples_readme", path: `C:\Windows\System32\config\samples\readme.md`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "config_softwarelist_json", path: `C:\Windows\System32\config\softwarelist.json`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		// CLFS scoping guard: identically-named CLFS files OUTSIDE System32\config
		// (generic transactional logs) must NOT be over-blocked.
		{name: "clfs_outside_config_temp", path: `C:\Temp\app.TM.blf`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "clfs_under_system32_not_config", path: `C:\Windows\System32\drivers\x.TMContainer00000000000000000001.regtrans-ms`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_DenyWindowsHiveAlternateRoots pins that the registry credential
// hives are denied under ALTERNATE top-level Windows roots, not just the live
// %SystemRoot%. `Windows.old` is created by every in-place Windows upgrade and is
// the classic OFFLINE SAM-dump location (the entire previous install, readable
// without locking the live hive); `$WINDOWS.~BT` / `$WINDOWS.~WS` are the upgrade
// staging/working roots. All carry credential-equivalent copies of SAM / SYSTEM /
// SECURITY / SOFTWARE (plus their RegBack, .LOG/.sav transaction logs and CLFS
// backing files), so they must DENY for read AND write, drive-letter-agnostically
// and in both slash spellings — exactly like the live root (this composes with
// task-fcf2da8f's sibling axis). NOTE: a real `$WINDOWS.~BT` path canonicalizes to
// `_env_windows_.~bt` because canonicalizeFilePath's POSIX env expansion rewrites
// the `$WINDOWS` token; these real-path cases are the drift guard for that
// placeholder (see windowsHiveRe's comment in file_gate.go).
func TestFileGate_DenyWindowsHiveAlternateRoots(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		// Windows.old — previous-install backup (offline SAM-dump location).
		{name: "windows_old_sam", path: `C:\Windows.old\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "windows_old_security", path: `C:\Windows.old\System32\config\SECURITY`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "windows_old_system", path: `C:\Windows.old\System32\config\SYSTEM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "windows_old_software_write", path: `C:\Windows.old\System32\config\SOFTWARE`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		{name: "windows_old_regback_sam", path: `C:\Windows.old\System32\config\RegBack\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "windows_old_sam_log1", path: `C:\Windows.old\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "windows_old_sam_sav", path: `C:\Windows.old\System32\config\SAM.sav`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "windows_old_clfs_blf", path: `C:\Windows.old\System32\config\{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.TM.blf`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_registry_clfs"},
		{name: "windows_old_clfs_regtrans", path: `C:\Windows.old\System32\config\TxR\{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.TMContainer00000000000000000001.regtrans-ms`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_registry_clfs"},
		{name: "windows_old_drive_relative", path: `\Windows.old\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "windows_old_forward_slash_lower", path: "/windows.old/system32/config/sam", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "windows_old_other_drive_log1", path: `D:\Windows.old\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		// $WINDOWS.~BT / $WINDOWS.~WS — upgrade staging/working roots. Real paths;
		// the POSIX-env canon rewrites $WINDOWS -> _env_windows_ (drift guard).
		{name: "staging_bt_sam", path: `C:\$WINDOWS.~BT\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "staging_bt_security_write", path: `C:\$WINDOWS.~BT\System32\config\SECURITY`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "staging_bt_sam_log1", path: `C:\$WINDOWS.~BT\System32\config\SAM.LOG1`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "staging_ws_sam", path: `C:\$WINDOWS.~WS\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_AllowAlternateRootLookalikesNotOverBlocked pins that relaxing the
// hive anchor to the alternate roots does NOT over-block: only the EXACT roots
// `windows`, `windows.old` and `$WINDOWS.~BT/~WS` match, and only at the TOP level
// (courtesy of the `^/` anchor after stripDriveLetter). A non-root NESTED `windows`
// dir, a hyphenated `windows-old`, a `windowsfoo` prefix and arbitrary `*.old`
// dirs all stay ALLOW — as do hive-lookalike files and the systemprofile user dir
// UNDER an alternate root (the suffix/segment anchoring is preserved).
func TestFileGate_AllowAlternateRootLookalikesNotOverBlocked(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "nested_windows_not_root", path: `C:\myproj\windows\system32\config\sam`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "windows_hyphen_old", path: "/windows-old/system32/config/sam", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "windowsfoo_prefix", path: "/windowsfoo/system32/config/sam", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "backup_old_textfile", path: "/backup.old/x.txt", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "data_old_notes", path: "/data.old/notes.txt", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "windows_old_software_report_lookalike", path: `C:\Windows.old\System32\config\software_report.txt`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "windows_old_systemprofile_dir", path: `C:\Windows.old\System32\config\systemprofile\Desktop\x.txt`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
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

// TestFileGate_DenyWindowsHiveDriveRelativeSpellings covers the fail-open
// reported for the Windows registry credential hives: canonicalizeFilePath
// lowercases and forward-slashes but never prepends a drive letter, so the
// drive-relative (`\Windows\...`), forward-slash (`/windows/...`) and
// other-drive (`D:\Windows\...`) spellings of SAM/SYSTEM/SECURITY/SOFTWARE
// must all DENY. The `C:\`/`%SYSTEMROOT%` spellings (covered elsewhere) keep
// the drive and masked this gap.
func TestFileGate_DenyWindowsHiveDriveRelativeSpellings(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "drive_relative_sam_read", path: `\Windows\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "drive_relative_sam_write", path: `\WINDOWS\System32\config\Sam`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "drive_relative_system_read", path: `\Windows\System32\config\SYSTEM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "drive_relative_security_read", path: `\Windows\System32\config\SECURITY`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "drive_relative_software_read", path: `\Windows\System32\config\SOFTWARE`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		{name: "forward_slash_sam_read", path: `/Windows/System32/config/SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "forward_slash_system_write", path: `/windows/system32/config/system`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "other_drive_sam_read", path: `D:\Windows\System32\config\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_DenyDotenvVariants covers the fail-open for `.env.<env>` secret
// files: credentialExact previously listed only ".env", so `.env.local` /
// `.env.production` / `.env.staging` (read AND write) fell through to ALLOW
// even though they carry real secrets. Mirrors core/edge/classifier.go
// matchesEnvSecretFile (EDGE-064).
func TestFileGate_DenyDotenvVariants(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "env_local_read", path: "/workspace/.env.local", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_local_write", path: "/workspace/.env.local", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_production_read", path: "/srv/app/.env.production", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_production_write", path: "/srv/app/.env.production", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_staging_read", path: "/repo/.env.staging", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_windows_backslash_read", path: `C:\Users\bob\app\.env.production`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_plain_read", path: "/workspace/.env", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_AllowDotenvTemplates is the over-block guard: the conventional
// non-secret .env template spellings must stay ALLOW (read AND write), in
// lockstep with the exclusions in core/edge/classifier.go matchesEnvSecretFile.
func TestFileGate_AllowDotenvTemplates(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "env_example_read", path: "/workspace/.env.example", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "env_example_write", path: "/workspace/.env.example", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "env_sample_read", path: "/workspace/.env.sample", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "env_template_read", path: "/workspace/.env.template", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "env_dist_read", path: "/workspace/.env.dist", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "env_defaults_read", path: "/workspace/.env.defaults", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_DenyWindowsHiveAllSpellings broadens hive coverage to RegBack
// copies, mixed separators, %SYSTEMROOT%/%WINDIR% expansion and other drives —
// every spelling must DENY for read AND write.
func TestFileGate_DenyWindowsHiveAllSpellings(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "regback_sam_read", path: `C:\Windows\System32\config\RegBack\SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "regback_system_fwdslash", path: `/windows/system32/config/regback/system`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_system_hive"},
		{name: "mixed_separators_sam", path: `C:\Windows/System32\config/SAM`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "systemroot_env_security", path: `%SYSTEMROOT%\System32\config\SECURITY`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "windir_env_software_write", path: `%WINDIR%\System32\config\SOFTWARE`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		{name: "security_drive_relative_write", path: `\Windows\System32\config\SECURITY`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_security_hive"},
		{name: "software_other_drive_write", path: `E:\Windows\System32\config\SOFTWARE`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
		{name: "unc_long_prefix_software", path: `\\?\C:\Windows\System32\config\SOFTWARE`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_software_hive"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_DenyOtherDriveUserCredentialDir covers the same-root-cause
// fail-open the sweep found in matchHomeUserCredentialDir: a Windows user
// profile on a NON-C drive (or the drive-relative spelling) whose credential
// file basename is not itself a known credential name must still DENY.
func TestFileGate_DenyOtherDriveUserCredentialDir(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "d_drive_aws_credentials", path: `D:\Users\bob\.aws\credentials`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "user_aws_creds"},
		{name: "e_drive_kube_config", path: `E:\Users\alice\.kube\config`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "user_kube_config"},
		{name: "drive_relative_docker_config", path: `\Users\bob\.docker\config.json`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "user_docker_config"},
		{name: "macos_ssh_known_hosts", path: "/Users/dev/.ssh/known_hosts", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "user_ssh"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_DenyDotenvFamilyExtra covers more .env.<env> variants including
// case-folding, a Windows path and a double suffix.
func TestFileGate_DenyDotenvFamilyExtra(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "env_development", path: "/app/.env.development", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_test_write", path: "/app/.env.test", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_uppercase_folds", path: `/app/.ENV.LOCAL`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_double_suffix", path: "/app/.env.production.local", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_windows_path", path: `C:\repo\.env.staging`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// TestFileGate_AllowNonSecretLookalikes is the over-block guard: paths that
// merely resemble a hive or a .env secret (the `system` hive name is a prefix
// of the systemprofile dir; `.environment`/`*.env` are not dotenv secret files)
// must stay ALLOW.
func TestFileGate_AllowNonSecretLookalikes(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "systemprofile_not_hive", path: `C:\Windows\System32\config\systemprofile\AppData\Local\x.txt`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "softwaredistribution_not_hive", path: `C:\Windows\SoftwareDistribution\Download\x.cab`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "system32_binary_not_hive", path: `C:\Windows\System32\notepad.exe`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "dot_environment_not_dotenv", path: "/workspace/.environment", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "app_env_extension", path: "/workspace/myapp.env", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "prod_env_extension", path: "/workspace/prod.env", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "project_config_dir", path: "/workspace/config/system.yaml", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

// classifierEnvSecretRef mirrors core/edge/classifier.go matchesEnvSecretFile
// (EDGE-064) so the lockstep test can assert FileGate and the Edge classifier
// agree on the .env family WITHOUT importing the unexported edge helper (which
// would also be an import cycle: actiongates already imports core/edge). KEEP
// IN LOCKSTEP with classifier.go if its rules change.
func classifierEnvSecretRef(path string) bool {
	padded := "/" + strings.TrimPrefix(path, "/")
	if !strings.Contains(padded, "/.env") {
		return false
	}
	for _, suffix := range []string{".example", ".sample", ".template", ".dist", ".defaults"} {
		if strings.HasSuffix(padded, "/.env"+suffix) {
			return false
		}
	}
	return true
}

// TestFileGate_EnvFamily_LockstepWithEdgeClassifier asserts that for every
// spelling in the canonical .env family, FileGate's DENY verdict matches the
// Edge classifier's secret verdict (read AND write). This pins the two
// secret-file definitions together so they cannot silently re-diverge.
func TestFileGate_EnvFamily_LockstepWithEdgeClassifier(t *testing.T) {
	t.Parallel()
	bases := []string{
		".env", ".env.local", ".env.production", ".env.staging",
		".env.development", ".env.test",
		".env.example", ".env.sample", ".env.template", ".env.dist", ".env.defaults",
	}
	for _, base := range bases {
		for _, verb := range []config.ActionVerb{config.ActionVerbRead, config.ActionVerbWrite} {
			path := "/workspace/" + base
			wantSecret := classifierEnvSecretRef(path)
			gate := NewFileGate()
			dec := gate.Evaluate(context.Background(), &config.PolicyInput{
				Tenant: "tnt_a",
				Action: &config.ActionDescriptor{Kind: config.ActionKindFile, Verb: verb, TargetPath: path},
			})
			gotDeny := dec.Decision == pb.DecisionType_DECISION_TYPE_DENY
			if gotDeny != wantSecret {
				t.Fatalf("lockstep mismatch for %q verb=%v: FileGate deny=%v, classifier secret=%v", base, verb, gotDeny, wantSecret)
			}
		}
	}
}

func TestStripDriveLetter(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"c:/windows/system32/config/sam", "/windows/system32/config/sam"},
		{"d:/users/bob/.aws/credentials", "/users/bob/.aws/credentials"},
		{"/windows/system32/config/sam", "/windows/system32/config/sam"},
		{"relative/path", "relative/path"},
		{"c:", ""},
	}
	for _, tc := range cases {
		if got := stripDriveLetter(tc.in); got != tc.want {
			t.Errorf("stripDriveLetter(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMatchWindowsRegistryHive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in         string
		wantReason string
		wantHit    bool
	}{
		{"/windows/system32/config/sam", "sensitive_path:windows_sam", true},
		{"c:/windows/system32/config/system", "sensitive_path:windows_system_hive", true},
		{"d:/windows/system32/config/security", "sensitive_path:windows_security_hive", true},
		{"/windows/system32/config/software", "sensitive_path:windows_software_hive", true},
		{"/windows/system32/config/regback/sam", "sensitive_path:windows_sam", true},
		// Hive write-ahead transaction logs + startup backups carry the same
		// secrets as the live hive (task-fcf2da8f). `software.log` is the
		// SOFTWARE hive's legacy single-log file, so it now DENIES as the
		// software hive — this intentionally reverses the parent task's pre-fix
		// under-block (which had filed this very follow-up).
		{"/windows/system32/config/software.log", "sensitive_path:windows_software_hive", true},
		{"/windows/system32/config/sam.log1", "sensitive_path:windows_sam", true},
		{"/windows/system32/config/sam.log2", "sensitive_path:windows_sam", true},
		{"/windows/system32/config/sam.sav", "sensitive_path:windows_sam", true},
		{"/windows/system32/config/regback/sam.log1", "sensitive_path:windows_sam", true},
		// CLFS transactional backing files (config-scoped) → dedicated reason.
		{"/windows/system32/config/{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.tm.blf", "sensitive_path:windows_registry_clfs", true},
		{"/windows/system32/config/txr/{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.tmcontainer00000000000000000001.regtrans-ms", "sensitive_path:windows_registry_clfs", true},
		// Over-block guards: the optional .log/.sav suffix must bind to the
		// EXACT hive word, so lookalikes and the systemprofile user dir stay ALLOW.
		{"/windows/system32/config/systemprofile/x", "", false},
		{"/windows/system32/config/software_report.txt", "", false},
		{"/windows/system32/config/systeminfo.log", "", false},
		{"/windows/system32/config/softwarelist.json", "", false},
		// Alternate top-level Windows roots (task-7b7249a3): Windows.old (offline
		// in-place-upgrade backup) + the $WINDOWS.~BT/~WS staging roots. canon strips
		// the drive and rewrites the $WINDOWS env token to the _env_windows_
		// placeholder, so the matcher sees _env_windows_.~bt / _env_windows_.~ws.
		{"/windows.old/system32/config/sam", "sensitive_path:windows_sam", true},
		{"/windows.old/system32/config/security", "sensitive_path:windows_security_hive", true},
		{"/windows.old/system32/config/regback/sam", "sensitive_path:windows_sam", true},
		{"/windows.old/system32/config/sam.log1", "sensitive_path:windows_sam", true},
		{"/windows.old/system32/config/sam.sav", "sensitive_path:windows_sam", true},
		{"/windows.old/system32/config/{016888cd-6c6f-11de-8d1d-001e0bcde3ec}.tm.blf", "sensitive_path:windows_registry_clfs", true},
		{"/_env_windows_.~bt/system32/config/sam", "sensitive_path:windows_sam", true},
		{"/_env_windows_.~ws/system32/config/system", "sensitive_path:windows_system_hive", true},
		// Alt-root over-block guards: only the exact top-level roots match.
		{"/myproj/windows/system32/config/sam", "", false},
		{"/windows-old/system32/config/sam", "", false},
		{"/windowsfoo/system32/config/sam", "", false},
		{"/backup.old/x.txt", "", false},
		{"/windows.old/system32/config/software_report.txt", "", false},
		{"/etc/shadow", "", false},
	}
	for _, tc := range cases {
		reason, hit := matchWindowsRegistryHive(tc.in)
		if hit != tc.wantHit || reason != tc.wantReason {
			t.Errorf("matchWindowsRegistryHive(%q) = (%q,%v), want (%q,%v)", tc.in, reason, hit, tc.wantReason, tc.wantHit)
		}
	}
}

func TestMatchEnvCredential(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base       string
		wantReason string
		wantHit    bool
	}{
		{".env", "credential:dotenv", true},
		{".env.local", "credential:dotenv", true},
		{".env.production", "credential:dotenv", true},
		{".env.production.local", "credential:dotenv", true},
		{".env.example", "", false},
		{".env.sample", "", false},
		{".env.template", "", false},
		{".env.dist", "", false},
		{".env.defaults", "", false},
		{".environment", "", false},
		{".envrc", "", false},
		{"myapp.env", "", false},
		{"env", "", false},
	}
	for _, tc := range cases {
		reason, hit := matchEnvCredential(tc.base)
		if hit != tc.wantHit || reason != tc.wantReason {
			t.Errorf("matchEnvCredential(%q) = (%q,%v), want (%q,%v)", tc.base, reason, hit, tc.wantReason, tc.wantHit)
		}
	}
}

// TestFileGate_DenyTrailingDotSpaceSpellings covers the Windows trailing
// dot/space bypass: a trailing '.' or ' ' on a component is ignored on open, so
// these spellings reach the same hive / credential file and must DENY. Legit
// names that merely trim to a non-secret stay ALLOW.
func TestFileGate_DenyTrailingDotSpaceSpellings(t *testing.T) {
	t.Parallel()
	cases := []fileGateCase{
		{name: "sam_trailing_dot", path: `C:\Windows\System32\config\SAM.`, verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "sam_trailing_space_drive_relative", path: "\\Windows\\System32\\config\\SAM ", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "config_dir_trailing_dot", path: `C:\Windows\System32\config.\SAM`, verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "windows_sam"},
		{name: "env_trailing_space", path: "/workspace/.env ", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "env_local_trailing_space", path: "/workspace/.env.local ", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		{name: "id_rsa_trailing_space", path: "/workspace/id_rsa ", verb: config.ActionVerbRead, wantDecision: pb.DecisionType_DECISION_TYPE_DENY, wantCode: CodeAccessDenied, subReasonHas: "credential"},
		// Over-block guard: trimming only affects matching; legit files stay ALLOW.
		{name: "report_trailing_dot_allowed", path: "/workspace/report.txt.", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
		{name: "notes_trailing_space_allowed", path: "/workspace/notes ", verb: config.ActionVerbWrite, wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runFileGate(t, tc) })
	}
}

func TestTrimTrailingDotSpaceSegments(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"c:/windows/system32/config/sam.", "c:/windows/system32/config/sam"},
		{"/workspace/.env.local ", "/workspace/.env.local"},
		{"c:/windows/system32/config./sam", "c:/windows/system32/config/sam"},
		{"/workspace/report.txt.", "/workspace/report.txt"},
		{"/workspace/notes ", "/workspace/notes"},
		{"/a/b", "/a/b"},
		{"/a/..", "/a/.."},
		{"/a/.", "/a/."},
	}
	for _, tc := range cases {
		if got := trimTrailingDotSpaceSegments(tc.in); got != tc.want {
			t.Errorf("trimTrailingDotSpaceSegments(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
