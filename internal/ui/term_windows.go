//go:build windows

package ui

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// On Windows, raw mode goes through the console API in kernel32, called
// through the standard library's syscall package so the tool stays
// dependency-free. Virtual-terminal input makes arrow keys arrive as the same
// escape sequences macOS and Linux send, so one key decoder serves both.

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableProcessedInput  = 0x0001
	enableLineInput       = 0x0002
	enableEchoInput       = 0x0004
	enableVTInput         = 0x0200
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

type rawTerm struct {
	in, out         syscall.Handle
	inMode, outMode uint32
}

func openRaw() (*rawTerm, error) {
	t := &rawTerm{
		in:  syscall.Handle(os.Stdin.Fd()),
		out: syscall.Handle(os.Stderr.Fd()),
	}
	if err := syscall.GetConsoleMode(t.in, &t.inMode); err != nil {
		return nil, errors.New("input is not a console")
	}
	if err := syscall.GetConsoleMode(t.out, &t.outMode); err != nil {
		return nil, errors.New("output is not a console")
	}
	raw := t.inMode&^(enableEchoInput|enableLineInput|enableProcessedInput) | enableVTInput
	if err := setConsoleMode(t.in, raw); err != nil {
		return nil, errors.New("console does not support raw input: " + err.Error())
	}
	if err := setConsoleMode(t.out, t.outMode|enableProcessedOutput|enableVTProcessing); err != nil {
		setConsoleMode(t.in, t.inMode)
		return nil, errors.New("console does not support escape sequences: " + err.Error())
	}
	return t, nil
}

func (t *rawTerm) Read(b []byte) (int, error) { return os.Stdin.Read(b) }

func (t *rawTerm) Restore() {
	setConsoleMode(t.in, t.inMode)
	setConsoleMode(t.out, t.outMode)
}

func (t *rawTerm) Size() (rows, cols int) { return termSize() }

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
