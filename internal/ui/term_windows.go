//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

// On Windows, the window size and colour support come from the console API in
// kernel32, called through the standard library's syscall package.

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableProcessedOutput = 0x0001
	enableVTProcessing    = 0x0004
)

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type screenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

func setConsoleMode(h syscall.Handle, mode uint32) error {
	r, _, err := procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	if r == 0 {
		return err
	}
	return nil
}

func termSize() (rows, cols int) {
	var info screenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(syscall.Handle(os.Stderr.Fd())), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, 0
	}
	return int(info.Window.Bottom-info.Window.Top) + 1, int(info.Window.Right-info.Window.Left) + 1
}

// ansiOK turns on escape-sequence processing for stderr, where all prompts go, and reports whether
// the console accepted it.
func ansiOK() bool {
	h := syscall.Handle(os.Stderr.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	return setConsoleMode(h, mode|enableProcessedOutput|enableVTProcessing) == nil
}
