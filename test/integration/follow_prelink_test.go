package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFollowJSONReconnectsAfterAnOfferItCannotAnswer(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env := append(configEnv(t), col.env()...)
	env = append(env, "SLICC_DEBUG=1", "SLICC_NO_TELEMETRY=", "SLICC_NO_UPDATE_CHECK=1")
	h := newFakeHub(t)
	tr := h.addTray("malformed")
	join := h.joinURL(tr)
	h.with(func() {
		tr.IceServers = nil
		tr.OfferSDP = strings.Replace(testOfferSDP, "s=-\r\n", "s=-\r\nb=AS:"+tr.Token+"\r\n", 1)
	})
	f := startFlowBinary(t, bin, env, join, "follow", "--json")
	for attempt := range 2 {
		c := f.next(t, "connecting")
		var connecting struct {
			Mode    string `json:"mode"`
			Attempt int    `json:"attempt"`
		}
		if json.Unmarshal([]byte(c.raw), &connecting) != nil || connecting.Mode != "tray" || connecting.Attempt != attempt {
			t.Fatalf("connecting %s", c.raw)
		}
		e := f.next(t, "error")
		var failure struct {
			Message   string `json:"message"`
			Retryable *bool  `json:"retryable"`
		}
		if json.Unmarshal([]byte(e.raw), &failure) != nil || !strings.Contains(failure.Message, "<redacted>") || failure.Retryable == nil || !*failure.Retryable {
			t.Fatalf("error %s", e.raw)
		}
		r := f.next(t, "reconnecting")
		var reconnecting struct {
			Attempt int       `json:"attempt"`
			RetryAt time.Time `json:"retryAt"`
			Wait    int64     `json:"wait"`
			Reason  string    `json:"reason"`
		}
		if json.Unmarshal([]byte(r.raw), &reconnecting) != nil || reconnecting.Attempt != attempt+1 || reconnecting.RetryAt.IsZero() || reconnecting.Wait <= 0 || reconnecting.Reason != failure.Message {
			t.Fatalf("reconnecting %s", r.raw)
		}
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
	var output strings.Builder
	output.WriteString(f.stderr.String())
	for _, e := range f.seen {
		output.WriteString(e.raw)
		if e.Event == "linked" || e.Event == "link-lost" {
			t.Fatalf("reported a link for a malformed offer: %s", e.raw)
		}
	}
	failure := col.find("error")
	if failure == nil || failure.Source != "signal" || !strings.Contains(failure.Target, "<redacted>") {
		t.Fatalf("no redacted signaling error beacon: %+v", col.got())
	}
	for _, b := range col.got() {
		output.WriteString(b.raw)
	}
	assertNoSecret(t, output.String(), join)
	h.with(func() {
		if tr.attaches < 2 || len(tr.answers) != 0 {
			t.Errorf("attaches %d, answers %d", tr.attaches, len(tr.answers))
		}
	})
}
