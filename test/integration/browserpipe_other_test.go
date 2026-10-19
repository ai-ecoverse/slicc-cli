//go:build !windows

package integration

import (
	"os/exec"
	"testing"
)

func passBrowserPipe(_ *testing.T, cmd *exec.Cmd, p *browserPipe) {
	cmd.ExtraFiles = append(cmd.ExtraFiles, p.childIn, p.childOut)
	cmd.Args = append(cmd.Args, "--remote-debugging-pipe")
}
