// Package sound plays a chime or speaks text on a chosen audio output device.
//
// The point is to play on a device picked by name rather than on the system default output.
// Device indexes change whenever devices are plugged in or out, so the device is looked up
// by name on every call. If it cannot be found, an error is returned; there is no fallback
// to the default output.
//
// This file is platform-neutral. Each platform provides listDevices, playChime, renderSpeech
// and playSpeech in its own build-tagged file (platform_darwin.go, platform_windows.go,
// platform_other.go), and the file lock in lock_unix.go / lock_windows.go / lock_other.go.
package sound

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultVoice is what Speak reports when no voice is configured and the system's default voice is used.
const DefaultVoice = "system default"

// Player knows where the config file is and reads it again on every call.
type Player struct {
	ConfigPath string
}

// Device is one audio device as seen by the platform backend. Input devices (microphones) are included.
type Device struct {
	Index   int    // backend-specific index used to play; changes when devices are plugged in or out
	Name    string // name from the OS (may be "(null)" when the OS has none)
	UID     string
	Inputs  int  // number of input channels
	Outputs int  // number of output channels
	Known   bool // whether channel counts could be read from the OS
}

// CanPlay reports whether the device is an output device. If the channel counts are unknown, it is assumed to be one.
func (d Device) CanPlay() bool { return !d.Known || d.Outputs > 0 }

// Matches reports whether the name or the UID contains query, ignoring case.
func (d Device) Matches(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	return q != "" && (strings.Contains(strings.ToLower(d.Name), q) || strings.Contains(strings.ToLower(d.UID), q))
}

func (d Device) String() string {
	kind := "unknown in/out"
	switch {
	case !d.Known:
	case d.Outputs > 0 && d.Inputs > 0:
		kind = fmt.Sprintf("output %dch, input %dch", d.Outputs, d.Inputs)
	case d.Outputs > 0:
		kind = fmt.Sprintf("output %dch", d.Outputs)
	case d.Inputs > 0:
		kind = fmt.Sprintf("input only %dch", d.Inputs)
	default:
		kind = "no channels"
	}
	return fmt.Sprintf("[%d] %s (%s), uid %s", d.Index, d.Name, kind, d.UID)
}

func (p *Player) config() (Config, error) {
	return LoadConfig(p.ConfigPath)
}

// ListDevices returns every audio device the backend can see.
func (p *Player) ListDevices(ctx context.Context) ([]Device, error) {
	cfg, err := p.config()
	if err != nil {
		return nil, err
	}
	return listDevices(ctx, cfg)
}

// MatchDevice picks the one output device whose name or UID contains query.
// No match or several matches is an error that lists the candidates.
func MatchDevice(devs []Device, query string) (Device, error) {
	if strings.TrimSpace(query) == "" {
		return Device{}, errors.New("empty device name")
	}
	var hits []Device
	for _, d := range devs {
		if d.CanPlay() && d.Matches(query) {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Device{}, fmt.Errorf("no output device matches %q (not falling back to the default output). Devices:\n%s", query, listText(devs))
	default:
		return Device{}, fmt.Errorf("%d output devices match %q; use a more specific name. Matches:\n%s", len(hits), query, listText(hits))
	}
}

func listText(devs []Device) string {
	var b strings.Builder
	for _, d := range devs {
		b.WriteString("  " + d.String() + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// resolve finds the device for the device argument (or the config's default when empty) and its config entry.
func resolve(ctx context.Context, cfg Config, device string) (Device, *DeviceConfig, error) {
	if strings.TrimSpace(device) == "" {
		for _, e := range cfg.Devices {
			if e.Default {
				device = e.Match
			}
		}
	}
	if strings.TrimSpace(device) == "" {
		return Device{}, nil, errors.New("no device given and no default device in the config; pick one from list_devices")
	}
	devs, err := listDevices(ctx, cfg)
	if err != nil {
		return Device{}, nil, err
	}
	d, err := MatchDevice(devs, device)
	if err != nil {
		return Device{}, nil, err
	}
	return d, cfg.entryFor(d), nil
}

// Chime plays the chime count times, one second apart.
func (p *Player) Chime(ctx context.Context, device string, count int) (Device, error) {
	if count < 1 {
		return Device{}, fmt.Errorf("count must be 1 or more, got %d", count)
	}
	cfg, err := p.config()
	if err != nil {
		return Device{}, err
	}
	unlock, err := lock(ctx)
	if err != nil {
		return Device{}, err
	}
	defer unlock()
	d, e, err := resolve(ctx, cfg, device)
	if err != nil {
		return Device{}, err
	}
	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleep(ctx, time.Second); err != nil {
				return d, err
			}
		}
		if err := playChime(ctx, cfg, d, cfg.chimeVolume(e)); err != nil {
			return d, err
		}
	}
	return d, nil
}

// Speak renders the text with the system's text-to-speech and plays it on the device.
// voice overrides the config's voice when not empty.
func (p *Player) Speak(ctx context.Context, device, text, voice string) (Device, string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return Device{}, "", errors.New("empty text")
	}
	cfg, err := p.config()
	if err != nil {
		return Device{}, "", err
	}
	if voice != "" {
		cfg.Voice = voice
	}
	path, voice, err := renderSpeech(ctx, cfg, text)
	if err != nil {
		return Device{}, voice, err
	}
	defer os.Remove(path)
	unlock, err := lock(ctx)
	if err != nil {
		return Device{}, voice, err
	}
	defer unlock()
	d, e, err := resolve(ctx, cfg, device)
	if err != nil {
		return Device{}, voice, err
	}
	return d, voice, playSpeech(ctx, cfg, d, path, cfg.speechVolume(e))
}

// Listing is what list_devices returns: the config's remarks and volumes laid over the actual devices.
type Listing struct {
	Outputs      []ListedDevice // devices that can play
	Inputs       []Device       // input-only devices (cannot play)
	Disconnected []DeviceConfig // config entries that are not connected now or do not match exactly one device
	ConfigPath   string
	Config       Config
}

// ListedDevice is an output device and its config entry (nil if there is none).
type ListedDevice struct {
	Device
	Entry        *DeviceConfig
	ChimeVolume  float64
	SpeechVolume float64
}

// List builds the listing.
func (p *Player) List(ctx context.Context) (Listing, error) {
	cfg, err := p.config()
	if err != nil {
		return Listing{}, err
	}
	devs, err := listDevices(ctx, cfg)
	if err != nil {
		return Listing{}, err
	}
	return buildListing(devs, cfg, p.ConfigPath), nil
}

func buildListing(devs []Device, cfg Config, path string) Listing {
	l := Listing{ConfigPath: path, Config: cfg}
	for _, d := range devs {
		if !d.CanPlay() {
			l.Inputs = append(l.Inputs, d)
			continue
		}
		e := cfg.entryFor(d)
		l.Outputs = append(l.Outputs, ListedDevice{Device: d, Entry: e, ChimeVolume: cfg.chimeVolume(e), SpeechVolume: cfg.speechVolume(e)})
	}
	for _, e := range cfg.Devices {
		if _, err := MatchDevice(devs, e.Match); err != nil {
			l.Disconnected = append(l.Disconnected, e)
		}
	}
	return l
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// lock plays one sound at a time, so that calls from several sessions (separate processes) do not overlap.
// TMPDIR can differ between sessions, so the lock file lives under ~/.app-data.
func lock(ctx context.Context) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".app-data", "aimed-chime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "play.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(ctx, f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { unlock(); f.Close() }, nil
}
