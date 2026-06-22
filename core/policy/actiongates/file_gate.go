package actiongates

import (
	"context"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// FileGate enforces filesystem-level read/write access. It canonicalizes the
// requested path (URL-decode, env-expand, separator-normalize) and matches
// against three rule families:
//
//  1. Traversal indicators (`..` segments, null bytes) -> always DENY.
//  2. Sensitive paths (OS credential stores, OS config, /proc secrets, etc.) ->
//     DENY by verb (always or write-only depending on entry).
//  3. Credential-file patterns (id_rsa, *.pem, *.kdbx, ...) -> DENY regardless
//     of directory.
//
// Gate is stateless. NewFileGate returns a singleton suitable for
// long-lived reuse.
type FileGate struct{}

// NewFileGate returns a fresh FileGate. Construction is cheap; callers may
// build per-request without performance concerns.
func NewFileGate() *FileGate { return &FileGate{} }

func (g *FileGate) ID() string { return GateIDFile }

// Evaluate returns a non-zero decision only when the requested action is a
// file kind AND a non-empty TargetPath triggers one of the sensitive/traversal
// rules. Everything else (nil action, other kinds, empty path, legitimate
// workspace paths) returns the zero decision so the pipeline continues.
func (g *FileGate) Evaluate(_ context.Context, in *config.PolicyInput) ActionGateDecision {
	if in == nil || in.Action == nil {
		return ActionGateDecision{}
	}
	act := in.Action
	if act.Kind != config.ActionKindFile {
		return ActionGateDecision{}
	}
	raw := strings.TrimSpace(act.TargetPath)
	if raw == "" {
		return ActionGateDecision{}
	}

	// 1) Null byte (pre-decode) — these truncate parsing in many libs.
	if strings.ContainsRune(raw, '\x00') || containsEncodedNull(raw) {
		return g.deny(CodeAccessDenied, "filesystem access denied", "null_byte", raw)
	}

	// 2) Iteratively URL-decode (cap iterations to avoid pathological inputs).
	decoded := raw
	for i := 0; i < 3; i++ {
		u, err := url.QueryUnescape(decoded)
		if err != nil || u == decoded {
			break
		}
		decoded = u
	}

	// 3) Traversal detection works on the decoded but raw-separator form.
	if hasTraversalSegment(decoded) {
		return g.deny(CodeAccessDenied, "filesystem traversal denied", "traversal", raw)
	}

	// 4) DOS device namespace (`\\.\PhysicalDrive0`, `\\.\PIPE\foo`) — detect
	// BEFORE canonicalization because path.Clean collapses the `\\.` marker.
	if isDOSDeviceNamespace(decoded) {
		return g.deny(CodeAccessDenied, "device access denied", "device_namespace", raw)
	}

	// 5) Canonical lowercase form with forward slashes for matching.
	canon := canonicalizeFilePath(decoded)

	// 6) Basename credential patterns first (cheap and dir-independent).
	if reason, hit := matchCredentialBasename(canon); hit {
		return g.deny(CodeAccessDenied, "credential file access denied", reason, raw)
	}

	// 7) Sensitive path matrix.
	writeRequested := isWriteVerb(act.Verb)
	if reason, hit := matchSensitivePath(canon, writeRequested); hit {
		return g.deny(CodeAccessDenied, "filesystem access denied", reason, raw)
	}

	// Nothing matched — pass.
	return ActionGateDecision{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, GateID: GateIDFile}
}

func (g *FileGate) deny(code, reason, subReason, raw string) ActionGateDecision {
	return ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		GateID:    GateIDFile,
		Code:      code,
		Reason:    reason,
		SubReason: subReason,
		Extra: map[string]string{
			"gate":       GateIDFile,
			"sub_reason": subReason,
			// raw target_path intentionally NOT echoed to clients (PII / token leak risk).
			// Length-only breadcrumb for SIEM cardinality bounds.
			"target_len": itoa(len(raw)),
		},
	}
}

// isDOSDeviceNamespace detects Windows DOS device path prefixes (`\\.\` and
// `\\?\DOSDEVICE`). The long-path UNC prefix `\\?\C:\...` itself is fine and
// is stripped during canonicalization; we trip only when the rest is a device
// rather than a drive letter.
func isDOSDeviceNamespace(s string) bool {
	n := strings.ReplaceAll(s, `\`, "/")
	if !strings.HasPrefix(n, "//./") && !strings.HasPrefix(n, "//.") {
		return false
	}
	rest := strings.TrimPrefix(n, "//./")
	rest = strings.TrimPrefix(rest, "//.")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return true
	}
	// Heuristic: device entries do not contain `:` (drive letters live behind \\?\ instead).
	return !strings.Contains(rest, ":")
}

// containsEncodedNull catches %00 (single-encoded) and %2500 (double-encoded
// %00). We do not iterate decode here because a downstream filesystem call
// might decode just once and stop, leaving the null active.
func containsEncodedNull(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "%00") || strings.Contains(lower, "%2500")
}

// hasTraversalSegment runs on the post-URL-decode form. We replace backslashes
// with forward slashes for analysis, then split. Any segment equal to ".." is
// a traversal attempt. This also catches `..` appearing after tilde or env
// expansion, since we apply this check before canonicalization.
func hasTraversalSegment(s string) bool {
	normalized := strings.ReplaceAll(s, `\`, "/")
	// Reject mid-string ".." even when not at a clean segment boundary, since
	// percent-encoded mixes can hide them (../../foo).
	if strings.Contains(normalized, "/../") || strings.HasSuffix(normalized, "/..") || strings.HasPrefix(normalized, "../") || normalized == ".." {
		return true
	}
	// Tilde with traversal — `~/../etc` is still traversal even before tilde-expand.
	if strings.HasPrefix(normalized, "~/") && strings.Contains(normalized[2:], "..") {
		return true
	}
	return false
}

// canonicalizeFilePath produces the form used for sensitive-path matching:
//
//   - URL-decoded already (caller).
//   - %ENV% and $ENV/${ENV} expanded to placeholders by lowercase name.
//   - Tilde expanded to /home/_user_/ for matching only.
//   - UNC long-path prefix stripped (`\\?\` and `\\.\` are inspected separately
//     by the device check above).
//   - Backslashes -> forward slashes.
//   - Lowercased.
//   - path.Clean applied.
func canonicalizeFilePath(s string) string {
	// %ENV%  -> /env/name/ style placeholder so we can match userprofile_*
	s = expandWindowsEnv(s)
	// $ENV / ${ENV} -> placeholder
	s = expandPosixEnv(s)
	// Strip UNC \\?\ long-path prefix; leave \\.\ in for device detection.
	if strings.HasPrefix(s, `\\?\`) || strings.HasPrefix(s, `//?/`) {
		s = strings.TrimPrefix(s, `\\?\`)
		s = strings.TrimPrefix(s, `//?/`)
	}
	// Backslashes -> forward slashes.
	s = strings.ReplaceAll(s, `\`, "/")
	// Lowercase.
	s = strings.ToLower(s)
	// Tilde expansion (post-lowercase, post-slash).
	if strings.HasPrefix(s, "~/") {
		s = "/home/_user_/" + strings.TrimPrefix(s, "~/")
	}
	// Windows ignores trailing dots/spaces on each path component when opening
	// a file (so `...\config\SAM.`, `...\config\SAM ` and `...\config.\SAM` all
	// open the SAM hive). Trim them per segment BEFORE matching so those
	// spellings can't bypass the sensitive-path/credential rules.
	s = trimTrailingDotSpaceSegments(s)
	// path.Clean to collapse repeats. Note: path.Clean('//./...') keeps the
	// device-namespace marker, which our device check needs.
	s = path.Clean(s)
	return s
}

// stripDriveLetter removes a leading Windows drive-letter prefix (e.g. "c:")
// from an already-canonicalized (lowercased, forward-slash) path. It lets the
// drive-absolute (`c:/...`), drive-relative (`/windows/...`) and other-drive
// (`d:/...`) spellings of a Windows system/user path collapse to one
// drive-agnostic form for matching. canonicalizeFilePath never prepends a
// drive, so without this the Windows-anchored rules fail OPEN on the
// drive-relative and forward-slash spellings.
func stripDriveLetter(canon string) string {
	// Account for an optional leading "/" before the drive letter:
	// canonicalization can yield both "c:/..." and "/c:/..." spellings, and the
	// latter would otherwise keep its "/c:" prefix and sail past the
	// drive-stripped matchers (matchHomeUserCredentialDir / matchWindowsRegistryHive),
	// reopening the fail-open the hardcoded "/c:/..." rows used to cover.
	offset := 0
	if strings.HasPrefix(canon, "/") {
		offset = 1
	}
	if len(canon) >= offset+2 && canon[offset+1] == ':' && canon[offset] >= 'a' && canon[offset] <= 'z' {
		return canon[offset+2:]
	}
	return canon
}

// trimTrailingDotSpaceSegments strips trailing '.' and ' ' characters from each
// '/'-separated segment, mirroring how Windows normalizes path components on
// open (a trailing dot or space is ignored). Literal "." / ".." tokens are left
// alone. A segment of ONLY dots+spaces with no trailing space (e.g. "...") is
// left untouched so path structure is preserved, but a dots+spaces segment WITH
// a trailing space (e.g. ".. " or ". ") has only its trailing spaces stripped:
// Windows opens ".. " as ".." (traversal) and ". " as ".", so path.Clean can
// then collapse the resulting token instead of leaving a literal ".. " segment
// that bypasses the sensitive-path/traversal matchers. Used only for match
// canonicalization, never for real file access, so over-trimming can only ever
// DENY more, never ALLOW more (fail-closed).
func trimTrailingDotSpaceSegments(s string) string {
	parts := strings.Split(s, "/")
	changed := false
	for i, p := range parts {
		if p == "." || p == ".." {
			continue
		}
		t := strings.TrimRight(p, ". ")
		if t == p {
			continue
		}
		if t == "" {
			// All dots/spaces (e.g. ".. ", ". "): strip only trailing spaces so
			// ".. " -> ".." and ". " -> "." get collapsed by path.Clean rather
			// than surviving as a literal segment that evades the matchers.
			stripped := strings.TrimRight(p, " ")
			if stripped == p {
				continue
			}
			parts[i] = stripped
			changed = true
			continue
		}
		parts[i] = t
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(parts, "/")
}

var windowsEnvRe = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

func expandWindowsEnv(s string) string {
	return windowsEnvRe.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.ToLower(m[1 : len(m)-1])
		switch name {
		case "userprofile":
			return `C:\Users\_user_`
		case "homepath":
			return `\Users\_user_`
		case "appdata":
			return `C:\Users\_user_\AppData\Roaming`
		case "localappdata":
			return `C:\Users\_user_\AppData\Local`
		case "programdata":
			return `C:\ProgramData`
		case "windir", "systemroot":
			return `C:\Windows`
		case "system32":
			return `C:\Windows\System32`
		}
		// Unknown env -> placeholder that can't accidentally collide with a real path.
		return `\_env_` + name + `_\`
	})
}

var posixEnvRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

func expandPosixEnv(s string) string {
	return posixEnvRe.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.ToLower(strings.TrimFunc(m, func(r rune) bool {
			return r == '$' || r == '{' || r == '}'
		}))
		switch name {
		case "home":
			return "/home/_user_"
		}
		return "/_env_" + name + "_"
	})
}

// matchSensitivePath returns (subReason, true) on a hit. The matrix is
// expressed as: (prefix, writeOnly bool, subReason). writeOnly entries fire
// only when the verb is a write/mutate. Forward-slash, lowercase canonical
// form is assumed.
var sensitivePaths = []struct {
	prefix    string
	writeOnly bool
	subReason string
}{
	// Always deny (read + write).
	{"/etc/shadow", false, "sensitive_path:etc_shadow"},
	{"/etc/sudoers", false, "sensitive_path:etc_sudoers"},
	{"/etc/master.passwd", false, "sensitive_path:bsd_master_passwd"},
	{"/etc/security/", false, "sensitive_path:etc_security"},
	{"/var/lib/secrets/", false, "sensitive_path:var_lib_secrets"},
	{"/proc/self/environ", false, "sensitive_path:proc_environ"},
	{"/proc/self/cgroup", false, "sensitive_path:proc_cgroup"},
	{"/root/.ssh/", false, "sensitive_path:ssh"},
	{"/root/.aws/", false, "sensitive_path:aws_creds"},
	{"/root/.docker/", false, "sensitive_path:docker_config"},
	{"/root/.kube/", false, "sensitive_path:kube_config"},
	// NOTE: Windows registry credential hives (SAM/SYSTEM/SECURITY/SOFTWARE)
	// are matched by matchWindowsRegistryHive, NOT here. A hardcoded `c:/`
	// prefix in this table fails OPEN on drive-relative (`\Windows\...`),
	// forward-slash (`/windows/...`) and other-drive (`d:/...`) spellings
	// because canonicalizeFilePath never prepends a drive letter.
	// Write-only (e.g. /etc/passwd is world-readable on Unix).
	{"/etc/passwd", true, "sensitive_path:etc_passwd_write"},
	{"/etc/group", true, "sensitive_path:etc_group_write"},
	{"/etc/hosts", true, "sensitive_path:etc_hosts_write"},
	{"/boot/", true, "sensitive_path:boot_write"},
	{"/sbin/", true, "sensitive_path:sbin_write"},
}

func matchSensitivePath(canon string, write bool) (string, bool) {
	// Windows registry credential hives — always deny (read + write),
	// drive-letter-agnostic (see matchWindowsRegistryHive).
	if reason, ok := matchWindowsRegistryHive(canon); ok {
		return reason, true
	}
	for _, rule := range sensitivePaths {
		if rule.writeOnly && !write {
			continue
		}
		if rule.prefix == canon || strings.HasPrefix(canon, rule.prefix) && (len(rule.prefix) == 0 || rule.prefix[len(rule.prefix)-1] == '/' || len(canon) == len(rule.prefix) || canon[len(rule.prefix)] == '/') {
			return rule.subReason, true
		}
	}
	// Wildcard /home/<any>/.aws/credentials, /home/<any>/.ssh/...
	if reason, ok := matchHomeUserCredentialDir(canon); ok {
		return reason, true
	}
	return "", false
}

// matchHomeUserCredentialDir handles /home/<user>/.aws/, /home/<user>/.ssh/,
// /users/<user>/.aws/ (mac-style after canonicalization keeps mixed-case ->
// lowercase), and C:/Users/<user>/.aws/ etc. We tolerate any single segment
// for the user.
func matchHomeUserCredentialDir(canon string) (string, bool) {
	// Strip a leading drive letter so Windows user profiles on ANY drive
	// (`c:/users/`, `d:/users/`, ...) and the drive-relative `\Users\...`
	// spelling all reduce to the `/users/` root below. A hardcoded `c:/users/`
	// row used to fail OPEN on non-C-drive profiles (e.g. D:\Users\bob\.aws\
	// credentials, whose basename is not a known credential file).
	canon = stripDriveLetter(canon)
	suspects := []struct {
		root      string
		sub       string
		subReason string
	}{
		{"/home/", "/.ssh/", "sensitive_path:user_ssh"},
		{"/home/", "/.aws/", "sensitive_path:user_aws_creds"},
		{"/home/", "/.docker/", "sensitive_path:user_docker_config"},
		{"/home/", "/.kube/", "sensitive_path:user_kube_config"},
		{"/users/", "/.ssh/", "sensitive_path:user_ssh"},
		{"/users/", "/.aws/", "sensitive_path:user_aws_creds"},
		{"/users/", "/.docker/", "sensitive_path:user_docker_config"},
		{"/users/", "/.kube/", "sensitive_path:user_kube_config"},
	}
	for _, s := range suspects {
		if !strings.HasPrefix(canon, s.root) {
			continue
		}
		rest := canon[len(s.root):]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			continue
		}
		tail := rest[slash:]
		if strings.HasPrefix(tail, s.sub) {
			return s.subReason, true
		}
	}
	return "", false
}

// windowsHiveRootAlt enumerates the EXACT credential-bearing top-level roots
// (after stripDriveLetter) that each hold a copy of the registry hives:
//   - `windows`: the live %SystemRoot%.
//   - `windows.old`: the previous install kept by every in-place Windows upgrade;
//     a classic OFFLINE SAM-dump location, readable without locking the live hive.
//   - `_env_windows_.~bt` / `_env_windows_.~ws`: the $WINDOWS.~BT / $WINDOWS.~WS
//     upgrade staging roots. canonicalizeFilePath's POSIX env expansion rewrites
//     the leading `$WINDOWS` token to the `_env_windows_` placeholder, so that is
//     the form the matcher actually sees (the real-path cases in
//     TestFileGate_DenyWindowsHiveAlternateRoots are the drift guard).
//
// EXACT enumeration (not a `windows.*` wildcard), consumed only after a leading
// `^/`, so a root matches ONLY at the top level: a nested `/x/windows/...`, a
// hyphenated `windows-old`, a `windowsfoo` prefix and arbitrary `*.old` dirs all
// stay ALLOW (TestFileGate_AllowAlternateRootLookalikesNotOverBlocked).
const windowsHiveRootAlt = `(?:windows|windows\.old|_env_windows_\.~(?:bt|ws))`

// windowsHiveRe matches the Windows registry credential hives (SAM, SYSTEM,
// SECURITY, SOFTWARE) under System32\config on any windowsHiveRootAlt root — including their
// RegBack copies AND their write-ahead transaction logs / startup backups
// (`.LOG`, `.LOG1`, `.LOG2`, `.sav`) — on a drive-stripped canonical path. The
// transaction logs and `.sav` copies carry the SAME credential-bearing data as
// the live hive (Windows replays the logs into the hive on mount), so they are
// a documented SAM-dump alternative when the live hive file is locked. Applied
// to stripDriveLetter(canon) so the drive-absolute, drive-relative and
// other-drive spellings all match; canon is already lowercased upstream, so
// SAM.LOG1 and sam.log1 are equivalent. Denied for read AND write.
//
// The anchoring after the hive word is load-bearing: the optional
// `\.(?:log\d*|sav)` only fires on a TRUE log/backup suffix of the EXACT hive
// name, and the trailing `(?:/.*)?$` still requires a path separator or
// end-of-string — so non-hive siblings such as `software_report.txt`,
// `systeminfo.log`, `softwarelist.json` and the `systemprofile\` user dir stay
// ALLOW (see TestFileGate_AllowHiveLookalikesNotOverBlocked).
var windowsHiveRe = regexp.MustCompile(`^/` + windowsHiveRootAlt + `/system32/config/(?:regback/)?(sam|security|system|software)(?:\.(?:log\d*|sav))?(?:/.*)?$`)

// windowsHiveCLFSRe matches the Common Log File System (CLFS) backing files of
// the registry's transactional engine under System32\config — the
// `{guid}.TM.blf` base log and `{guid}.TMContainer<n>.regtrans-ms` containers
// (incl. the `TxR\` subdir). These replay uncommitted hive transactions and so
// expose the same secrets as the hive. Scoped STRICTLY to the config subtree so
// generic CLFS logs elsewhere on disk are NOT over-blocked.
var windowsHiveCLFSRe = regexp.MustCompile(`^/` + windowsHiveRootAlt + `/system32/config/(?:txr/)?[^/]+\.tm(?:container[0-9]*)?\.(?:blf|regtrans-ms)$`)

// matchWindowsRegistryHive reports whether canon targets a Windows registry
// credential hive — or its transaction-log/backup/CLFS siblings, which hold the
// same secrets — drive-letter-agnostically (see windowsHiveRe / windowsHiveCLFSRe
// / the fail-open this closes). Returns the per-hive sub-reason on a hit.
func matchWindowsRegistryHive(canon string) (string, bool) {
	stripped := stripDriveLetter(canon)
	if m := windowsHiveRe.FindStringSubmatch(stripped); m != nil {
		switch m[1] {
		case "sam":
			return "sensitive_path:windows_sam", true
		case "security":
			return "sensitive_path:windows_security_hive", true
		case "system":
			return "sensitive_path:windows_system_hive", true
		case "software":
			return "sensitive_path:windows_software_hive", true
		}
	}
	// CLFS transaction containers ({guid}.TM.blf / *.TMContainer*.regtrans-ms)
	// cannot be attributed to a single hive, so they get a dedicated sub-reason.
	if windowsHiveCLFSRe.MatchString(stripped) {
		return "sensitive_path:windows_registry_clfs", true
	}
	return "", false
}

// Basename credential patterns. These DENY regardless of directory.
//
// Two forms: exact basenames (e.g. id_rsa) and suffix patterns (e.g. *.pem).
// Pattern matching is intentionally narrow — overly broad globs would over-refuse.
var credentialExact = map[string]string{
	"id_rsa":         "credential:ssh_rsa",
	"id_rsa.pub":     "credential:ssh_rsa_pub",
	"id_ed25519":     "credential:ssh_ed25519",
	"id_ed25519.pub": "credential:ssh_ed25519_pub",
	"id_dsa":         "credential:ssh_dsa",
	"id_ecdsa":       "credential:ssh_ecdsa",
	".npmrc":         "credential:npmrc",
	".netrc":         "credential:netrc",
	// NOTE: ".env" and its ".env.<suffix>" variants are matched by
	// matchEnvCredential (in lockstep with core/edge/classifier.go
	// matchesEnvSecretFile), not here, so template spellings stay ALLOW.
	".pgpass":           "credential:pgpass",
	".dockerconfigjson": "credential:docker_pull_secret",
	"credentials.json":  "credential:gcp_or_generic_creds",
	"keystore.jks":      "credential:jks",
	"truststore.jks":    "credential:jks",
}

var credentialSuffix = []struct {
	suffix    string
	subReason string
}{
	{".pem", "credential:pem"},
	{".key", "credential:key"},
	{".kdbx", "credential:kdbx"},
	{".pgp", "credential:pgp"},
	{".gpg", "credential:gpg"},
	{".asc", "credential:pgp_asc"},
	{".p12", "credential:pkcs12"},
	{".pfx", "credential:pkcs12"},
}

var credentialPrefix = []struct {
	prefix    string
	suffix    string
	subReason string
}{
	{"service_account", ".json", "credential:gcp_service_account"},
	{"service-account", ".json", "credential:gcp_service_account"},
}

func matchCredentialBasename(canon string) (string, bool) {
	base := canon
	if idx := strings.LastIndex(canon, "/"); idx >= 0 {
		base = canon[idx+1:]
	}
	if base == "" {
		return "", false
	}
	// Dotenv secret family (.env, .env.local, .env.production, ...) excluding
	// template spellings — kept in lockstep with the Edge classifier.
	if reason, ok := matchEnvCredential(base); ok {
		return reason, true
	}
	if reason, ok := credentialExact[base]; ok {
		return reason, true
	}
	for _, suf := range credentialSuffix {
		if strings.HasSuffix(base, suf.suffix) {
			return suf.subReason, true
		}
	}
	for _, p := range credentialPrefix {
		if strings.HasPrefix(base, p.prefix) && strings.HasSuffix(base, p.suffix) {
			return p.subReason, true
		}
	}
	return "", false
}

// envSecretTemplateSuffixes are the conventionally non-secret ".env" template
// spellings committed to source control. KEEP IN LOCKSTEP with
// core/edge/classifier.go matchesEnvSecretFile (EDGE-064): if you change one,
// change the other (and the lockstep test in file_gate_test.go).
var envSecretTemplateSuffixes = []string{".example", ".sample", ".template", ".dist", ".defaults"}

// matchEnvCredential reports whether base (a path basename) is a dotenv secret
// file: ".env" or any ".env.<suffix>" variant (.env.local, .env.production,
// .env.staging, ...), EXCLUDING the known non-secret template spellings. It
// mirrors core/edge/classifier.go matchesEnvSecretFile so FileGate and the Edge
// classifier never disagree on what a secret .env file is. Previously only the
// bare ".env" was caught, so .env.<env> variants fell through to ALLOW.
func matchEnvCredential(base string) (string, bool) {
	if base != ".env" && !strings.HasPrefix(base, ".env.") {
		return "", false
	}
	for _, suf := range envSecretTemplateSuffixes {
		if base == ".env"+suf {
			return "", false
		}
	}
	return "credential:dotenv", true
}

func isWriteVerb(v config.ActionVerb) bool {
	switch v {
	case config.ActionVerbWrite, config.ActionVerbDelete, config.ActionVerbDrop, config.ActionVerbTruncate,
		config.ActionVerbExport, config.ActionVerbAdminGrant, config.ActionVerbAdminRevoke,
		config.ActionVerbRoleAssign, config.ActionVerbRoleRemove, config.ActionVerbLicenseCreate,
		config.ActionVerbLicenseRevoke, config.ActionVerbLicenseChange, config.ActionVerbKeyRotate,
		config.ActionVerbKeyDelete, config.ActionVerbSecretsWrite, config.ActionVerbSecretsDelete,
		config.ActionVerbConfigWrite, config.ActionVerbConfigDelete, config.ActionVerbBackupRestore,
		config.ActionVerbTenantCreate, config.ActionVerbTenantDelete, config.ActionVerbPayment:
		return true
	}
	return false
}

// itoa is a tiny int-to-string used for SIEM breadcrumbs; avoids strconv import
// inflation in a tight package.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
