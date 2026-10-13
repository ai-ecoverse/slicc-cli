package browser

import (
	"runtime"
	"slices"
	"strconv"
	"strings"
)

func ActivePorts(o Options) []uint16 {
	d := discoveryFor(o)
	o = o.withDefaults()
	var out []uint16
	for _, c := range activePortCandidates(o, o.platform(runtime.GOOS), d) {
		_, raw, _ := strings.Cut(strings.TrimPrefix(c.base, "http://"), ":")
		p, err := strconv.ParseUint(raw, 10, 16)
		if err == nil && p > 0 && !slices.Contains(out, uint16(p)) {
			out = append(out, uint16(p))
		}
	}
	return out
}
