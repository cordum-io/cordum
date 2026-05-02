//go:build windows

package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestApplyWindowsDACLSetsOwnerOnlyACE(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write probe file: %v", err)
	}
	if err := applyWindowsDACL(path); err != nil {
		t.Fatalf("applyWindowsDACL: %v", err)
	}
	assertOwnerSystemOnlyDACL(t, path)
	if err := applyWindowsDACL(path); err != nil {
		t.Fatalf("second applyWindowsDACL: %v", err)
	}
	assertOwnerSystemOnlyDACL(t, path)
}

func TestVerifyStateDirPermissionsRejectsBroaderACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state-root")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}
	grantAuthenticatedUsersRead(t, path)
	if err := verifyStateDirPermissions(path); err == nil {
		t.Fatal("verifyStateDirPermissions returned nil for broader Authenticated Users ACL")
	}
}

func TestStateDirStrictPermsFailsClosed(t *testing.T) {
	t.Setenv("CORDUM_AGENTD_STRICT_PERMS", "1")
	path := filepath.Join(t.TempDir(), "state-root")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}
	grantAuthenticatedUsersRead(t, path)
	_, err := NewFileStateStore(path)
	if err == nil {
		t.Fatal("NewFileStateStore returned nil error for broader ACL under strict perms")
	}
	if !strings.Contains(err.Error(), "strict check") {
		t.Fatalf("NewFileStateStore error = %v, want strict check context", err)
	}
}

func assertOwnerSystemOnlyDACL(t *testing.T, path string) {
	t.Helper()
	owner, entries, err := readWindowsStateDACL(path)
	if err != nil {
		t.Fatalf("readWindowsStateDACL(%s): %v", path, err)
	}
	current, err := currentWindowsUserSID()
	if err != nil {
		t.Fatalf("currentWindowsUserSID: %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("system sid: %v", err)
	}
	forbidden, err := forbiddenWindowsStateSIDs()
	if err != nil {
		t.Fatalf("forbidden sids: %v", err)
	}
	if owner == nil || !owner.Equals(current) {
		t.Fatalf("owner = %v, want current user %v", windowsSIDString(owner), current.String())
	}
	if len(entries) != 2 {
		t.Fatalf("ACE count = %d, want exactly 2 owner/system ACEs", len(entries))
	}
	var ownerSeen, systemSeen bool
	for _, entry := range entries {
		for name, sid := range forbidden {
			if entry.sid.Equals(sid) {
				t.Fatalf("DACL grants forbidden %s sid %s", name, entry.sid.String())
			}
		}
		if !windowsGrantsFullControl(entry.mask) {
			t.Fatalf("DACL sid %s mask %#x does not grant full control", entry.sid.String(), uint32(entry.mask))
		}
		ownerSeen = ownerSeen || entry.sid.Equals(current)
		systemSeen = systemSeen || entry.sid.Equals(system)
	}
	if !ownerSeen || !systemSeen {
		t.Fatalf("DACL owner/system presence owner=%v system=%v", ownerSeen, systemSeen)
	}
}

func grantAuthenticatedUsersRead(t *testing.T, path string) {
	t.Helper()
	owner, err := currentWindowsUserSID()
	if err != nil {
		t.Fatalf("currentWindowsUserSID: %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("system sid: %v", err)
	}
	authenticatedUsers, err := windows.CreateWellKnownSid(windows.WinAuthenticatedUserSid)
	if err != nil {
		t.Fatalf("authenticated users sid: %v", err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		windowsAllowFullControl(owner, windows.TRUSTEE_IS_USER),
		windowsAllowFullControl(system, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
		{
			AccessPermissions: windows.GENERIC_READ,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(authenticatedUsers),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatalf("build broad ACL: %v", err)
	}
	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInfo, nil, nil, acl, nil); err != nil {
		t.Fatalf("set broad ACL: %v", err)
	}
}
