//go:build !windows

package main

import "os"

// replace moves the new binary into place. A rename within one directory is atomic, and a
// running process keeps the old file (inode) open, so running MCP servers are not affected.
func replace(tmp, dest string) error {
	return os.Rename(tmp, dest)
}
