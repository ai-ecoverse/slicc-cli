//go:build !wasip1

package main

import "github.com/ai-ecoverse/slicc-cli/internal/update"

var goInstalled bool

func init() {
	b := update.Identify(version)
	version, goInstalled = b.Version, b.Go
}
