//go:build !wasip1

package main

import "github.com/ai-ecoverse/slicc-cli/internal/policy"

type hostsFlag struct {
	path  string
	hosts *policy.Hosts
}

func (h *hostsFlag) String() string { return h.path }

func (h *hostsFlag) Set(path string) error {
	hosts, err := policy.LoadHosts(path)
	if err != nil {
		return err
	}
	h.path, h.hosts = path, hosts
	return nil
}
