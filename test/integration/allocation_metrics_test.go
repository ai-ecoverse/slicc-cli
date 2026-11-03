package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func cdpMeasurementOverlay(dir string) (string, error) {
	source, err := filepath.Abs("../../internal/caps/cdp/relay.go")
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	text := string(content)
	if strings.Count(text, "\trl.run()") != 1 {
		return "", errors.New("CDP relay measurement insertion point changed")
	}
	text = strings.Replace(text, "var httpClient =", "var measuredRelays atomic.Int64\n\nvar httpClient =", 1)
	text = strings.Replace(text, "\trl.run()", "\tr.logf(\"cdp: active relay streams: %d\", measuredRelays.Add(1))\n\tdefer func() { r.logf(\"cdp: active relay streams: %d\", measuredRelays.Add(-1)) }()\n\trl.run()", 1)
	instrumented := filepath.Join(dir, "cdp-relay-measurement.go")
	if err = os.WriteFile(instrumented, []byte(text), 0600); err != nil {
		return "", err
	}
	mapping, err := json.Marshal(map[string]any{"Replace": map[string]string{source: instrumented}})
	if err != nil {
		return "", err
	}
	overlay := filepath.Join(dir, "cdp-relay-measurement.json")
	return overlay, os.WriteFile(overlay, mapping, 0600)
}
