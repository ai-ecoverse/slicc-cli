package main

import (
	"context"
	"os"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/facade"
)

const (
	interruptAborts = false

	promptSynopsis = ""
	promptHow      = `Inside seven, slicc reaches that seven through this seven's link facade
(SLICC_LINK_URL), which links to it as this seven; it must allow remote
control from this seven (Linked devices). The link offers nothing to it.
Ctrl-C ends slicc, and the run goes on there.
`
	promptFlags = ""
	promptExit  = `this seven or this seven's link facade refuses, 130 on Ctrl-C.
`

	watchSynopsis = ""
	watchFlags    = ""
	watchExit     = `130 on Ctrl-C, 1 when the link can't be set up for good, 2 for
bad usage, 3 when that seven doesn't allow remote control from this seven or
this seven's link facade refuses.
`

	abortSynopsis = ""
	abortFlags    = ""
)

type facadeLinks struct {
	url  string
	join string
}

func (f agentFlags) links(context.Context, agentIO, func(string, ...any)) (agentLinks, error) {
	raw := os.Getenv(facade.EnvURL)
	if _, err := facade.ParseURL(raw); err != nil {
		return nil, err
	}
	if err := facade.Ready(); err != nil {
		return nil, err
	}
	return facadeLinks{url: raw, join: f.joinURL}, nil
}

func (fl facadeLinks) next(context.Context) (agentctl.Link, error) {
	return facade.New(fl.url, fl.join, facade.Dial)
}

func (facadeLinks) close() {}
