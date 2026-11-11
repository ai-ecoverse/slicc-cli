//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	ossignal "os/signal"
)

func (p *restartPlan) exec(stderr io.Writer) int {
	cmd := exec.Command(p.exe, p.args...)
	cmd.Env = p.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	ossignal.Ignore(os.Interrupt)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return p.failed(stderr, err)
	}
	return 0
}
