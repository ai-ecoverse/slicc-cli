package integration

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func TestTrayRejoinStoresTheNewURLForTheNextDial(t *testing.T) {
	h := newFakeHub(t)
	old, fresh := h.addTray("old"), h.addTray("fresh")
	path := filepath.Join(t.TempDir(), "slicc", "tray-join.json")
	state := tray.OpenState(path)
	logs := &logBuffer{}
	cfg := fastConfig(h.joinURL(old), logs)
	cfg.State = state
	var changed []string
	cfg.OnJoinURLChanged = func(u string) { changed = append(changed, u) }
	f, s := dial(t, cfg)
	if err := f.Rejoin(" " + h.joinURL(fresh) + "\n"); err != nil {
		t.Fatal(err)
	}
	if ev := recv(t, s); ev.Kind != tray.EventOffer {
		t.Fatalf("the current session ended: %v", ev.Kind)
	}
	if state.Current(h.joinURL(old)) != h.joinURL(fresh) || len(changed) != 1 || changed[0] != h.joinURL(fresh) {
		t.Fatal("rejoin not persisted")
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("state mode %v %v", fi, err)
		}
	}
	if _, err := f.Dial(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if len(attachRequests(h, old.Token)) != 1 || len(attachRequests(h, fresh.Token)) != 1 {
		t.Fatal("the next dial did not use the new join URL")
	}
	f2, err := tray.NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !f2.Redirected() {
		t.Fatal("a restarted follower does not start from the new join URL")
	}
	assertRedacted(t, logs.String(), h, old, fresh)
	if f.Redact(h.joinURL(fresh)) != "<redacted>" {
		t.Fatal("new join URL not redacted")
	}
}

func TestTrayRejoinWithoutStateUpdatesTheFollower(t *testing.T) {
	h := newFakeHub(t)
	old, fresh := h.addTray("old"), h.addTray("fresh")
	f, _ := dial(t, fastConfig(h.joinURL(old), nil))
	if err := f.Rejoin(h.joinURL(fresh)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Dial(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if len(attachRequests(h, fresh.Token)) != 1 {
		t.Fatal("the next dial did not use the new join URL")
	}
}

func TestTrayRejoinFollowsTheSupersededOrigin(t *testing.T) {
	h := newFakeHub(t)
	trays := supersedeChain(h, 1)
	fresh := h.addTray("fresh")
	f, _ := dial(t, fastConfig(h.joinURL(trays[0]), nil))
	if err := f.Rejoin(h.joinURL(fresh)); err != nil {
		t.Fatal(err)
	}
}

func TestTrayRejoinRefusesAnotherOrigin(t *testing.T) {
	h := newFakeHub(t)
	other := newFakeHub(t)
	old := h.addTray("old")
	foreign := other.addTray("foreign")
	path := filepath.Join(t.TempDir(), "tray-join.json")
	state := tray.OpenState(path)
	logs := &logBuffer{}
	cfg := fastConfig(h.joinURL(old), logs)
	cfg.State = state
	f, _ := dial(t, cfg)
	port := h.srv.URL[strings.LastIndex(h.srv.URL, ":"):]
	cases := map[string]error{
		other.joinURL(foreign):                                tray.ErrJoinOrigin,
		"http://localhost" + port + "/join/" + foreign.Token:  tray.ErrJoinOrigin,
		"https://127.0.0.1" + port + "/join/" + foreign.Token: tray.ErrJoinOrigin,
		"https://www.sliccy.ai/join/" + foreign.Token:         tray.ErrJoinOrigin,
		"http://evil.example.test/join/" + foreign.Token:      tray.ErrInvalidJoinURL,
		h.srv.URL + "/controller/" + foreign.Token:            tray.ErrInvalidJoinURL,
		"not a url " + foreign.Token:                          tray.ErrInvalidJoinURL,
	}
	for joinURL, want := range cases {
		err := f.Rejoin(joinURL)
		if !errors.Is(err, want) {
			t.Fatalf("%q: err %v", f.Redact(joinURL), err)
		}
		if strings.Contains(err.Error(), foreign.Token) {
			t.Fatalf("error leaks the token: %v", err)
		}
	}
	if state.Current(h.joinURL(old)) != h.joinURL(old) {
		t.Fatal("a refused join URL was stored")
	}
	if strings.Contains(logs.String(), foreign.Token) {
		t.Fatal("logs leak the refused token")
	}
	if _, err := f.Dial(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if len(attachRequests(h, old.Token)) != 2 || len(other.requestLog()) != 0 {
		t.Fatal("a refused join URL was dialled")
	}
}
