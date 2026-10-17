//go:build windows

package integration

import (
	"os"
	"testing"
	"unsafe"

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
	control, _, err := sd.Control()
	acl, _, aerr := sd.DACL()
	if err != nil || aerr != nil || control&windows.SE_DACL_PROTECTED == 0 || acl == nil || acl.AceCount != 2 {
		t.Fatalf("the socket directory's DACL is %s: want it protected, for the user and SYSTEM only", sd.String())
	}
	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || (!sid.Equals(u.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid)) {
			t.Fatalf("the socket directory's DACL is %s: want it for the user and SYSTEM only", sd.String())
		}
	}
}
