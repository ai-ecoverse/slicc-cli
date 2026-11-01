package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPairingSaysWhatToDoWhenNoBrowserReachesIt(t *testing.T) {
	const hint = "no browser has reached slicc yet"
	page := "https://seven.sliccy.ai/"

	t.Run("no request names WebKit and the other ways to link", func(t *testing.T) {
		e := newLoopEnv(t)
		e.env = overrideEnv(e.env, map[string]string{"SLICC_PAIRING_QUIET": "100ms"})
		o := startLoop(t, e, "pair", "--page", page)
		o.url(t)
		o.waitStderr(t, hint)
		o.waitStderr(t, "Use Chrome, Edge or Firefox, or link it through seven's Link a device")
	})

	t.Run("any request keeps it quiet", func(t *testing.T) {
		e := newLoopEnv(t)
		e.env = overrideEnv(e.env, map[string]string{"SLICC_PAIRING_QUIET": "2s"})
		o := startLoop(t, e, "pair", "--page", page)
		p := parsePairing(t, o.url(t))
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/slicc/v1/pair", p.port), "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		time.Sleep(3 * time.Second)
		if strings.Contains(o.stderr.String(), hint) {
			t.Fatalf("hinted after a request reached slicc:\n%s", o.stderr.String())
		}
	})
}
