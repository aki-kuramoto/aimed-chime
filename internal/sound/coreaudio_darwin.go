//go:build darwin

package sound

// ffmpeg's list alone cannot tell inputs from outputs (a USB headset, for example, shows up as two
// entries with the same name) and often shows "(null)" as the name, so the name and channel counts
// are read from CoreAudio per UID.

/*
#cgo LDFLAGS: -framework CoreAudio -framework CoreFoundation
#include <stdlib.h>
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>

static int ac_device_ids(AudioObjectID *out, int max) {
	AudioObjectPropertyAddress a = {kAudioHardwarePropertyDevices, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &a, 0, NULL, &size) != noErr) return -1;
	int n = size / sizeof(AudioObjectID);
	if (n > max) n = max;
	size = n * sizeof(AudioObjectID);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &size, out) != noErr) return -1;
	return size / sizeof(AudioObjectID);
}

static int ac_string(AudioObjectID id, AudioObjectPropertySelector sel, char *buf, int len) {
	AudioObjectPropertyAddress a = {sel, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
	CFStringRef s = NULL;
	UInt32 size = sizeof(s);
	if (AudioObjectGetPropertyData(id, &a, 0, NULL, &size, &s) != noErr || s == NULL) return 0;
	Boolean ok = CFStringGetCString(s, buf, len, kCFStringEncodingUTF8);
	CFRelease(s);
	return ok;
}

static int ac_channels(AudioObjectID id, int output) {
	AudioObjectPropertyAddress a = {kAudioDevicePropertyStreamConfiguration,
		output ? kAudioObjectPropertyScopeOutput : kAudioObjectPropertyScopeInput, kAudioObjectPropertyElementMain};
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(id, &a, 0, NULL, &size) != noErr || size == 0) return 0;
	AudioBufferList *l = malloc(size);
	int ch = 0;
	if (AudioObjectGetPropertyData(id, &a, 0, NULL, &size, l) == noErr) {
		for (UInt32 i = 0; i < l->mNumberBuffers; i++) ch += l->mBuffers[i].mNumberChannels;
	}
	free(l);
	return ch;
}
*/
import "C"

import "unsafe"

type coreAudioInfo struct {
	Name    string
	Inputs  int
	Outputs int
}

// coreAudioDevices maps UID to name and channel counts. It returns an empty map if nothing can be read.
func coreAudioDevices() map[string]coreAudioInfo {
	const max = 128
	var ids [max]C.AudioObjectID
	n := int(C.ac_device_ids(&ids[0], max))
	m := map[string]coreAudioInfo{}
	buf := (*C.char)(C.malloc(1024))
	defer C.free(unsafe.Pointer(buf))
	str := func(id C.AudioObjectID, sel C.AudioObjectPropertySelector) string {
		if C.ac_string(id, sel, buf, 1024) == 0 {
			return ""
		}
		return C.GoString(buf)
	}
	for i := 0; i < n; i++ {
		uid := str(ids[i], C.kAudioDevicePropertyDeviceUID)
		if uid == "" {
			continue
		}
		m[uid] = coreAudioInfo{
			Name:    str(ids[i], C.kAudioObjectPropertyName),
			Inputs:  int(C.ac_channels(ids[i], 0)),
			Outputs: int(C.ac_channels(ids[i], 1)),
		}
	}
	return m
}
