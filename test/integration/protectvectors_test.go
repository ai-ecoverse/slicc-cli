package integration

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

var vectorPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")

func vectorRoutes(mux *http.ServeMux, log *navLog) {
	cors := func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
	}
	mux.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(r.URL.Query().Get("code"))
		if err != nil || code < 300 || code > 399 {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, r.URL.Query().Get("to"), code)
	})
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		cors(w, r)
		_, _ = w.Write([]byte("real-" + r.URL.Query().Get("n")))
	})
	mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(vectorPNG)
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		cors(w, r)
		if u, p, ok := r.BasicAuth(); ok && u == "u" && p == "p" {
			_, _ = w.Write([]byte("authed"))
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="vector"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	})
	mux.HandleFunc("/framed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		src := strings.NewReplacer(`"`, "&quot;", "<", "&lt;").Replace(r.URL.Query().Get("src"))
		_, _ = w.Write([]byte(`<!doctype html><title>framed</title><iframe id="v" src="` + src + `"></iframe>`))
	})
	mux.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		cors(w, r)
		n := r.URL.Query().Get("n")
		log.mu.Lock()
		out := struct {
			Hits    []string `json:"hits"`
			Beacons []string `json:"beacons"`
		}{Hits: []string{}, Beacons: append([]string{}, log.beacons[n]...)}
		for _, h := range log.hits {
			if strings.Contains(h, n) && !strings.Contains(h, " /log?") && !strings.Contains(h, " /beacon?") {
				out.Hits = append(out.Hits, h)
			}
		}
		log.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

func TestCDPSettlesLegacyInterceptionsOfProtectedURLsItself(t *testing.T) {
	var vault = "http://vault.sliccy.ai/data"
	env, fake := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		ev := func(id, url, extra string) string {
			return `{"method":"Network.requestIntercepted","params":{"interceptionId":"` + id + `","resourceType":"Fetch","request":{"url":"` + url + `","headers":{"Cookie":"s=secret"}}` + extra + `}}`
		}
		return func(method string, _ json.RawMessage) (string, []string) {
			if method != "Network.setRequestInterception" {
				return "{}", nil
			}
			return "{}", []string{
				ev("seven", seven+"/data", ""),
				ev("vault", vault, ""),
				ev("auth", seven+"/auth", `,"authChallenge":{"source":"Server","origin":"`+seven+`","scheme":"basic","realm":"r"}`),
				strings.Replace(ev("doc", seven+"/doc", ""), `"Fetch"`, `"Document"`, 1),
				ev("ordinary", allowed+"/data", ""),
			}
		}
	})
	var r struct {
		Seen     []string `json:"seen"`
		Attempts map[string]struct {
			OK    bool `json:"ok"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"attempts"`
	}
	protectedIDs := []string{"seven", "vault", "auth", "doc"}
	env.evalJSON(t, "linkTest.protect.legacyInterception("+jsArgs("chrome-2", append(protectedIDs, "ordinary"), "ordinary")+")", &r)
	if strings.Join(r.Seen, " ") != "ordinary" {
		t.Errorf("the page saw interceptions %v, want only the ordinary one", r.Seen)
	}
	if a := r.Attempts["ordinary"]; !a.OK {
		t.Errorf("the control: continuing the ordinary interception it was shown: %+v", a)
	}
	for _, id := range protectedIDs {
		if a := r.Attempts[id]; a.OK || !strings.Contains(a.Error.Message, "slicc didn't pass this paused request to the page") {
			t.Errorf("continuing the hidden %s interception: %+v", id, a)
		}
	}
	want := map[string]string{
		"seven": `{"interceptionId":"seven"}`,
		"vault": `{"interceptionId":"vault"}`,
		"auth":  `{"authChallengeResponse":{"response":"Default"},"interceptionId":"auth"}`,
		"doc":   `{"errorReason":"BlockedByClient","interceptionId":"doc"}`,
	}
	settled := map[string]string{}
	ordinaryForwarded := 0
	for _, raw := range fake.received() {
		var m struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(raw), &m) != nil || m.Method != "Network.continueInterceptedRequest" {
			continue
		}
		var p struct {
			InterceptionID string `json:"interceptionId"`
			RawResponse    string `json:"rawResponse"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.RawResponse != "" {
			t.Errorf("a rewritten legacy response reached the browser: %s", raw)
		}
		if p.InterceptionID == "ordinary" {
			ordinaryForwarded++
			continue
		}
		settled[p.InterceptionID] = string(m.Params)
	}
	for id, w := range want {
		if settled[id] != w {
			t.Errorf("slicc settled %s with %s, want %s", id, settled[id], w)
		}
	}
	if ordinaryForwarded != 1 {
		t.Errorf("the unmodified page continuation reached the browser %d times, want once", ordinaryForwarded)
	}
}
