//go:build windows

package sound

// WASAPI (Windows Core Audio) called directly through its COM method tables, so that no cgo is needed.
// Only what aimed-chime uses is declared: listing the active endpoints, and playing 16-bit PCM on one
// endpoint in shared mode, letting the audio engine convert the sample rate and channels.

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procPropVariantClear = ole32.NewProc("PropVariantClear")
)

var (
	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioClient         = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioRenderClient   = windows.GUID{Data1: 0xF294ACFC, Data2: 0x3146, Data3: 0x4483, Data4: [8]byte{0xA7, 0xBF, 0xAD, 0xDC, 0xA7, 0xC2, 0x60, 0xE2}}

	// PKEY_Device_FriendlyName, e.g. "Speakers (USB Audio Device)".
	pkeyDeviceFriendlyName = propertyKey{windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, 14}
)

const (
	eRender                             = 0
	eCapture                            = 1
	deviceStateActive                   = 1
	clsctxAll                           = 0x17
	stgmRead                            = 0
	vtLPWSTR                            = 31
	coinitMultithreaded                 = 0
	audclntShareModeShared              = 0
	audclntStreamFlagsAutoConvertPCM    = 0x80000000
	audclntStreamFlagsSrcDefaultQuality = 0x08000000

	// Buffer length in 100-nanosecond units (200 ms).
	bufferDuration = 200 * 10000
)

// Method indexes in the COM method tables (IUnknown's three come first).
const (
	mRelease = 2

	mEnumAudioEndpoints = 3 // IMMDeviceEnumerator
	mGetDevice          = 5

	mCollectionGetCount = 3 // IMMDeviceCollection
	mCollectionItem     = 4

	mActivate          = 3 // IMMDevice
	mOpenPropertyStore = 4
	mGetID             = 5

	mGetValue = 5 // IPropertyStore

	mInitialize        = 3 // IAudioClient
	mGetBufferSize     = 4
	mGetStreamLatency  = 5
	mGetCurrentPadding = 6
	mGetMixFormat      = 8
	mStart             = 10
	mStop              = 11
	mGetService        = 14

	mGetBuffer     = 3 // IAudioRenderClient
	mReleaseBuffer = 4
)

// HRESULTs that are worth a readable message.
var hresultText = map[uint32]string{
	0x80010106: "COM is already initialized in another mode",
	0x80070490: "the device is not found (unplugged?)",
	0x88890004: "the device was invalidated (unplugged or disabled?)",
	0x88890008: "the audio format is not supported",
	0x8889000A: "the device is in use by another application in exclusive mode",
	0x88890010: "the audio service is not running",
}

type hresult uint32

func (h hresult) Error() string {
	if s, ok := hresultText[uint32(h)]; ok {
		return fmt.Sprintf("%s (HRESULT 0x%08X)", s, uint32(h))
	}
	return fmt.Sprintf("HRESULT 0x%08X", uint32(h))
}

func check(r uintptr) error {
	if int32(r) < 0 {
		return hresult(r)
	}
	return nil
}

// comObject is a COM interface pointer: its first field points to the method table.
type comObject struct {
	vtbl *[32]uintptr
}

func (o *comObject) call(method int, args ...uintptr) error {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return check(r)
}

func (o *comObject) release() {
	syscall.SyscallN(o.vtbl[mRelease], uintptr(unsafe.Pointer(o)))
}

// int64Arg passes a 64-bit argument by value, which takes two slots on 32-bit Windows.
func int64Arg(v int64) []uintptr {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return []uintptr{uintptr(v)}
	}
	return []uintptr{uintptr(uint32(v)), uintptr(uint32(v >> 32))}
}

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// propVariant is large enough for PROPVARIANT on both 32- and 64-bit Windows; only VT_LPWSTR is read.
type propVariant struct {
	vt  uint16
	_   [3]uint16
	str *uint16
	_   [2]uintptr
}

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

// withEnumerator runs f on a locked OS thread with COM initialized and an IMMDeviceEnumerator.
func withEnumerator(f func(en *comObject) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	if err := check(r); err == nil {
		defer procCoUninitialize.Call()
	} else if uint32(r) != 0x80010106 { // already initialized in another mode is still usable
		return fmt.Errorf("initialize COM: %w", err)
	}
	var en *comObject
	r, _, _ = procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&en)))
	if err := check(r); err != nil {
		return fmt.Errorf("create the audio device enumerator: %w", err)
	}
	defer en.release()
	return f(en)
}

// wasapiDevices lists the active output endpoints, then the active input endpoints.
// UID is the endpoint ID, which is what playPCM opens; Index is only the position in this list.
func wasapiDevices() ([]Device, error) {
	var devs []Device
	err := withEnumerator(func(en *comObject) error {
		for _, flow := range []uintptr{eRender, eCapture} {
			var col *comObject
			if err := en.call(mEnumAudioEndpoints, flow, deviceStateActive, uintptr(unsafe.Pointer(&col))); err != nil {
				return fmt.Errorf("list audio devices: %w", err)
			}
			var n uint32
			if err := col.call(mCollectionGetCount, uintptr(unsafe.Pointer(&n))); err != nil {
				col.release()
				return fmt.Errorf("list audio devices: %w", err)
			}
			for i := uint32(0); i < n; i++ {
				var dev *comObject
				if col.call(mCollectionItem, uintptr(i), uintptr(unsafe.Pointer(&dev))) != nil {
					continue
				}
				d := Device{Index: len(devs), UID: endpointID(dev), Name: friendlyName(dev)}
				ch := mixChannels(dev)
				if flow == eRender {
					// If the channels cannot be read, leave it unknown so that it is still a candidate.
					d.Outputs, d.Known = ch, ch > 0
				} else {
					d.Inputs, d.Known = ch, true
				}
				dev.release()
				if d.UID != "" {
					devs = append(devs, d)
				}
			}
			col.release()
		}
		return nil
	})
	return devs, err
}

func endpointID(dev *comObject) string {
	var s *uint16
	if dev.call(mGetID, uintptr(unsafe.Pointer(&s))) != nil || s == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(s))
	return windows.UTF16PtrToString(s)
}

func friendlyName(dev *comObject) string {
	var ps *comObject
	if dev.call(mOpenPropertyStore, stgmRead, uintptr(unsafe.Pointer(&ps))) != nil {
		return "(null)"
	}
	defer ps.release()
	var pv propVariant
	if ps.call(mGetValue, uintptr(unsafe.Pointer(&pkeyDeviceFriendlyName)), uintptr(unsafe.Pointer(&pv))) != nil {
		return "(null)"
	}
	defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if pv.vt != vtLPWSTR || pv.str == nil {
		return "(null)"
	}
	return windows.UTF16PtrToString(pv.str)
}

// mixChannels is the number of channels the audio engine mixes for the endpoint, or 0 if it cannot be read.
func mixChannels(dev *comObject) int {
	var ac *comObject
	if dev.call(mActivate, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&ac))) != nil {
		return 0
	}
	defer ac.release()
	var wf *waveFormatEx
	if ac.call(mGetMixFormat, uintptr(unsafe.Pointer(&wf))) != nil || wf == nil {
		return 0
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(wf))
	return int(wf.Channels)
}

// playPCM plays interleaved 16-bit PCM on the endpoint with the given ID, and returns after the
// last sample has left the device.
func playPCM(ctx context.Context, id string, rate, channels int, pcm []byte) error {
	idp, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return err
	}
	return withEnumerator(func(en *comObject) error {
		var dev *comObject
		if err := en.call(mGetDevice, uintptr(unsafe.Pointer(idp)), uintptr(unsafe.Pointer(&dev))); err != nil {
			return fmt.Errorf("open the device: %w", err)
		}
		defer dev.release()
		var ac *comObject
		if err := dev.call(mActivate, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&ac))); err != nil {
			return fmt.Errorf("open the device: %w", err)
		}
		defer ac.release()

		frame := channels * 2
		wf := waveFormatEx{
			FormatTag:      1, // WAVE_FORMAT_PCM
			Channels:       uint16(channels),
			SamplesPerSec:  uint32(rate),
			AvgBytesPerSec: uint32(rate * frame),
			BlockAlign:     uint16(frame),
			BitsPerSample:  16,
		}
		args := []uintptr{audclntShareModeShared, audclntStreamFlagsAutoConvertPCM | audclntStreamFlagsSrcDefaultQuality}
		args = append(args, int64Arg(bufferDuration)...)
		args = append(args, int64Arg(0)...)
		args = append(args, uintptr(unsafe.Pointer(&wf)), 0)
		if err := ac.call(mInitialize, args...); err != nil {
			return fmt.Errorf("initialize the device: %w", err)
		}
		var bufFrames uint32
		if err := ac.call(mGetBufferSize, uintptr(unsafe.Pointer(&bufFrames))); err != nil {
			return fmt.Errorf("initialize the device: %w", err)
		}
		var rc *comObject
		if err := ac.call(mGetService, uintptr(unsafe.Pointer(&iidIAudioRenderClient)), uintptr(unsafe.Pointer(&rc))); err != nil {
			return fmt.Errorf("initialize the device: %w", err)
		}
		defer rc.release()

		total, pos, started := len(pcm)/frame, 0, false
		defer func() {
			if started {
				ac.call(mStop)
			}
		}()
		var drainUntil time.Time
		for {
			var padding uint32
			if err := ac.call(mGetCurrentPadding, uintptr(unsafe.Pointer(&padding))); err != nil {
				return fmt.Errorf("play: %w", err)
			}
			if pos < total {
				if n := min(int(bufFrames-padding), total-pos); n > 0 {
					var p *byte
					if err := rc.call(mGetBuffer, uintptr(n), uintptr(unsafe.Pointer(&p))); err != nil {
						return fmt.Errorf("play: %w", err)
					}
					copy(unsafe.Slice(p, n*frame), pcm[pos*frame:])
					if err := rc.call(mReleaseBuffer, uintptr(n), 0); err != nil {
						return fmt.Errorf("play: %w", err)
					}
					pos += n
					if !started {
						if err := ac.call(mStart); err != nil {
							return fmt.Errorf("play: %w", err)
						}
						started = true
					}
				}
			} else {
				if padding == 0 {
					break
				}
				// Guard against a device that stops consuming without reporting an error.
				if drainUntil.IsZero() {
					drainUntil = time.Now().Add(bufferDuration*100*time.Nanosecond + 2*time.Second)
				} else if time.Now().After(drainUntil) {
					break
				}
			}
			if err := sleep(ctx, 10*time.Millisecond); err != nil {
				return err
			}
		}
		// The buffer is empty, but the device still holds its own latency's worth of sound.
		var latency int64
		ac.call(mGetStreamLatency, uintptr(unsafe.Pointer(&latency)))
		return sleep(ctx, time.Duration(latency)*100*time.Nanosecond+200*time.Millisecond)
	})
}
