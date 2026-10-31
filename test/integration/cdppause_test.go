package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mappedServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mapped.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("SourceMap", "/mapped.js.map")
		_, _ = w.Write([]byte("document.title = 'mapped';"))
	})
	mux.HandleFunc("/mapped.js.map", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":3,"sources":["mapped.ts"],"names":[],"mappings":"AAAA"}`))
	})
	mux.HandleFunc("/mapped", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>loading</title><p>mapped<script src="/mapped.js"></script>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>" + strings.TrimPrefix(r.URL.Path, "/") + "</title><p>plain"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPAnswersAroundAPageItsOwnInstrumentationBreakpointPaused(t *testing.T) {
	env := setupProtect(t)
	base := strings.Replace(mappedServer(t).URL, "127.0.0.1", "localhost", 1)
	const timeout = 3000
	var r struct {
		Reason  string `json:"reason"`
		Disable struct {
			cdpOutcome
			MS int `json:"ms"`
		} `json:"disable"`
		SideEval cdpOutcome `json:"sideEval"`
		Targets  cdpOutcome `json:"targets"`
		Version  cdpOutcome `json:"version"`
		Resume   cdpOutcome `json:"resume"`
		Title    struct {
			cdpOutcome
			Result string `json:"result"`
		} `json:"title"`
	}
	env.evalJSON(t, "linkTest.protect.instrumentationPause("+jsArgs("chrome", base, timeout)+")", &r)
	if r.Reason != "other" {
		t.Fatalf("the page paused with reason %q, want the guard-converted ordinary pause", r.Reason)
	}
	if !r.Disable.OK && (r.Disable.Error.Message != "CDP timed out: Debugger.disable" || r.Disable.MS > timeout+5000) {
		t.Errorf("Debugger.disable on the paused page: %+v, want an answer or the client's own timeout", r.Disable)
	}
	if !r.SideEval.OK || !r.Targets.OK || !r.Version.OK {
		t.Errorf("while the page was paused: another session %+v, the browser %+v, another stream %+v, want all answered", r.SideEval, r.Targets, r.Version)
	}
	if !r.Resume.OK && !r.Disable.OK {
		t.Errorf("Debugger.resume on the paused page: %+v, want it answered", r.Resume)
	}
	if !r.Title.OK || r.Title.Result != "mapped" {
		t.Errorf("the page after the resume: %+v, want its script run", r.Title)
	}
}
