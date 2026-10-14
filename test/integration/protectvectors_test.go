package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
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
	mux.HandleFunc("/selfnav", func(w http.ResponseWriter, r *http.Request) {
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		to, _ := json.Marshal(r.URL.Query().Get("to"))
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>selfnav</title><script>setTimeout(() => { location.href = ` + string(to) + `; }, ` + strconv.Itoa(ms) + `);</script>`))
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

type vectorEnv struct {
	*navEnv
	same  string
	ord   string
	plain string
}

func setupVectors(t *testing.T) *vectorEnv {
	t.Helper()
	nav := setupNav(t)
	h := nav.b.client
	srv := httptest.NewServer(nav.handler)
	t.Cleanup(srv.Close)
	e := &vectorEnv{
		navEnv: nav,
		same:   srv.URL,
		ord:    "http://ordinary.test:" + nav.port,
		plain:  "http://page.ordinary.test:" + nav.port,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for kind, base := range map[string]string{"ordinary": e.ord, "seven": e.seven, "vault": e.vault} {
		c := map[string]any{"name": "vec-" + kind, "value": "1", "url": base + "/"}
		if err := h.Call(ctx, "", "Storage.setCookies", map[string]any{"cookies": []any{c}}, nil); err != nil {
			t.Fatalf("planting the %s cookie: %v", kind, err)
		}
	}
	return e
}

type vectorInfo struct {
	ID      string            `json:"id"`
	Case    string            `json:"case"`
	Control string            `json:"control"`
	Skip    map[string]string `json:"skip"`
}

type vectorResult struct {
	Pass       bool            `json:"pass"`
	Skipped    string          `json:"skipped"`
	Observed   map[string]any  `json:"observed"`
	Expected   map[string]any  `json:"expected"`
	Mismatches []string        `json:"mismatches"`
	Targets    []string        `json:"targets"`
	ErrorPage  []string        `json:"errorPage"`
	Error      string          `json:"error"`
	CDPError   json.RawMessage `json:"cdpError"`
}

func (e *vectorEnv) env() map[string]any {
	return map[string]any{
		"allowed": e.allowed, "cross": e.cross, "log": e.allowed,
		"dest":     map[string]string{"ordinary": e.ord, "seven": e.seven, "vault": e.vault},
		"sameSite": map[string]string{"ordinary": e.plain, "seven": e.same},
		"cookie":   "vec-",
	}
}

var vectorOrigins = []string{"ordinary", "seven", "vault"}

func TestCDPProtectVectors(t *testing.T) {
	e := setupVectors(t)
	var list []vectorInfo
	e.p.eval("linkTest.protect.vectorList()", &list)
	cases := map[string]bool{}
	for _, v := range list {
		cases[v.Case] = true
		if v.Control != "ordinary" {
			t.Errorf("vector %s has no control on an ordinary origin", v.ID)
		}
		for origin := range v.Skip {
			if origin == "ordinary" {
				t.Errorf("vector %s skips its control", v.ID)
			}
		}
	}
	var got []string
	for c := range cases {
		got = append(got, c)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "C1 C2 C3 C4 C5 C6" {
		t.Errorf("the vectors cover %v, want C1 to C6", got)
	}
	env := e.env()
	for _, v := range list {
		t.Run(v.ID, func(t *testing.T) {
			control := false
			for _, origin := range vectorOrigins {
				var r vectorResult
				e.p.eval("linkTest.protect.vector("+jsArgs("chrome", v.ID, origin, env)+")", &r)
				func() {
					defer func() {
						for _, id := range r.Targets {
							e.closeTarget(id)
						}
					}()
					e.checkVector(t, v, origin, r, &control)
				}()
			}
			if !control {
				t.Errorf("%s: the control on an ordinary origin did not pass, so the protected runs prove nothing", v.ID)
			}
		})
	}
}

func (e *vectorEnv) checkVector(t *testing.T, v vectorInfo, origin string, r vectorResult, control *bool) {
	t.Helper()
	if r.Skipped != "" {
		t.Logf("%s on %s: not applicable: %s", v.ID, origin, r.Skipped)
		return
	}
	if r.Error != "" {
		t.Errorf("%s on %s: %s %s", v.ID, origin, r.Error, r.CDPError)
		return
	}
	if !r.Pass {
		t.Errorf("%s on %s: %v\n  observed %v\n  expected %v", v.ID, origin, r.Mismatches, r.Observed, r.Expected)
	}
	if origin == "ordinary" {
		*control = r.Pass
	}
	for _, id := range r.ErrorPage {
		if href := e.directHref(t, id); !strings.HasPrefix(href, "chrome-error://") {
			t.Errorf("%s on %s: the target is at %q, want the error page", v.ID, origin, href)
		}
	}
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
	forged := 0
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
			forged++
			if p.InterceptionID != "ordinary" {
				t.Errorf("the page's forged answer reached the browser for %s", p.InterceptionID)
			}
			continue
		}
		settled[p.InterceptionID] = string(m.Params)
	}
	for id, w := range want {
		if settled[id] != w {
			t.Errorf("slicc settled %s with %s, want %s", id, settled[id], w)
		}
	}
	if forged != 1 {
		t.Errorf("the control's answer reached the browser %d times, want once", forged)
	}
}
