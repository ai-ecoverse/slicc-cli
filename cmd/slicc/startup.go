package main

import (
	"io"

	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/ai-ecoverse/slicc-cli/internal/update"
)

var quietCommands = map[string]bool{
	"--version": true, "-V": true, "version": true,
	"--help": true, "-h": true, "help": true,
}

func startup(args []string, stderr io.Writer) func() {
	if len(args) > 0 && quietCommands[args[0]] {
		return func() {}
	}
	flushTelemetry := telemetry.Start(update.IsReleaseVersion(version), args)
	flushNotice := func() {}
	if len(args) == 0 || args[0] != "update" {
		flushNotice = startUpdateNotice(stderr)
	}
	return func() {
		flushNotice()
		flushTelemetry()
	}
}
