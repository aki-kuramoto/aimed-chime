package sound

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	setay "github.com/aki-kuramoto/setay-go"
)

// Config is the content of ~/.app-data/aimed-chime/config.setay. It is read again on every call,
// so edits take effect from the next call.
type Config struct {
	Devices      []DeviceConfig `setay:"devices"`
	ChimeVolume  float64        `setay:"chime-volume"`  // default 6
	SpeechVolume float64        `setay:"speech-volume"` // default 1.4
	Voice        string         `setay:"voice"`         // text-to-speech voice; empty means the system default voice
	FFmpeg       string         `setay:"ffmpeg"`        // empty means search PATH and the usual install locations
}

// DeviceConfig is the description and volumes of one device.
type DeviceConfig struct {
	Match        string  `setay:"match"`         // part of the name or UID; must match exactly one output device
	Remarks      string  `setay:"remarks"`       // shown by list_devices to help choose the device, e.g. which kind of work it is for
	Default      bool    `setay:"default"`       // used when device is omitted
	ChimeVolume  float64 `setay:"chime-volume"`  // 0 means the global value
	SpeechVolume float64 `setay:"speech-volume"` // 0 means the global value
}

// ConfigPath returns the config file path.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".app-data", "aimed-chime", "config.setay"), nil
}

// LoadConfig reads the config file. If there is no file, it returns the defaults.
// Unknown keys and out-of-range values are errors, so typos are not silently ignored.
func LoadConfig(path string) (Config, error) {
	var c Config
	if _, err := os.Stat(path); err == nil {
		if err := setay.UnmarshalFile(path, &c, setay.DisallowUnknownFields()); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	if c.ChimeVolume == 0 {
		c.ChimeVolume = 6
	}
	if c.SpeechVolume == 0 {
		c.SpeechVolume = 1.4
	}
	if c.ChimeVolume < 0 || c.SpeechVolume < 0 {
		return c, fmt.Errorf("%s: volumes must be positive", path)
	}
	defaults := 0
	for i, d := range c.Devices {
		if d.Match == "" {
			return c, fmt.Errorf("%s: devices[%d] has no match", path, i)
		}
		if d.ChimeVolume < 0 || d.SpeechVolume < 0 {
			return c, fmt.Errorf("%s: devices[%d] (%s): volumes must be positive", path, i, d.Match)
		}
		if d.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return c, fmt.Errorf("%s: %d devices are marked default; mark at most one", path, defaults)
	}
	return c, nil
}

// entryFor returns the config entry for an actual device, or nil.
func (c Config) entryFor(d Device) *DeviceConfig {
	for i := range c.Devices {
		if d.Matches(c.Devices[i].Match) {
			return &c.Devices[i]
		}
	}
	return nil
}

func (c Config) chimeVolume(e *DeviceConfig) float64 {
	if e != nil && e.ChimeVolume > 0 {
		return e.ChimeVolume
	}
	return c.ChimeVolume
}

func (c Config) speechVolume(e *DeviceConfig) float64 {
	if e != nil && e.SpeechVolume > 0 {
		return e.SpeechVolume
	}
	return c.SpeechVolume
}
