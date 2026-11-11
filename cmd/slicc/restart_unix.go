//go:build !wasip1 && !windows

package main

import (
	"io"
	"os"
	"syscall"
)

func (p *restartPlan) exec(stderr io.Writer) int {
	err := syscall.Exec(p.exe, append([]string{os.Args[0]}, p.args...), p.env)
	return p.failed(stderr, err)
}
