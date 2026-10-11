package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/ai-ecoverse/slicc-cli/internal/update"
)

const updateUsage = `usage: slicc update [--check]

Replaces this binary with the latest release of ai-ecoverse/slicc-cli.
--check only reports whether a newer release exists. A copy installed
with go install, npm or an app bundle is not replaced; slicc update
prints how to update it instead.
`

var launcher = update.TakeLauncher()

func runUpdate(args []string, stdout, stderr io.Writer) int {
	check := false
	for _, arg := range args {
		switch arg {
		case "--check":
			check = true
		case "--help", "-h":
			fmt.Fprint(stdout, updateUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "slicc update: unknown argument %q\n\n%s", arg, updateUsage)
			return 2
		}
	}
	exe, err := update.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "slicc update: locating this binary: %s\n", err)
		return 1
	}
	if m := update.ManagedBy(exe, launcher, goInstalled); m != update.Unmanaged {
		fmt.Fprintf(stderr, "slicc update: %s\n", m.Advice(exe))
		return 1
	}
	update.RemoveStale(exe)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	checker := update.NewChecker()
	rel, err := checker.Latest(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "slicc update: %s\n", err)
		telemetry.ReportError(telemetry.Update, err)
		return 1
	}
	return applyUpdate(ctx, checker, rel, exe, check, stdout, stderr)
}

func applyUpdate(ctx context.Context, checker *update.Checker, rel *update.Release, exe string, check bool, stdout, stderr io.Writer) int {
	if !update.IsReleaseVersion(version) {
		if check {
			fmt.Fprintf(stdout, "latest release: %s (this is a development build, %s)\n", rel.Version, version)
			return 0
		}
		fmt.Fprintf(stderr, "slicc update: refusing to replace a development build (%s) with release %s\n", version, rel.Version)
		return 1
	}
	if !update.IsNewer(rel.Version, version) {
		fmt.Fprintf(stdout, "slicc %s is up to date\n", update.Normalize(version))
		return 0
	}
	if check {
		fmt.Fprintln(stdout, update.Notice(update.Unmanaged, rel.Version, version))
		return 0
	}
	fmt.Fprintf(stdout, "updating slicc %s to %s\n", update.Normalize(version), rel.Version)
	if err := checker.Apply(ctx, rel, exe); err != nil {
		fmt.Fprintf(stderr, "slicc update: %s\n", err)
		telemetry.ReportError(telemetry.Update, err)
		return 1
	}
	fmt.Fprintf(stdout, "updated %s to slicc %s\n", exe, rel.Version)
	return 0
}

func startUpdateNotice(stderr io.Writer) func() {
	exe, err := update.Executable()
	if err != nil {
		return func() {}
	}
	m := update.ManagedBy(exe, launcher, goInstalled)
	if m == update.Unmanaged {
		update.RemoveStale(exe)
	}
	return update.NewNotifier(version, m, stderr).Start()
}
