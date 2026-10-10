//go:build !windows

package integration

import (
	"os"
	"os/exec"
)

func prepareInterruptible(*exec.Cmd) {}

func interrupt(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
