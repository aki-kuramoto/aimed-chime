//go:build windows

package sound

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive LockFileEx lock on f, polling so that ctx can cancel the wait.
// Windows releases the lock when the handle is closed or the process exits.
func lockFile(ctx context.Context, f *os.File) (func(), error) {
	h := windows.Handle(f.Fd())
	for {
		ol := new(windows.Overlapped)
		err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
		if err == nil {
			return func() { windows.UnlockFileEx(h, 0, 1, 0, ol) }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, err
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
