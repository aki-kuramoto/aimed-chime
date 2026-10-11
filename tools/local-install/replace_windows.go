//go:build windows

package main

import (
	"fmt"
	"os"
	"time"
)

// replace moves the new binary into place. A running .exe cannot be overwritten or deleted on
// Windows, but it can be renamed, so the old one is moved aside first. Old copies that are no
// longer running are cleaned up on the next install.
func replace(tmp, dest string) error {
	old := dest + ".old"
	if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
		// Still running from an earlier install: use a unique name instead.
		old = fmt.Sprintf("%s.%d.old", dest, time.Now().Unix())
	}
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, old); err != nil {
			return fmt.Errorf("move the running binary aside: %w", err)
		}
	}
	return os.Rename(tmp, dest)
}
