//go:build unix

package sound

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile takes an exclusive flock on f, polling so that ctx can cancel the wait.
func lockFile(ctx context.Context, f *os.File) (func(), error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, err
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
