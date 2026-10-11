//go:build unix || windows

package sound

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Two handles on the same file conflict even within one process, so this exercises the real lock.
func TestLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "play.lock")
	open := func() *os.File {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	a, b := open(), open()
	unlockA, err := lockFile(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := lockFile(ctx, b); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while held: %v, want deadline exceeded", err)
	}
	unlockA()
	unlockB, err := lockFile(context.Background(), b)
	if err != nil {
		t.Fatalf("second lock after unlock: %v", err)
	}
	unlockB()
}
