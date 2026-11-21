//go:build !windows

package ui

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// On macOS and Linux, the window size comes from stty against /dev/tty,
// which keeps the typed picker free of a terminal package.

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
