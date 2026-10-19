//go:build windows

package integration

import (
	"fmt"
	"os/exec"
	"syscall"
	"testing"
)

func passBrowserPipe(t *testing.T, cmd *exec.Cmd, p *browserPipe) {
	in, out := syscall.Handle(p.childIn.Fd()), syscall.Handle(p.childOut.Fd())
	for _, h := range []syscall.Handle{in, out} {
		if err := syscall.SetHandleInformation(h, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
			t.Fatal(err)
		}
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, in, out)
	cmd.Args = append(cmd.Args, "--remote-debugging-pipe", fmt.Sprintf("--remote-debugging-io-pipes=%d,%d", in, out))
}
