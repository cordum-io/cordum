//go:build windows

package agentd

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsACLEntry struct {
	sid  *windows.SID
	mask windows.ACCESS_MASK
}

func applyStatePathPermissions(path string) error {
	return applyWindowsDACL(path)
}

func verifyStateDirPermissions(path string) error {
	return verifyWindowsStateDACL(path)
}

func applyWindowsDACL(path string) error {
	ownerSID, err := currentWindowsUserSID()
	if err != nil {
		return fmt.Errorf("get current user sid: %w", err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("create system sid: %w", err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		windowsAllowFullControl(ownerSID, windows.TRUSTEE_IS_USER),
		windowsAllowFullControl(systemSID, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
	}, nil)
	if err != nil {
		return fmt.Errorf("build owner/system dacl: %w", err)
	}
	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInfo, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("set owner/system dacl: %w", err)
	}
	return nil
}

func verifyWindowsStateDACL(path string) error {
	ownerSID, entries, err := readWindowsStateDACL(path)
	if err != nil {
		return err
	}
	currentSID, err := currentWindowsUserSID()
	if err != nil {
		return fmt.Errorf("get current user sid: %w", err)
	}
	if ownerSID == nil || !ownerSID.Equals(currentSID) {
		return fmt.Errorf("state path owner is %s, want current user %s", windowsSIDString(ownerSID), currentSID.String())
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("create system sid: %w", err)
	}
	forbidden, err := forbiddenWindowsStateSIDs()
	if err != nil {
		return err
	}
	if len(entries) != 2 {
		return fmt.Errorf("state path dacl has %d ACEs, want exactly 2 owner/system ACEs", len(entries))
	}
	var ownerSeen, systemSeen bool
	for _, entry := range entries {
		for name, sid := range forbidden {
			if entry.sid.Equals(sid) {
				return fmt.Errorf("state path dacl grants forbidden %s sid %s", name, entry.sid.String())
			}
		}
		if !windowsGrantsFullControl(entry.mask) {
			return fmt.Errorf("state path sid %s mask %#x does not grant full control", entry.sid.String(), uint32(entry.mask))
		}
		switch {
		case entry.sid.Equals(currentSID):
			ownerSeen = true
		case entry.sid.Equals(systemSID):
			systemSeen = true
		default:
			return fmt.Errorf("state path dacl grants unexpected sid %s", entry.sid.String())
		}
	}
	if !ownerSeen || !systemSeen {
		return fmt.Errorf("state path dacl missing owner/system ACEs (owner=%v system=%v)", ownerSeen, systemSeen)
	}
	return nil
}

func windowsAllowFullControl(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windowsFileFullControlMask,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

const windowsFileFullControlMask windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func windowsGrantsFullControl(mask windows.ACCESS_MASK) bool {
	return mask&windows.GENERIC_ALL != 0 || mask&windowsFileFullControlMask == windowsFileFullControlMask
}

func currentWindowsUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid.Copy()
}

func readWindowsStateDACL(path string) (*windows.SID, []windowsACLEntry, error) {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("read state path security info: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return nil, nil, fmt.Errorf("read state path owner: %w", err)
	}
	ownerCopy, err := owner.Copy()
	if err != nil {
		return nil, nil, fmt.Errorf("copy state path owner sid: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, nil, fmt.Errorf("read state path dacl: %w", err)
	}
	if dacl == nil {
		return ownerCopy, nil, fmt.Errorf("state path has nil dacl")
	}
	entries := make([]windowsACLEntry, 0, dacl.AceCount)
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, nil, fmt.Errorf("read dacl ace %d: %w", i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return nil, nil, fmt.Errorf("state path dacl ace %d type %d is not allow", i, ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		copied, err := sid.Copy()
		if err != nil {
			return nil, nil, fmt.Errorf("copy dacl ace %d sid: %w", i, err)
		}
		entries = append(entries, windowsACLEntry{sid: copied, mask: ace.Mask})
	}
	return ownerCopy, entries, nil
}

func forbiddenWindowsStateSIDs() (map[string]*windows.SID, error) {
	types := map[string]windows.WELL_KNOWN_SID_TYPE{
		"Everyone":            windows.WinWorldSid,
		"Authenticated Users": windows.WinAuthenticatedUserSid,
		"Users":               windows.WinBuiltinUsersSid,
	}
	out := make(map[string]*windows.SID, len(types))
	for name, sidType := range types {
		sid, err := windows.CreateWellKnownSid(sidType)
		if err != nil {
			return nil, fmt.Errorf("create %s sid: %w", name, err)
		}
		out[name] = sid
	}
	return out, nil
}

func windowsSIDString(sid *windows.SID) string {
	if sid == nil {
		return "<nil>"
	}
	return sid.String()
}
