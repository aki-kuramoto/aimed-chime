//go:build darwin

package sound

import "testing"

const listOut = `[AudioToolbox @ 0x92d0183c0] CoreAudio devices:
[AudioToolbox @ 0x92d0183c0] [0]                  Example Monitor, 00000000-0000-0000-0000-000000000001
[AudioToolbox @ 0x92d0183c0] [1]                         (null), USBAudio:Example:Desk Speaker:000001:1
[AudioToolbox @ 0x92d0183c0] [2]                         (null), BuiltInMicrophoneDevice
[AudioToolbox @ 0x92d0183c0] [3]                         (null), BuiltInSpeakerDevice
Output #0, audiotoolbox, to 'pipe:':
`

func TestParseDevices(t *testing.T) {
	devs := ParseDevices(listOut)
	if len(devs) != 4 {
		t.Fatalf("got %d devices: %v", len(devs), devs)
	}
	want := Device{Index: 1, Name: "(null)", UID: "USBAudio:Example:Desk Speaker:000001:1"}
	if devs[1] != want {
		t.Errorf("devs[1] = %+v, want %+v", devs[1], want)
	}
	if devs[0].Name != "Example Monitor" {
		t.Errorf("devs[0].Name = %q", devs[0].Name)
	}
}

func TestParseVoices(t *testing.T) {
	out := "Flo (German (Germany))     de_DE    # Hallo! Ich heiße Flo.\n" +
		"Example Voice       en_US    # Hello! My name is Example Voice.\n"
	got := ParseVoices(out)
	if len(got) != 2 || got[0] != "Flo (German (Germany))" || got[1] != "Example Voice" {
		t.Errorf("ParseVoices = %q", got)
	}
}
