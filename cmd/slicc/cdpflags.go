package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	capcdp "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
)

type singleCDP struct{ v *string }

func (s singleCDP) String() string {
	if s.v == nil {
		return ""
	}
	return *s.v
}

func (s singleCDP) Set(v string) error {
	if *s.v != "" {
		return errors.New("--cdp names one browser; use --cdp-extra for more")
	}
	*s.v = v
	return nil
}

func cdpBrowsers(ctx context.Context, seven string, extra []string, logf func(string, ...any)) (*capcdp.Registry, []uint16, error) {
	r := capcdp.NewRegistry(logf)
	var ports []uint16
	add := func(raw string, isSeven bool) error {
		if base, _, err := browser.NormalizeCDPURL(raw); err == nil {
			ports = append(ports, debugPort(base))
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := r.Add(pctx, raw, isSeven); err != nil {
			return fmt.Errorf("browser %s: %w", raw, err)
		}
		return nil
	}
	if seven != "" {
		if err := add(seven, true); err != nil {
			return nil, nil, err
		}
	}
	for _, raw := range extra {
		if err := add(raw, false); err != nil {
			return nil, nil, err
		}
	}
	return r, ports, nil
}

func followBrowsers(n *node.Node, r *capcdp.Registry, status func(string, ...any)) {
	capcdp.Register(n, r)
	ids := make([]string, 0, r.Len())
	for _, b := range r.List() {
		ids = append(ids, b.ID)
	}
	status("cdp on (%s): seven gets full control of these browsers", strings.Join(ids, ", "))
}

func serveBrowsers(ctx context.Context, n *node.Node, r *capcdp.Registry) {
	if r.Len() == 0 {
		return
	}
	capcdp.Register(n, r)
	go r.Watch(ctx, 2*time.Second)
}
