//go:build windows

package claude

import (
	"io/fs"
	"syscall"
)

// artifactSystemFile reports a file with the SYSTEM attribute, or one whose
// attributes cannot be read. Git Bash's MSYS programs (bash, sh, perl) read a
// SYSTEM file that starts with !<symlink> as a link to another file, while
// Go reads its own bytes.
func artifactSystemFile(info fs.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || data.FileAttributes&syscall.FILE_ATTRIBUTE_SYSTEM != 0
}
