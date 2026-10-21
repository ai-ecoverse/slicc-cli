package clipboard

import (
	"context"
	"errors"
	"runtime"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32                         = windows.NewLazySystemDLL("user32.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory              = kernel32.NewProc("RtlMoveMemory")
)

func find(func(string) string, func(string) (string, error)) (*Clipboard, string) {
	if user32.Load() != nil || procOpenClipboard.Find() != nil || procSetClipboardData.Find() != nil {
		return nil, "no clipboard API in user32.dll"
	}
	return &Clipboard{Name: "the Windows clipboard", read: winRead, write: winWrite}, ""
}

func open(ctx context.Context) error {
	for {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return fail("EACCES", "another program holds the clipboard")
			}
			return fail("EIO", "the clipboard couldn't be opened: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func closeClipboard() { _, _, _ = procCloseClipboard.Call() }

func winRead(ctx context.Context) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := open(ctx); err != nil {
		return nil, err
	}
	defer closeClipboard()
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
		return nil, nil
	}
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return nil, fail("EIO", "the clipboard's text couldn't be read")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fail("EIO", "the clipboard's text couldn't be read")
	}
	defer func() { _, _, _ = procGlobalUnlock.Call(h) }()
	size, _, _ := procGlobalSize.Call(h)
	units := int(size / 2)
	if units > MaxBytes+1 {
		return nil, fail("EFBIG", "the clipboard holds more than 1 MiB of text")
	}
	if units == 0 {
		return nil, nil
	}
	buf := make([]uint16, units)
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, uintptr(units*2))
	for i, u := range buf {
		if u == 0 {
			buf = buf[:i]
			break
		}
	}
	return decode(buf)
}

func decode(buf []uint16) ([]byte, error) {
	for i := 0; i < len(buf); i++ {
		switch u := buf[i]; {
		case u >= 0xd800 && u < 0xdc00:
			if i+1 >= len(buf) || buf[i+1] < 0xdc00 || buf[i+1] >= 0xe000 {
				return nil, fail("EILSEQ", "the clipboard's text isn't valid UTF-16")
			}
			i++
		case u >= 0xdc00 && u < 0xe000:
			return nil, fail("EILSEQ", "the clipboard's text isn't valid UTF-16")
		}
	}
	return []byte(string(utf16.Decode(buf))), nil
}

func winWrite(ctx context.Context, text []byte) error {
	units := append(utf16.Encode([]rune(string(text))), 0)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(units)*2))
	if h == 0 {
		return fail("EIO", "no memory for the clipboard's text")
	}
	owned := false
	defer func() {
		if !owned {
			_, _, _ = procGlobalFree.Call(h)
		}
	}()
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return fail("EIO", "no memory for the clipboard's text")
	}
	_, _, _ = procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)*2))
	_, _, _ = procGlobalUnlock.Call(h)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := open(ctx); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, _ := procEmptyClipboard.Call(); r == 0 {
		return fail("EIO", "the clipboard couldn't be emptied")
	}
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		return fail("EIO", "the clipboard's text couldn't be set")
	}
	owned = true
	return nil
}
