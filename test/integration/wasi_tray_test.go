package integration

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const (
	trayFP1      = "3fa90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c210"
	trayFP2      = "3fa9ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554407be"
	trayFP3      = "88d10123456789abcdef0123456789abcdef0123456789abcdef0123456707be"
	trayLeaderJU = "https://tray.example.test/join/leader.aabbccddeeff"
)

func trayStatus() map[string]any {
	return ev(
		"tray", "ready",
		"hub", "https://tray.example.test",
		"exit", ev("kind", "link", "id", trayFP1),
		"devices", []any{
			ev("id", trayFP1, "name", "slicc on mac", "kind", "cli", "host", "mac.slicc.internal", "address", "10.77.0.2", "mode", "remote", "offers", []any{"net", "ssh", "fs"}, "exit", true, "state", "connected"),
			ev("id", trayFP2, "name", "slicc on linux", "kind", "cli", "host", "linux.slicc.internal", "address", "10.77.0.3", "mode", "remote", "offers", []any{"ssh"}, "exit", false, "state", "offline"),
			ev("id", trayFP3, "name", "SLICC on Linux", "kind", "seven", "host", "", "address", "", "mode", "remote", "offers", []any{}, "exit", false, "state", "connected"),
		},
	)
}

func leakyStatus() map[string]any {
	s := trayStatus()
	s["tray"] = "failed"
	s["hub"] = trayLeaderJU
	s["error"] = "dial " + trayLeaderJU + ": refused"
	s["joinUrl"] = trayLeaderJU
	s["joinCommand"] = "slicc " + trayLeaderJU + " follow"
	s["followUrl"] = "https://seven.example.test/#follow=" + trayLeaderJU
	s["webhookUrl"] = "https://tray.example.test/hook/secret"
	devices := s["devices"].([]any)
	devices[0].(map[string]any)["name"] = "\x1b[31mevil\nDEVICE " + trayLeaderJU
	return s
}

func checkNoLeak(t *testing.T, r wasiResult) {
	t.Helper()
	all := r.Stdout + r.Stderr
	for _, bad := range []string{"leader.aabbccddeeff", "\x1b", "hook/secret"} {
		if strings.Contains(all, bad) {
			t.Fatalf("output shows %q:\nstdout %q\nstderr %q", bad, r.Stdout, r.Stderr)
		}
	}
}

func checkTrayOpen(t *testing.T, c wasiConn, want map[string]any) {
	t.Helper()
	if c.First == nil || c.First.Type != 1 || c.First.JSON.V != 1 || slices.Contains(c.First.Keys, "join") {
		t.Fatalf("first record %+v: want a control record, v 1, no join key", c.First)
	}
	got, _ := json.Marshal(c.First.JSON.Open)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("open %s, want %s", got, exp)
	}
}

func trayConns(t *testing.T, r wasiResult, n int) []wasiConn {
	t.Helper()
	if len(r.Log) != n {
		t.Fatalf("%d facade connections, want %d: %+v", len(r.Log), n, r.Log)
	}
	for _, c := range r.Log {
		if c.First == nil {
			t.Fatalf("a facade connection without a first record: %+v", r.Log)
		}
		if len(c.Records) != 0 {
			t.Fatalf("slicc sent records after a tray op's opening: %v", c.Records)
		}
	}
	return r.Log
}

func TestWasiTray(t *testing.T) {
	status := opened(trayStatus())
	runWasi(t, []wasiCase{
		{Name: "status", Argv: []string{"slicc", "tray"}, Scenario: []map[string]any{status},
			check: func(t *testing.T, r wasiResult) {
				want := "tray ready, hub https://tray.example.test\nexit: slicc on mac (3fa9…c210)\n" +
					"DEVICE     NAME            HOST                  MODE    STATE      OFFERS\n" +
					"3fa9…c210  slicc on mac    mac.slicc.internal    remote  connected  net,ssh,fs,exit\n" +
					"3fa9…07be  slicc on linux  linux.slicc.internal  remote  offline    ssh\n" +
					"88d1…07be  SLICC on Linux  -                     remote  connected  -\n"
				if r.Status != 0 || r.Stdout != want || r.Stderr != "" {
					t.Fatalf("exit %d\nstdout %q\nwant   %q\nstderr %q", r.Status, r.Stdout, want, r.Stderr)
				}
				checkTrayOpen(t, trayConns(t, r, 1)[0], ev("kind", "tray", "op", "status"))
			}},
		{Name: "status json", Argv: []string{"slicc", "tray", "--json"}, Scenario: []map[string]any{status},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "")
				var got map[string]any
				if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
					t.Fatalf("stdout isn't JSON: %v\n%s", err, r.Stdout)
				}
				exp, _ := json.Marshal(trayStatus())
				back, _ := json.Marshal(got)
				if string(back) != string(exp) || strings.Count(r.Stdout, "\n") != 1 {
					t.Fatalf("json %s, want %s", r.Stdout, exp)
				}
			}},
		{Name: "tray off", Argv: []string{"slicc", "tray"}, Scenario: []map[string]any{opened(ev("tray", "idle", "devices", []any{}))},
			check: func(t *testing.T, r wasiResult) { wantExit(t, r, 0, "tray off\nno devices\n", "") }},
		{Name: "url", Argv: []string{"slicc", "tray", "url"}, Scenario: []map[string]any{opened(ev("url", trayLeaderJU))},
			check: func(t *testing.T, r wasiResult) {
				if r.Status != 0 || r.Stdout != trayLeaderJU+"\n" || r.Stderr != "" {
					t.Fatalf("exit %d, stdout %q, stderr %q", r.Status, r.Stdout, r.Stderr)
				}
				checkTrayOpen(t, trayConns(t, r, 1)[0], ev("kind", "tray", "op", "url"))
			}},
		{Name: "url while the tray is down", Argv: []string{"slicc", "tray", "url"}, Scenario: []map[string]any{refused("ENOTCONN")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: this seven's tray isn't connected\n")
			}},
		{Name: "rotate", Argv: []string{"slicc", "tray", "rotate"}, Scenario: []map[string]any{opened(ev())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: made a new join URL; the old one no longer works")
				if r.Stdout != "" {
					t.Fatalf("rotate printed %q", r.Stdout)
				}
				checkTrayOpen(t, trayConns(t, r, 1)[0], ev("kind", "tray", "op", "rotate"))
			}},
		{Name: "rotate declined", Argv: []string{"slicc", "tray", "rotate"}, Scenario: []map[string]any{refused("EACCES: you declined")},
			check: func(t *testing.T, r wasiResult) { wantExit(t, r, 3, "", "slicc tray: you declined\n") }},
		{Name: "retry", Argv: []string{"slicc", "tray", "retry"}, Scenario: []map[string]any{opened(ev())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: reconnecting the tray's links\n")
				checkTrayOpen(t, trayConns(t, r, 1)[0], ev("kind", "tray", "op", "retry"))
			}},
		{Name: "unlink by prefix", Argv: []string{"slicc", "tray", "unlink", "88:D1"}, Scenario: []map[string]any{status, opened(ev())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: unlinked 88d1…07be (SLICC on Linux)\n")
				c := trayConns(t, r, 2)
				checkTrayOpen(t, c[0], ev("kind", "tray", "op", "status"))
				checkTrayOpen(t, c[1], ev("kind", "tray", "op", "unlink", "id", trayFP3))
			}},
		{Name: "unlink by short form", Argv: []string{"slicc", "tray", "unlink", "3fa9…07be"}, Scenario: []map[string]any{status, opened(ev())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: unlinked 3fa9…07be (slicc on linux)\n")
				checkTrayOpen(t, trayConns(t, r, 2)[1], ev("kind", "tray", "op", "unlink", "id", trayFP2))
			}},
		{Name: "unlink by a loose short form", Argv: []string{"slicc", "tray", "unlink", "88d…7be"}, Scenario: []map[string]any{status},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: no device matches 88d…7be\n")
				trayConns(t, r, 1)
			}},
		{Name: "unlink by a short form with dots", Argv: []string{"slicc", "tray", "unlink", "88d1...07be"}, Scenario: []map[string]any{status, opened(ev())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: unlinked 88d1…07be (SLICC on Linux)\n")
				checkTrayOpen(t, trayConns(t, r, 2)[1], ev("kind", "tray", "op", "unlink", "id", trayFP3))
			}},
		{Name: "unlink ambiguous", Argv: []string{"slicc", "tray", "unlink", "3fa9"}, Scenario: []map[string]any{status},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: more than one device matches 3fa9; give more of the fingerprint\n")
				trayConns(t, r, 1)
			}},
		{Name: "unlink unknown", Argv: []string{"slicc", "tray", "unlink", "abcd"}, Scenario: []map[string]any{status},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: no device matches abcd\n")
				trayConns(t, r, 1)
			}},
		{Name: "a status that leaks the join URL", Argv: []string{"slicc", "tray"}, Scenario: []map[string]any{opened(leakyStatus())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "tray failed, hub <join-url>\nerror: dial <join-url>: refused\n", "")
				checkNoLeak(t, r)
			}},
		{Name: "a json status that leaks the join URL", Argv: []string{"slicc", "tray", "--json"}, Scenario: []map[string]any{opened(leakyStatus())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, `"hub":"\u003cjoin-url\u003e"`, "")
				checkNoLeak(t, r)
				for _, key := range []string{`"joinUrl"`, `"joinCommand"`, `"followUrl"`, `"webhookUrl"`, `"url"`} {
					if strings.Contains(r.Stdout, key) {
						t.Fatalf("--json kept %s: %s", key, r.Stdout)
					}
				}
				if strings.Count(r.Stdout, "\n") != 1 {
					t.Fatalf("--json isn't one line: %q", r.Stdout)
				}
			}},
		{Name: "a refusal that leaks the join URL", Argv: []string{"slicc", "tray", "retry"}, Scenario: []map[string]any{refused("EACCES: no, " + trayLeaderJU + "\x1b[2J")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc tray: no, <join-url>\n")
				checkNoLeak(t, r)
			}},
		{Name: "unlink of a device whose name leaks", Argv: []string{"slicc", "tray", "unlink", "3fa9…c210"}, Scenario: []map[string]any{opened(leakyStatus()), opened(ev())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: unlinked 3fa9…c210 (evil DEVICE <join-url>)\n")
				checkNoLeak(t, r)
			}},
		{Name: "url that isn't a join URL", Argv: []string{"slicc", "tray", "url"}, Scenario: []map[string]any{opened(ev("url", "javascript:alert(1)"))},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: seven's link facade sent something that isn't a join URL\n")
				if r.Stdout != "" || strings.Contains(r.Stderr, "alert") {
					t.Fatalf("stdout %q, stderr %q", r.Stdout, r.Stderr)
				}
			}},
		{Name: "url with a second line", Argv: []string{"slicc", "tray", "url"}, Scenario: []map[string]any{opened(ev("url", trayLeaderJU+"\nrm -rf /"))},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: seven's link facade sent something that isn't a join URL\n")
				checkNoLeak(t, r)
			}},
		{Name: "a facade without a tray", Argv: []string{"slicc", "tray"}, Scenario: []map[string]any{refused("EOPNOTSUPP")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: this seven's link facade has no tray\n")
			}},
		{Name: "malformed status", Argv: []string{"slicc", "tray"}, Scenario: []map[string]any{opened(ev("devices", "nope"))},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: seven's link facade sent a malformed tray status\n")
			}},
		{Name: "no facade url", Argv: []string{"slicc", "tray"}, Env: map[string]string{"SLICC_LINK_URL": ""},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc tray: SLICC_LINK_URL isn't set")
				if len(r.Log) != 0 {
					t.Fatalf("facade connections: %+v", r.Log)
				}
			}},
	})
}

func TestWasiTrayUsage(t *testing.T) {
	bad := func(want string) func(t *testing.T, r wasiResult) {
		return func(t *testing.T, r wasiResult) {
			wantExit(t, r, 2, "", want)
			if len(r.Log) != 0 {
				t.Fatalf("facade connections: %+v", r.Log)
			}
		}
	}
	runWasi(t, []wasiCase{
		{Name: "help", Argv: []string{"slicc", "tray", "--help"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 0, "usage: slicc tray [--json]", "")
			if !strings.Contains(r.Stdout, "slicc tray url > /dev/clipboard") {
				t.Fatalf("help: %s", r.Stdout)
			}
		}},
		{Name: "top-level help lists tray", Argv: []string{"slicc", "--help"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 0, "  slicc tray ", "")
		}},
		{Name: "unknown op", Argv: []string{"slicc", "tray", "reset"}, check: bad(`slicc tray: unknown command "reset"`)},
		{Name: "json with url", Argv: []string{"slicc", "tray", "url", "--json"}, check: bad("slicc tray: --json only goes with slicc tray")},
		{Name: "unlink without a device", Argv: []string{"slicc", "tray", "unlink"}, check: bad("slicc tray: name one device")},
		{Name: "extra argument", Argv: []string{"slicc", "tray", "rotate", "now"}, check: bad(`slicc tray: unexpected argument "now"`)},
	})
}

func TestWasiTrayRealFacade(t *testing.T) {
	real := wasiReal{Port: wasiLinkPort, Users: []wasiUser{{Name: "cone", Groups: []string{"sudo"}}, {Name: "scoop-site", Groups: []string{"cone"}}}}
	runWasiReal(t, real, []wasiCase{
		{Name: "root lists the tray", Argv: []string{"slicc", "tray"}, Scenario: []map[string]any{opened(trayStatus())},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "tray ready, hub https://tray.example.test\n", "")
				c := onlyConn(t, r)
				checkStamp(t, c, "root", true)
				checkTrayOpen(t, c, ev("kind", "tray", "op", "status"))
			}},
		{Name: "a cone in sudo prints the join URL", Argv: []string{"slicc", "tray", "url"}, User: "cone", Scenario: []map[string]any{opened(ev("url", trayLeaderJU))},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, trayLeaderJU+"\n", "")
				c := onlyConn(t, r)
				checkStamp(t, c, "cone", false)
				checkTrayOpen(t, c, ev("kind", "tray", "op", "url"))
			}},
		{Name: "a scoop is refused", Argv: []string{"slicc", "tray", "url"}, User: "scoop-site", Scenario: []map[string]any{opened(ev("url", trayLeaderJU))},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc tray: only root and members of sudo may follow a tray\n")
				if strings.Contains(r.Stdout+r.Stderr, "leader.aabbccddeeff") {
					t.Fatalf("a scoop saw the join URL: %q %q", r.Stdout, r.Stderr)
				}
				c := onlyConn(t, r)
				checkStamp(t, c, "scoop-site", false)
				checkTrayOpen(t, c, ev("kind", "tray", "op", "url"))
			}},
	})
}
