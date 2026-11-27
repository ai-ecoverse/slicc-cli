//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/term"
)

func writePasteDisable(fallback *os.File) error {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		fd, e := fileFD(f)
		if e != nil || !term.IsTerminal(fd) {
			continue
		}
		if _, e = f.Write([]byte("\x1b[?2004l")); e == nil {
			return nil
		}
	}
	if fallback != nil {
		fd, e := fileFD(fallback)
		if e == nil && term.IsTerminal(fd) {
			if _, e = fallback.Write([]byte("\x1b[?2004l")); e == nil {
				return nil
			}
		}
	}
	return errors.New("no terminal to disable bracketed paste")
}
