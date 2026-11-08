//go:build !windows

package integration

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFollowJSONPeerLossAfterLinkedIsNotAnICEError(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	b, p := launchWithPage(t, pageServer(t).URL+"/index.html")
	env := append(configEnv(t), col.env()...)
	env = append(env, "SLICC_NO_TELEMETRY=", "SLICC_NO_UPDATE_CHECK=1")
	h := newFakeHub(t)
	tr := h.addTray("icelinked")
	join := h.joinURL(tr)
	_ = newTrayLeader(t, h, p, tr)
	f := startFlowBinary(t, bin, env, join, "follow", "--json")
	if c := f.next(t, "connecting"); !strings.Contains(c.raw, `"mode":"tray"`) {
		t.Fatalf("connecting %s", c.raw)
	}
	f.next(t, "linked")
	p.waitUp(t, "")
	if err := syscall.Kill(-b.cmd.Process.Pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-b.cmd.Process.Pid, syscall.SIGCONT) })
	lost := f.next(t, "link-lost")
	var loss struct {
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal([]byte(lost.raw), &loss) != nil {
		t.Fatalf("link-lost %s", lost.raw)
	}
	if strings.Contains(lost.raw, "UDP") || strings.Contains(loss.Message, "UDP") || strings.Contains(loss.Reason, "UDP") {
		t.Fatalf("link-lost carries the UDP hint: %s", lost.raw)
	}
	re := f.next(t, "reconnecting")
	var reconnecting struct {
		Attempt int       `json:"attempt"`
		RetryAt time.Time `json:"retryAt"`
		Wait    int64     `json:"wait"`
		Reason  string    `json:"reason"`
	}
	if json.Unmarshal([]byte(re.raw), &reconnecting) != nil || reconnecting.Attempt != 1 || reconnecting.RetryAt.IsZero() || reconnecting.Wait <= 0 {
		t.Fatalf("reconnecting %s", re.raw)
	}
	if strings.Contains(re.raw, "UDP") || strings.Contains(reconnecting.Reason, "UDP") {
		t.Fatalf("reconnecting carries the UDP hint: %s", re.raw)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(-b.cmd.Process.Pid, syscall.SIGCONT)
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
	for _, ev := range f.seen {
		if ev.Event == "error" {
			t.Fatalf("unexpected error event after a linked peer loss: %v", f.kinds())
		}
	}
	var output strings.Builder
	output.WriteString(f.stderr.String())
	for _, ev := range f.seen {
		output.WriteString(ev.raw)
	}
	if col.find("error") != nil {
		t.Fatalf("telemetry reported an error for a linked peer loss: %+v", col.got())
	}
	for _, b := range col.got() {
		output.WriteString(b.raw)
	}
	if strings.Contains(output.String(), "UDP") {
		t.Fatalf("UDP hint leaked after a linked peer loss: %q", output.String())
	}
	assertNoSecret(t, output.String(), join)
}
