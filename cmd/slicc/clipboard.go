//go:build !wasip1

package main

import (
	"errors"
	"flag"

	capclip "github.com/ai-ecoverse/slicc-cli/internal/caps/clipboard"
	hostclip "github.com/ai-ecoverse/slicc-cli/internal/clipboard"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

const clipboardReadByDefault = false

const clipboardLocalUsage = `  --clipboard          also let seven read this machine's clipboard
                       (clipboard-read); setting it (clipboard) is on by
                       default
  --no-clipboard       don't offer this machine's clipboard to seven
`

const clipboardFollowUsage = `  --clipboard-write    let seven set this machine's clipboard (clipboard)
  --clipboard          also let seven read it (clipboard-read; implies
                       --clipboard-write)
`

type clipFlags struct {
	read  bool
	write bool
	off   bool
}

func (c *clipFlags) register(fs *flag.FlagSet, follow bool) {
	fs.BoolVar(&c.read, "clipboard", false, "")
	fs.BoolVar(&c.off, "no-clipboard", false, "")
	if follow {
		fs.BoolVar(&c.write, "clipboard-write", false, "")
	}
}

func (c clipFlags) check() error {
	if c.off && (c.read || c.write) {
		return errors.New("--no-clipboard can't go with --clipboard or --clipboard-write")
	}
	return nil
}

func (c clipFlags) local() tui.Clip {
	switch {
	case c.off:
		return tui.ClipOff
	case c.read || clipboardReadByDefault:
		return tui.ClipRead
	}
	return tui.ClipWrite
}

func (c clipFlags) follow() tui.Clip {
	switch {
	case c.off:
		return tui.ClipOff
	case c.read:
		return tui.ClipRead
	case c.write:
		return tui.ClipWrite
	}
	return tui.ClipOff
}

func clipboardName() (string, string) {
	c, why := hostclip.Find()
	if c == nil {
		return "", why
	}
	return c.Name, ""
}

func serveClipboard(n *node.Node, mode tui.Clip, status func(string, ...any)) {
	if mode == tui.ClipOff {
		return
	}
	c, why := hostclip.Find()
	if c == nil {
		status("clipboard: %s; not offering it", why)
		return
	}
	capclip.Register(n, capclip.Options{Clipboard: c, Read: mode == tui.ClipRead})
	caps := "clipboard"
	if mode == tui.ClipRead {
		caps = "clipboard, clipboard-read"
	}
	status("clipboard: offering %s (%s)", c.Name, caps)
}
