//go:build !wasip1

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
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
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

func cdpPorts(urls ...string) []uint16 {
	var ports []uint16
	for _, raw := range urls {
		if raw == "" {
			continue
		}
		if base, _, err := browser.NormalizeCDPURL(raw); err == nil {
			ports = append(ports, debugPort(base))
		}
	}
	return ports
}

func cdpBrowsers(ctx context.Context, seven string, extra, origins []string, pol *policy.Policy, hosts *policy.Hosts, logf func(string, ...any)) (*capcdp.Registry, error) {
	r := capcdp.NewRegistry(logf)
	if err := r.Protect(origins); err != nil {
		return nil, err
	}
	r.OnAdd(func(ctx context.Context, id string, hostports []string) {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := pol.ProtectBrowser(pctx, id, hostports, hosts); err != nil {
			logf("cdp: browser %s: %v", id, err)
		}
		logf("cdp: browser %s endpoints denied to tcp: %v", id, pol.ProtectedEndpoints(id))
	})
	r.OnCheck(func(ctx context.Context, id string, hostports []string) {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := pol.RefreshBrowser(pctx, id, hostports, hosts); err != nil {
			logf("cdp: browser %s: %v", id, err)
		}
	})
	add := func(raw string, isSeven bool) error {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := r.Add(pctx, raw, isSeven); err != nil {
			return fmt.Errorf("browser %s: %w", raw, err)
		}
		return nil
	}
	if seven != "" {
		if err := add(seven, true); err != nil {
			return nil, err
		}
	}
	for _, raw := range extra {
		if err := add(raw, false); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func followBrowsers(n *node.Node, r *capcdp.Registry, status func(string, ...any)) {
	capcdp.Register(n, r)
	ids := make([]string, 0, r.Len())
	for _, b := range r.List() {
		ids = append(ids, b.ID)
	}
	status("cdp on (%s): seven gets full control of these browsers, except tabs, cookies and storage on sliccy.ai and the browser's privileged pages", strings.Join(ids, ", "))
}

func serveBrowsers(ctx context.Context, n *node.Node, r *capcdp.Registry) {
	if r.Len() == 0 {
		return
	}
	capcdp.Register(n, r)
	go r.Watch(ctx, 2*time.Second)
}
