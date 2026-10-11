//go:build windows

package sound

import (
	"encoding/binary"
	"strings"
	"testing"
)

func peak(pcm []byte) int {
	m := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		m = max(m, s, -s)
	}
	return m
}

func TestChimePCM(t *testing.T) {
	pcm := chimePCM(6)
	if want := chimeRate * 4; len(pcm) != want { // one second of 16-bit stereo
		t.Errorf("len = %d, want %d", len(pcm), want)
	}
	// Amplitude 1/8 times 6.
	if p := peak(pcm); p < 24500 || p > 24600 {
		t.Errorf("peak = %d, want about 0.75 of full scale", p)
	}
	if p := peak(chimePCM(100)); p != 32767 {
		t.Errorf("loud peak = %d, want clipped at 32767", p)
	}
}

// wav builds a WAV file with an extra chunk before data, as some writers add.
func wav(channels, bits int, samples ...int16) []byte {
	var b []byte
	le := binary.LittleEndian
	chunk := func(id string, body []byte) {
		b = append(b, id...)
		b = le.AppendUint32(b, uint32(len(body)))
		b = append(b, body...)
		if len(body)%2 == 1 {
			b = append(b, 0)
		}
	}
	var fmtBody []byte
	fmtBody = le.AppendUint16(fmtBody, 1)
	fmtBody = le.AppendUint16(fmtBody, uint16(channels))
	fmtBody = le.AppendUint32(fmtBody, 22050)
	fmtBody = le.AppendUint32(fmtBody, uint32(22050*channels*bits/8))
	fmtBody = le.AppendUint16(fmtBody, uint16(channels*bits/8))
	fmtBody = le.AppendUint16(fmtBody, uint16(bits))
	var data []byte
	for _, s := range samples {
		data = le.AppendUint16(data, uint16(s))
	}
	chunk("fmt ", fmtBody)
	chunk("LIST", []byte("odd"))
	chunk("data", data)
	return append(le.AppendUint32([]byte("RIFF"), uint32(len(b)+4)), append([]byte("WAVE"), b...)...)
}

func TestParseWAV(t *testing.T) {
	w, err := parseWAV(wav(1, 16, 100, -200, 300))
	if err != nil {
		t.Fatal(err)
	}
	if w.rate != 22050 || w.channels != 1 || len(w.pcm) != 6 || int16(binary.LittleEndian.Uint16(w.pcm[2:])) != -200 {
		t.Errorf("parseWAV = %+v", w)
	}
	// A partial last frame is dropped.
	if w, err := parseWAV(wav(2, 16, 1, 2, 3)); err != nil || len(w.pcm) != 4 {
		t.Errorf("stereo: %+v, %v", w, err)
	}
	if _, err := parseWAV(wav(1, 8, 0)); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("8-bit: %v", err)
	}
	if _, err := parseWAV([]byte("not a wav file")); err == nil {
		t.Error("garbage should fail")
	}
}

func TestApplyGain(t *testing.T) {
	pcm := binary.LittleEndian.AppendUint16(nil, uint16(1000))
	pcm = binary.LittleEndian.AppendUint16(pcm, uint16(30000))
	pcm = binary.LittleEndian.AppendUint16(pcm, 0x8000) // -32768
	applyGain(pcm, 1.5)
	got := []int16{int16(binary.LittleEndian.Uint16(pcm)), int16(binary.LittleEndian.Uint16(pcm[2:])), int16(binary.LittleEndian.Uint16(pcm[4:]))}
	if got[0] != 1500 || got[1] != 32767 || got[2] != -32768 {
		t.Errorf("applyGain = %v", got)
	}
}

func TestEncodeCommand(t *testing.T) {
	// "a" in UTF-16LE is 61 00.
	if got := encodeCommand("a"); got != "YQA=" {
		t.Errorf("encodeCommand = %q", got)
	}
}
