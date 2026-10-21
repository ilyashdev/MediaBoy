package cli

import (
	"os"
	"syscall"
)

// attachConsole joins the console of the terminal (or MediaBoy.com) that
// started MediaBoy. The release build is a GUI program (-H windowsgui), which
// Windows starts without a console: joining it delivers Ctrl+C, and output
// streams that are not connected anywhere are pointed at it. Streams that
// were redirected to a file or pipe, or passed on by MediaBoy.com, are kept.
func attachConsole() {
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole").Call(attachParentProcess)
	if r == 0 {
		return // already has a console, or started without a terminal
	}
	stdout := usable(syscall.STD_OUTPUT_HANDLE)
	stderr := usable(syscall.STD_ERROR_HANDLE)
	if stdout && stderr {
		return
	}
	con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	if !stdout {
		os.Stdout = con
	}
	if !stderr {
		os.Stderr = con
	}
	// The shell printed its prompt when the GUI program started; begin below it.
	con.WriteString("\r\n")
}

// usable reports whether the standard handle refers to a console, file or pipe.
func usable(std int) bool {
	h, err := syscall.GetStdHandle(std)
	if err != nil || h == 0 || h == syscall.InvalidHandle {
		return false
	}
	t, err := syscall.GetFileType(h)
	return err == nil && t != 0 // FILE_TYPE_UNKNOWN
}
