//go:build !windows

package claude

import "io/fs"

// artifactMSYSLink reports a Windows file Git Bash reads as a link, which
// exists only on Windows.
func artifactMSYSLink(fs.FileInfo) bool { return false }
