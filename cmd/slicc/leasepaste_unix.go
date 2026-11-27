//go:build unix && !wasip1

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var (
	pasteDisableCandidates = func() []*os.File { return []*os.File{os.Stdout, os.Stderr} }
	pasteDisableOpenTTY    = func() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_WRONLY, 0) }
)

func writePasteDisable(fallback *os.File) error {
	in, err := pasteInputStat(fallback)
	if err != nil {
		return errors.New("no terminal to disable bracketed paste")
	}
	for _, f := range pasteDisableCandidates() {
		if writePasteDisableMatch(f, in) {
			return nil
		}
	}
	if tty, e := pasteDisableOpenTTY(); e == nil {
		ok := writePasteDisableMatch(tty, in)
		_ = tty.Close()
		if ok {
			return nil
		}
	}
	if writePasteDisableMatch(fallback, in) {
		return nil
	}
	return errors.New("no terminal to disable bracketed paste")
}

func pasteInputStat(f *os.File) (unix.Stat_t, error) {
	var st unix.Stat_t
	fd, e := fileFD(f)
	if e != nil {
		return st, e
	}
	if !term.IsTerminal(fd) {
		return st, errors.New("not a terminal")
	}
	return st, unix.Fstat(fd, &st)
}

func writePasteDisableMatch(f *os.File, in unix.Stat_t) bool {
	if f == nil {
		return false
	}
	fd, e := fileFD(f)
	if e != nil || !term.IsTerminal(fd) {
		return false
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Rdev != in.Rdev || st.Ino != in.Ino {
		return false
	}
	_, e = f.Write([]byte("\x1b[?2004l"))
	return e == nil
}
