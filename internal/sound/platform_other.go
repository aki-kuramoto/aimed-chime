//go:build !darwin && !windows

package sound

// Placeholder backend for platforms that are not supported yet, so the rest of the program still builds.
// A platform backend provides listDevices, playChime, renderSpeech and playSpeech.

import (
	"context"
	"fmt"
	"runtime"
)

var errUnsupported = fmt.Errorf("aimed-chime does not support %s yet", runtime.GOOS)

func listDevices(ctx context.Context, cfg Config) ([]Device, error) {
	return nil, errUnsupported
}

func playChime(ctx context.Context, cfg Config, d Device, volume float64) error {
	return errUnsupported
}

func renderSpeech(ctx context.Context, cfg Config, text string) (path, voice string, err error) {
	return "", "", errUnsupported
}

func playSpeech(ctx context.Context, cfg Config, d Device, path string, volume float64) error {
	return errUnsupported
}
