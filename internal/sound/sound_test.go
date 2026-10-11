package sound

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testDevices are what a backend might report: an output with a null name, a monitor,
// an input-only microphone and a built-in speaker.
func testDevices() []Device {
	return []Device{
		{Index: 0, Name: "Example Monitor", UID: "00000000-0000-0000-0000-000000000001", Outputs: 2, Known: true},
		{Index: 1, Name: "(null)", UID: "USBAudio:Example:Desk Speaker:000001:1", Outputs: 2, Inputs: 2, Known: true},
		{Index: 2, Name: "(null)", UID: "BuiltInMicrophoneDevice", Inputs: 1, Known: true},
		{Index: 3, Name: "(null)", UID: "BuiltInSpeakerDevice", Outputs: 2, Known: true},
	}
}

func TestMatchDevice(t *testing.T) {
	devs := testDevices()
	d, err := MatchDevice(devs, "desk speaker")
	if err != nil || d.Index != 1 {
		t.Errorf("desk speaker: %+v, %v", d, err)
	}
	if _, err := MatchDevice(devs, "Microphone"); err == nil || !strings.Contains(err.Error(), "not falling back") {
		t.Errorf("Microphone: %v", err)
	}
	// The input-only microphone is not a candidate, so "BuiltIn" narrows down to the speaker.
	if d, err := MatchDevice(devs, "BuiltIn"); err != nil || d.Index != 3 {
		t.Errorf("BuiltIn: %+v, %v", d, err)
	}
	if _, err := MatchDevice(devs, "0"); err == nil || !strings.Contains(err.Error(), "output devices match") {
		t.Errorf("0: %v", err)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.setay")
	if c, err := LoadConfig(path); err != nil || c.ChimeVolume != 6 || c.SpeechVolume != 1.4 {
		t.Fatalf("missing file: %+v, %v", c, err)
	}
	os.WriteFile(path, []byte(`{
	# comment
	devices = [
		{ match = 'Desk Speaker'; remarks = 'research work'; default; speech-volume = 2; },
		{ match = 'Headset'; remarks = 'day-to-day work'; chime-volume = 3; },
	];
	chime-volume = 5;
}`), 0o644)
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	desk := Device{Name: "(null)", UID: "USBAudio:Example:Desk Speaker:000001:1"}
	e := c.entryFor(desk)
	if e == nil || !e.Default || e.Remarks != "research work" || c.chimeVolume(e) != 5 || c.speechVolume(e) != 2 {
		t.Errorf("desk speaker entry: %+v", e)
	}
	hs := c.entryFor(Device{Name: "Example USB Headset"})
	if hs == nil || c.chimeVolume(hs) != 3 || c.speechVolume(hs) != 1.4 {
		t.Errorf("headset entry: %+v", hs)
	}
	os.WriteFile(path, []byte(`{ devices = [ { match = 'Desk Speaker'; remark = 'typo'; } ]; }`), 0o644)
	if _, err := LoadConfig(path); err == nil {
		t.Error("unknown key should fail")
	}
}

func TestExampleConfig(t *testing.T) {
	c, err := LoadConfig(filepath.Join("..", "..", "config.example.setay"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Devices) != 2 || c.Devices[1].ChimeVolume != 4 || c.Voice != "" {
		t.Errorf("example config: %+v", c)
	}
}

func TestBuildListing(t *testing.T) {
	cfg := Config{ChimeVolume: 6, SpeechVolume: 1.4, Devices: []DeviceConfig{
		{Match: "Desk Speaker", Remarks: "research work", ChimeVolume: 3},
		{Match: "Not Connected", Remarks: "unplugged"},
	}}
	l := buildListing(testDevices(), cfg, "config.setay")
	if len(l.Outputs) != 3 || len(l.Inputs) != 1 || len(l.Disconnected) != 1 || l.Disconnected[0].Match != "Not Connected" {
		t.Fatalf("listing: %+v", l)
	}
	desk := l.Outputs[1]
	if desk.Entry == nil || desk.Entry.Remarks != "research work" || desk.ChimeVolume != 3 || desk.SpeechVolume != 1.4 {
		t.Errorf("desk speaker: %+v", desk)
	}
	if l.Outputs[0].Entry != nil {
		t.Errorf("monitor should have no entry: %+v", l.Outputs[0])
	}
}
