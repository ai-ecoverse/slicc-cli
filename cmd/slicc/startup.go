package main

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/ai-ecoverse/slicc-cli/internal/update"
)

var quietCommands = map[string]bool{
	"--version": true, "-V": true, "version": true,
	"--help": true, "-h": true, "help": true,
}

func quiet(args []string) bool {
	if len(args) > 0 && quietCommands[args[0]] {
		return true
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "-help" {
			return true
		}
	}
	return false
}

func startup(args []string, stderr io.Writer) func() {
	if quiet(args) {
		return func() {}
	}
	if isDefault(args) || args[0] == "browsers" || args[0] == "launch" {
		for _, n := range browser.MigrateProfiles(browser.Options{}) {
			fmt.Fprintf(stderr, "slicc: %s\n", n)
		}
	}
	targs := args
	noticeOut := stderr
	if isDefault(args) {
		targs = nil
		noticeOut = &notice
	}
	flushTelemetry := telemetry.Start(update.IsReleaseVersion(version), targs)
	flushNotice := func() {}
	if len(args) == 0 || args[0] != "update" {
		flushNotice = startUpdateNotice(noticeOut)
	}
	return func() {
		flushNotice()
		flushTelemetry()
	}
}

type noticeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (n *noticeBuffer) Write(p []byte) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.buf.Write(p)
}

var notice noticeBuffer

func takeNotice() string {
	notice.mu.Lock()
	defer notice.mu.Unlock()
	s := notice.buf.String()
	notice.buf.Reset()
	return s
}

func flushNotice(w io.Writer) {
	if s := takeNotice(); s != "" {
		_, _ = io.WriteString(w, s)
	}
}
