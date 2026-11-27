//go:build windows

package claude

import (
	"io/fs"
	"syscall"
)

// artifactMSYSLink reports a file with the SYSTEM or READONLY attribute, or
// one whose attributes cannot be read. Git Bash's MSYS programs (bash, sh,
// perl) read a SYSTEM file that starts with !<symlink>, and a READONLY Windows
// shortcut (.lnk), as links to other files, while Go reads their own bytes.
func artifactMSYSLink(info fs.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || data.FileAttributes&(syscall.FILE_ATTRIBUTE_SYSTEM|syscall.FILE_ATTRIBUTE_READONLY) != 0
}
