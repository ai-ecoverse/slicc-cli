//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const browsersUsage = `usage: slicc browsers [--json] [--cdp <url>]... [--no-bundle-scan]

List running CDP endpoints and installed browsers, best match first.

  --json             print one JSON document (see README.md)
  --cdp <url>        also check this CDP endpoint (repeatable)
  --no-bundle-scan   macOS: find Electron apps by known bundle id only,
                     without reading inside app bundles
`

func runBrowsers(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("browsers", stderr, browsersUsage)
	asJSON := fs.Bool("json", false, "")
	noScan := fs.Bool("no-bundle-scan", false, "")
	var cdp stringList
	fs.Var(&cdp, "cdp", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, browsersUsage)
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc browsers: unexpected argument %q\n\n%s", fs.Arg(0), browsersUsage)
		return 2
	}
	r := browser.Discover(context.Background(), browser.Options{CDPURLs: cdp, NoBundleScan: *noScan})
	for _, err := range r.Errors {
		fmt.Fprintf(stderr, "slicc browsers: %v\n", err)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(browser.NewReport(r.Entries)); err != nil {
			fmt.Fprintf(stderr, "slicc browsers: %v\n", err)
			return 1
		}
		return 0
	}
	printEntries(stdout, r.Entries)
	return 0
}

func printEntries(w io.Writer, entries []browser.Entry) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "no browsers found")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tVERSION\tSTATE\tWHERE")
	for _, e := range entries {
		version := e.Version
		if version == "" {
			version = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.Name, version, entryState(e), entryWhere(e))
	}
	_ = tw.Flush()
}

func entryState(e browser.Entry) string {
	if e.Kind == browser.KindEndpoint {
		var parts []string
		parts = append(parts, "running")
		if e.Endpoint.SevenOpen {
			parts = append(parts, "seven open")
		}
		if e.Endpoint.WSOnly {
			parts = append(parts, "websocket only")
		}
		if e.Endpoint.Six {
			parts = append(parts, "SLICC six's profile")
		}
		return strings.Join(parts, ", ")
	}
	parts := []string{"installed"}
	if e.Installed.Engine == browser.EngineElectron {
		parts = append(parts, "electron")
	}
	if e.Installed.DebugSupport == browser.DebugDisabled {
		parts = append(parts, "debugging disabled")
	}
	return strings.Join(parts, ", ")
}

func entryWhere(e browser.Entry) string {
	if e.Kind == browser.KindEndpoint {
		return e.Endpoint.CDP
	}
	if e.Installed.Via != "" {
		return e.Installed.Via + " " + e.Installed.Package
	}
	return e.Installed.Path
}
