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
	// path.Clean to collapse repeats. Note: path.Clean('//./...') keeps the
	// device-namespace marker, which our device check needs.
	s = path.Clean(s)
	return s
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
	// Windows registry hives + SAM (always).
	{"c:/windows/system32/config/sam", false, "sensitive_path:windows_sam"},
	{"c:/windows/system32/config/security", false, "sensitive_path:windows_security_hive"},
	{"c:/windows/system32/config/system", false, "sensitive_path:windows_system_hive"},
	{"c:/windows/system32/config/software", false, "sensitive_path:windows_software_hive"},
	// Write-only (e.g. /etc/passwd is world-readable on Unix).
	{"/etc/passwd", true, "sensitive_path:etc_passwd_write"},
	{"/etc/group", true, "sensitive_path:etc_group_write"},
	{"/etc/hosts", true, "sensitive_path:etc_hosts_write"},
	{"/boot/", true, "sensitive_path:boot_write"},
	{"/sbin/", true, "sensitive_path:sbin_write"},
}

func matchSensitivePath(canon string, write bool) (string, bool) {
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
		{"c:/users/", "/.ssh/", "sensitive_path:user_ssh"},
		{"c:/users/", "/.aws/", "sensitive_path:user_aws_creds"},
		{"c:/users/", "/.docker/", "sensitive_path:user_docker_config"},
		{"c:/users/", "/.kube/", "sensitive_path:user_kube_config"},
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

// Basename credential patterns. These DENY regardless of directory.
//
// Two forms: exact basenames (e.g. id_rsa) and suffix patterns (e.g. *.pem).
// Pattern matching is intentionally narrow — overly broad globs would over-refuse.
var credentialExact = map[string]string{
	"id_rsa":           "credential:ssh_rsa",
	"id_rsa.pub":       "credential:ssh_rsa_pub",
	"id_ed25519":       "credential:ssh_ed25519",
	"id_ed25519.pub":   "credential:ssh_ed25519_pub",
	"id_dsa":           "credential:ssh_dsa",
	"id_ecdsa":         "credential:ssh_ecdsa",
	".npmrc":           "credential:npmrc",
	".netrc":           "credential:netrc",
	".env":             "credential:dotenv",
	".pgpass":          "credential:pgpass",
	".dockerconfigjson": "credential:docker_pull_secret",
	"credentials.json": "credential:gcp_or_generic_creds",
	"keystore.jks":     "credential:jks",
	"truststore.jks":   "credential:jks",
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
