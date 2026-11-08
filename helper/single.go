package main

import (
	"syscall"
	"unsafe"
)

const errorAlreadyExists syscall.Errno = 183

// acquireSingle claims the one taskbar strip shared by every logon session.
// A Global mutex also covers an elevated second copy, which has its own window
// station and cannot be found by looking for the first copy's window.
func acquireSingle() (syscall.Handle, bool) {
	name := utf16p(`GlobalMyGoMusicTaskbarLyrics`)
	h, _, errno := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, false
	}
	if errno == errorAlreadyExists {
		procCloseHandle.Call(h)
		return 0, false
	}
	return syscall.Handle(h), true
}
