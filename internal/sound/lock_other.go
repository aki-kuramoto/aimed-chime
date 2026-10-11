//go:build !unix && !windows

package sound

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// lockFile is not implemented on this platform yet.
func lockFile(ctx context.Context, f *os.File) (func(), error) {
	return nil, fmt.Errorf("play lock is not implemented on %s yet", runtime.GOOS)
}
