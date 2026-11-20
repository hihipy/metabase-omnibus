//go:build !windows

package ui

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// On macOS and Linux, raw mode goes through stty against /dev/tty, which keeps
// the tool free of a terminal package.

type rawTerm struct {
	tty   *os.File
	saved string
}

func openRaw() (*rawTerm, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("no /dev/tty")
	}
	saved, err := stty(tty, "-g")
	if err != nil {
		tty.Close()
		return nil, errors.New("stty -g: " + err.Error())
	}
	if _, err := stty(tty, "raw", "-echo"); err != nil {
		tty.Close()
		return nil, errors.New("stty raw: " + err.Error())
	}
	return &rawTerm{tty: tty, saved: saved}, nil
}

func (t *rawTerm) Read(b []byte) (int, error) { return t.tty.Read(b) }

func (t *rawTerm) Restore() {
	stty(t.tty, t.saved)
	t.tty.Close()
}

func (t *rawTerm) Size() (rows, cols int) { return ttySize(t.tty) }

// termSize reports the window size without entering raw mode.
func termSize() (rows, cols int) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 0, 0
	}
	defer tty.Close()
	return ttySize(tty)
}

func ttySize(tty *os.File) (rows, cols int) {
	out, err := stty(tty, "size")
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0
	}
	rows, _ = strconv.Atoi(f[0])
	cols, _ = strconv.Atoi(f[1])
	return rows, cols
}

func stty(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func ansiOK() bool {
	if t := os.Getenv("TERM"); t == "" || t == "dumb" {
		return false
	}
	info, err := os.Stderr.Stat() // prompts and progress go to stderr
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
