//go:build windows

package sound

// Windows backend: devices are listed and played through WASAPI (wasapi_windows.go), the chime is
// synthesized here, and speech is rendered with the system's text-to-speech (SAPI) through Windows
// PowerShell. ffmpeg cannot play on a chosen device on Windows, so it is not used at all (nor is the
// ffmpeg config key), and neither is cgo.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func listDevices(ctx context.Context, cfg Config) ([]Device, error) {
	devs, err := wasapiDevices()
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		return nil, errors.New("Windows reported no active audio devices")
	}
	return devs, nil
}

const chimeRate = 48000

// chimePCM is one chime as 16-bit stereo PCM at chimeRate: 880 -> 660 -> 990 Hz sine tones, the same
// as the macOS backend's ffmpeg sine sources (amplitude 1/8) through its volume filter.
func chimePCM(volume float64) []byte {
	tones := []struct{ freq, dur float64 }{{880, 0.25}, {660, 0.25}, {990, 0.5}}
	var pcm []byte
	for _, t := range tones {
		n := int(t.dur * chimeRate)
		for i := 0; i < n; i++ {
			s := uint16(toInt16(volume / 8 * math.Sin(2*math.Pi*t.freq*float64(i)/chimeRate)))
			pcm = binary.LittleEndian.AppendUint16(pcm, s)
			pcm = binary.LittleEndian.AppendUint16(pcm, s)
		}
	}
	return pcm
}

func toInt16(v float64) int16 {
	return int16(math.Round(math.Max(-1, math.Min(1, v)) * math.MaxInt16))
}

// playChime plays the chime once.
func playChime(ctx context.Context, cfg Config, d Device, volume float64) error {
	return playPCM(ctx, d.UID, chimeRate, 2, chimePCM(volume))
}

// speechScript renders $env:AIMED_CHIME_TEXT_FILE (UTF-8) to the WAV file $env:AIMED_CHIME_WAV and
// prints the name of the voice it used. An unknown voice exits with 3 and lists the installed ones,
// because SelectVoice's own error does not.
const speechScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch {}
try {
	Add-Type -AssemblyName System.Speech
	$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
	try {
		$voice = $env:AIMED_CHIME_VOICE
		if ($voice) {
			$names = @($s.GetInstalledVoices() | Where-Object { $_.Enabled } | ForEach-Object { $_.VoiceInfo.Name })
			if ($names -notcontains $voice) {
				[Console]::Error.WriteLine('voice "' + $voice + '" is not installed. Installed voices: ' + ($names -join ', '))
				exit 3
			}
			$s.SelectVoice($voice)
		}
		$text = [System.IO.File]::ReadAllText($env:AIMED_CHIME_TEXT_FILE, [System.Text.Encoding]::UTF8)
		$s.SetOutputToWaveFile($env:AIMED_CHIME_WAV)
		$s.Speak($text)
		$s.SetOutputToNull()
		[Console]::Out.Write($s.Voice.Name)
	} finally {
		$s.Dispose()
	}
} catch {
	[Console]::Error.WriteLine($_.Exception.Message)
	exit 1
}
`

// renderSpeech renders the text to a WAV file with SAPI, because playing it ourselves is what lets
// it go to the chosen device and have its volume set. The text goes through a file so that its
// length is not limited by the command-line size. The caller removes the WAV file. voice is what
// was used, for reporting.
func renderSpeech(ctx context.Context, cfg Config, text string) (path, voice string, err error) {
	voice = DefaultVoice
	if cfg.Voice != "" {
		voice = cfg.Voice
	}
	txt, err := writeTemp("aimed-chime-*.txt", []byte(text))
	if err != nil {
		return "", voice, err
	}
	defer os.Remove(txt)
	wav, err := writeTemp("aimed-chime-*.wav", nil)
	if err != nil {
		return "", voice, err
	}
	cmd := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeCommand(speechScript))
	cmd.Env = append(os.Environ(),
		"AIMED_CHIME_TEXT_FILE="+txt, "AIMED_CHIME_WAV="+wav, "AIMED_CHIME_VOICE="+cfg.Voice)
	hideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(wav)
		msg := stripCLIXML(stderr.String())
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return "", voice, errors.New(msg)
		}
		return "", voice, fmt.Errorf("text-to-speech failed: %v\n%s", err, msg)
	}
	if used := strings.TrimSpace(stdout.String()); cfg.Voice == "" && used != "" {
		voice = fmt.Sprintf("%s (%s)", DefaultVoice, used)
	}
	return wav, voice, nil
}

// stripCLIXML drops the serialized progress records that PowerShell can write to a redirected stderr.
func stripCLIXML(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#< CLIXML") && !strings.HasPrefix(t, "<Objs ") {
			lines = append(lines, t)
		}
	}
	return strings.Join(lines, "\n")
}

// playSpeech plays a rendered WAV file. Unlike the macOS backend no silence is appended:
// playPCM itself waits until the device has played the last sample.
func playSpeech(ctx context.Context, cfg Config, d Device, path string, volume float64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	w, err := parseWAV(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	applyGain(w.pcm, volume)
	return playPCM(ctx, d.UID, w.rate, w.channels, w.pcm)
}

type wavData struct {
	rate, channels int
	pcm            []byte // interleaved 16-bit little-endian
}

// parseWAV reads a 16-bit PCM RIFF WAVE file, which is what SAPI writes.
func parseWAV(b []byte) (wavData, error) {
	var w wavData
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return w, errors.New("not a WAV file")
	}
	gotFmt := false
	for b = b[12:]; len(b) >= 8; {
		id, size := string(b[0:4]), int(binary.LittleEndian.Uint32(b[4:8]))
		body := b[8:]
		if size > len(body) {
			size = len(body) // a truncated last chunk
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return w, errors.New("WAV fmt chunk is too short")
			}
			tag := binary.LittleEndian.Uint16(body[0:2])
			w.channels = int(binary.LittleEndian.Uint16(body[2:4]))
			w.rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits := binary.LittleEndian.Uint16(body[14:16])
			if (tag != 1 && tag != 0xFFFE) || bits != 16 || w.channels < 1 || w.rate < 1 {
				return w, fmt.Errorf("unsupported WAV format (tag %#x, %d bits, %d channels, %d Hz); want 16-bit PCM", tag, bits, w.channels, w.rate)
			}
			gotFmt = true
		case "data":
			if !gotFmt {
				return w, errors.New("WAV data chunk before fmt chunk")
			}
			n := size / (2 * w.channels) * (2 * w.channels)
			w.pcm = body[:n]
			return w, nil
		}
		next := 8 + size + size%2 // chunks are padded to an even size
		if next > len(b) {
			break
		}
		b = b[next:]
	}
	return w, errors.New("WAV file has no data chunk")
}

// applyGain multiplies 16-bit PCM samples in place, clipping like ffmpeg's volume filter does on output.
func applyGain(pcm []byte, volume float64) {
	if volume == 1 {
		return
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		s := float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) * volume
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(math.Round(math.Max(math.MinInt16, math.Min(math.MaxInt16, s))))))
	}
}

func writeTemp(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// powershellPath is Windows PowerShell 5.1, which every Windows 10 / 11 has and which can load
// System.Speech (PowerShell 7 cannot). MCP servers are sometimes started with a minimal PATH,
// so the system directory is used rather than a PATH search.
func powershellPath() string {
	if dir, err := windows.GetSystemDirectory(); err == nil {
		return filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	return "powershell.exe"
}

// encodeCommand encodes a script for powershell -EncodedCommand (base64 of UTF-16LE).
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// hideWindow keeps a console window from flashing up when the server has no console of its own.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
