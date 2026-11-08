package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFollowJSONReportsICEFailureThenReconnects(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env := append(configEnv(t), col.env()...)
	env = append(env, "SLICC_NO_TELEMETRY=", "SLICC_NO_UPDATE_CHECK=1")
	h := newFakeHub(t)
	tr := h.addTray("icefail")
	join := h.joinURL(tr)
	h.with(func() {
		tr.IceServers = nil
		tr.OfferSDP = testOfferSDP
	})
	f := startFlowBinary(t, bin, env, join, "follow", "--json")
	c := f.next(t, "connecting")
	var connecting struct {
		Mode    string `json:"mode"`
		Attempt int    `json:"attempt"`
	}
	if json.Unmarshal([]byte(c.raw), &connecting) != nil || connecting.Mode != "tray" || connecting.Attempt != 0 {
		t.Fatalf("connecting %s", c.raw)
	}
	e := f.next(t, "error")
	var failure struct {
		Message   string `json:"message"`
		Retryable *bool  `json:"retryable"`
	}
	if json.Unmarshal([]byte(e.raw), &failure) != nil {
		t.Fatalf("error %s", e.raw)
	}
	if failure.Retryable == nil || !*failure.Retryable {
		t.Fatalf("error not retryable: %s", e.raw)
	}
	if !strings.Contains(failure.Message, "during ICE") || !strings.Contains(failure.Message, "no candidate pair") {
		t.Fatalf("error missing ICE wording: %s", e.raw)
	}
	if !strings.Contains(failure.Message, "UDP") {
		t.Fatalf("error missing UDP hint: %s", e.raw)
	}
	r := f.next(t, "reconnecting")
	var reconnecting struct {
		Attempt int       `json:"attempt"`
		RetryAt time.Time `json:"retryAt"`
		Wait    int64     `json:"wait"`
		Reason  string    `json:"reason"`
	}
	if json.Unmarshal([]byte(r.raw), &reconnecting) != nil || reconnecting.Attempt != 1 || reconnecting.RetryAt.IsZero() || reconnecting.Wait <= 0 || reconnecting.Reason != failure.Message {
		t.Fatalf("reconnecting %s", r.raw)
	}
	if !strings.Contains(f.stderr.String(), failure.Message) {
		t.Fatalf("stderr missing ICE failure: %q", f.stderr.String())
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
	for _, ev := range f.seen {
		output.WriteString(ev.raw)
	}
	failureBeacon := col.find("error")
	if failureBeacon == nil || failureBeacon.Source != "signal" || !strings.Contains(failureBeacon.Target, "during ICE") {
		t.Fatalf("no redacted ICE signaling error beacon: %+v", col.got())
	}
	for _, b := range col.got() {
		output.WriteString(b.raw)
	}
	assertNoSecret(t, output.String(), join)
	for _, needle := range []string{"192.0.2.10", "ufragsecret", "icepasswordsecretvalue0000", "turn-user-secret", "turn-credential-secret"} {
		if strings.Contains(output.String(), needle) {
			t.Fatalf("output leaks %q: %q", needle, output.String())
		}
	}
}
