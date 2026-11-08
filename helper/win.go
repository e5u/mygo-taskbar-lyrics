package main

import (
	"syscall"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")
)

var (
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateLayeredWindow           = user32.NewProc("UpdateLayeredWindow")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procIsWindowVisible               = user32.NewProc("IsWindowVisible")
	procFindWindowW                   = user32.NewProc("FindWindowW")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procSetTimer                      = user32.NewProc("SetTimer")
	procKillTimer                     = user32.NewProc("KillTimer")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procDrawTextW                     = user32.NewProc("DrawTextW")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procBitBlt                        = gdi32.NewProc("BitBlt")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDpiAwareness        = shcore.NewProc("SetProcessDpiAwareness")

	procCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procDeleteDC              = gdi32.NewProc("DeleteDC")
	procCreateFontW           = gdi32.NewProc("CreateFontW")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	procGetTextMetricsW       = gdi32.NewProc("GetTextMetricsW")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW     = kernel32.NewProc("CreateMutexW")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
)

const (
	wsPopup = 0x80000000

	wsExLayered     = 0x00080000
	wsExTopmost     = 0x00000008
	wsExNoActivate  = 0x08000000
	wsExToolWindow  = 0x00000080
	wsExTransparent = 0x00000020

	swHide           = 0
	swShowNoActivate = 4

	wmTimer   = 0x0113
	wmDestroy = 0x0002

	ulwAlpha = 0x00000002

	dtLeft        = 0x0000
	dtCenter      = 0x0001
	dtRight       = 0x0002
	dtVCenter     = 0x0004
	dtSingleLine  = 0x0020
	dtNoPrefix    = 0x0800
	dtEndEllipsis = 0x8000

	transparentBk = 1
	antialiasedQ  = 4
)

type rect32 struct {
	left, top, right, bottom int32
}

type point32 struct {
	x, y int32
}

type size32 struct {
	cx, cy int32
}

type blend32 struct {
	op, flags, alpha, format byte
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

// rgb builds a COLORREF (0x00BBGGRR) for GDI calls.
func rgb(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

func enableDPIAwareness() {
	// Per-monitor v2 so GetWindowRect and our pixels agree; -4 is the v2 context handle.
	if procSetProcessDpiAwarenessContext.Find() == nil {
		if r, _, _ := procSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	if procSetProcessDpiAwareness.Find() == nil {
		if r, _, _ := procSetProcessDpiAwareness.Call(2); r == 0 {
			return
		}
	}
	procSetProcessDPIAware.Call()
}

func dpiForWindow(h uintptr) int32 {
	if procGetDpiForWindow.Find() != nil {
		return 96
	}
	d, _, _ := procGetDpiForWindow.Call(h)
	if d < 48 || d > 480 {
		return 96
	}
	return int32(d)
}

func utf16p(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}
