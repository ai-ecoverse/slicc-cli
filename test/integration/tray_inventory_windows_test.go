//go:build windows

package integration

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTrayLocksTheRuntimeDirectoryToTheUser(t *testing.T) {
	sockets := trayRuntime(t)
	if err := os.MkdirAll(sockets, 0o755); err != nil {
		t.Fatal(err)
	}
	a := startAttachEnv(t, true, nil, "--signal", "stdio")
	trayRowFor(t, append(os.Environ(), "SLICC_NO_TELEMETRY=1"), a.cmd.Process.Pid, "")
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(sockets, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if want := "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)"; sd.String() != want {
		t.Fatalf("the socket directory's DACL is %s, want %s", sd.String(), want)
	}
}
