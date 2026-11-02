package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	nativeScreen "github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"golang.org/x/sys/unix"
)

func TestScreenBootstrapRefusesReadWriteDescriptorWithoutClosingIt(t *testing.T) {
	if os.Getenv("SLICC_TEST_READWRITE_BOOTSTRAP") == "1" {
		c, e := nativeScreen.Start(context.Background(), node.New(node.Config{}), func(string, string, string, string) error { return nil })
		if e == nil || c != nil {
			t.Fatal("read-write descriptor accepted as bootstrap")
		}
		if _, e = unix.FcntlInt(3, unix.F_GETFL, 0); e != nil {
			t.Fatal("bootstrap rejection closed an unrelated descriptor")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "bootstrap")
	if e := unix.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	file, e := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = file.Close() }()
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestScreenBootstrapRefusesReadWriteDescriptorWithoutClosingIt$")
	command.Env = append(os.Environ(), "SLICC_TEST_READWRITE_BOOTSTRAP=1")
	command.ExtraFiles = []*os.File{file}
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("bootstrap child: %v\n%s", e, output)
	}
}
