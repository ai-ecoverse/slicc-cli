//go:build !wasip1

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
	"io"
	"slices"
	"sort"
	"text/tabwriter"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

const capabilitiesSchema = 1

const contractVersion = 1

const (
	jsonDocument = "document"
	jsonEvents   = "events"
	jsonNone     = "none"
)

const (
	vocabularyEvents = "events"
	vocabularyAgent  = "agent"
)

const capabilitiesUsage = `usage: slicc capabilities [--json]

What this slicc offers a program that drives it: its commands and how each
one prints JSON, the events of --json, the agent events of prompt, watch and
wait, and the control socket's ops. It reads nothing and asks no network.

  --json   print one JSON document (see README.md)
`

type commandInfo struct {
	JSON       string `json:"json"`
	Vocabulary string `json:"vocabulary,omitempty"`
}

var commandTable = map[string]commandInfo{
	"default":      {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"attach":       {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"open":         {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"pair":         {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"unpair":       {JSON: jsonDocument},
	"follow":       {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"tray":         {JSON: jsonDocument},
	"prompt":       {JSON: jsonEvents, Vocabulary: vocabularyAgent},
	"watch":        {JSON: jsonEvents, Vocabulary: vocabularyAgent},
	"wait":         {JSON: jsonEvents, Vocabulary: vocabularyAgent},
	"abort":        {JSON: jsonNone},
	"agents":       {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"browsers":     {JSON: jsonDocument},
	"launch":       {JSON: jsonEvents, Vocabulary: vocabularyEvents},
	"update":       {JSON: jsonNone},
	"install":      {JSON: jsonNone},
	"capabilities": {JSON: jsonDocument},
	"version":      {JSON: jsonNone},
	"help":         {JSON: jsonNone},
}

var launchEvents = []string{"launched", "attached", "browser-slow", "error", "exited", "detached", "closed"}

var agentOwn = []string{agentSettled, agentOpened, agentReconnecting}

type capabilities struct {
	Schema   int                    `json:"schema"`
	Version  string                 `json:"version"`
	Contract int                    `json:"contract"`
	Commands map[string]commandInfo `json:"commands"`
	Events   capEvents              `json:"events"`
	Agent    capAgent               `json:"agent"`
	Screen   screen.Features        `json:"screen"`
	Control  capControl             `json:"control"`
}

type capEvents struct {
	Schema int      `json:"schema"`
	Names  []string `json:"names"`
}

type capAgent struct {
	Events []string `json:"events"`
	Own    []string `json:"own"`
}

type capControl struct {
	V   int      `json:"v"`
	Ops []string `json:"ops"`
}

func currentCapabilities() capabilities {
	names := append(flow.Names(), launchEvents...)
	names = append(names, screen.ProofEvent)
	slices.Sort(names)
	return capabilities{
		Schema:   capabilitiesSchema,
		Screen:   screen.Capabilities(),
		Version:  version,
		Contract: contractVersion,
		Commands: commandTable,
		Events:   capEvents{Schema: flow.Schema, Names: slices.Compact(names)},
		Agent:    capAgent{Events: slices.Clone(agentctl.Events), Own: slices.Clone(agentOwn)},
		Control:  capControl{V: ctl.V, Ops: slices.Clone(ctl.Ops)},
	}
}

func runCapabilities(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("capabilities", stderr, capabilitiesUsage)
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, capabilitiesUsage)
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc capabilities: unexpected argument %q\n\n%s", fs.Arg(0), capabilitiesUsage)
		return 2
	}
	c := currentCapabilities()
	if *asJSON {
		b, _ := json.Marshal(c)
		fmt.Fprintf(stdout, "%s\n", b)
		return 0
	}
	fmt.Fprintf(stdout, "slicc %s, contract %d\n\n", c.Version, c.Contract)
	names := make([]string, 0, len(c.Commands))
	for n := range c.Commands {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMMAND\tJSON")
	for _, n := range names {
		fmt.Fprintf(tw, "%s\t%s\n", n, c.Commands[n].JSON)
	}
	_ = tw.Flush()
	return 0
}
