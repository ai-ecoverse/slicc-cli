package integration

import (
	"encoding/json"
	"sync/atomic"
	"testing"
)

func versionRouteScript() fakeScript {
	var n atomic.Int32
	return func(method string, _ json.RawMessage) (string, []string) {
		if method != "Browser.getVersion" {
			return "{}", nil
		}
		switch n.Add(1) {
		case 1:
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3","revision":"@abc","userAgent":"Mozilla/5.0 Fake","jsVersion":"13.4.114"}`, nil
		case 2:
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3","revision":"@abc","userAgent":"Mozilla/5.0 Fake","jsVersion":"13.4.114","slicc":{"route":"poisoned","attacker":true}}`, nil
		default:
			return "!Browser.getVersion failed upstream", nil
		}
	}
}

func TestCDPBrowserGetVersionCarriesASliccRouteHint(t *testing.T) {
	env, _ := setupFakeProtect(t, func(string, string) fakeScript { return versionRouteScript() })
	var r struct {
		Plain struct {
			OK     bool `json:"ok"`
			Result *struct {
				Product         string `json:"product"`
				ProtocolVersion string `json:"protocolVersion"`
				Revision        string `json:"revision"`
				UserAgent       string `json:"userAgent"`
				JSVersion       string `json:"jsVersion"`
				Slicc           *struct {
					Route    string `json:"route"`
					Attacker *bool  `json:"attacker"`
				} `json:"slicc"`
			} `json:"result"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"plain"`
		Poisoned struct {
			OK     bool `json:"ok"`
			Result *struct {
				Product         string `json:"product"`
				ProtocolVersion string `json:"protocolVersion"`
				Revision        string `json:"revision"`
				UserAgent       string `json:"userAgent"`
				JSVersion       string `json:"jsVersion"`
				Slicc           *struct {
					Route    string `json:"route"`
					Attacker *bool  `json:"attacker"`
				} `json:"slicc"`
			} `json:"result"`
		} `json:"poisoned"`
		Failed struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int             `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			} `json:"error"`
		} `json:"failed"`
		PlainKeys    []string `json:"plainKeys"`
		PoisonedKeys []string `json:"poisonedKeys"`
		FailedKeys   []string `json:"failedKeys"`
	}
	env.evalJSON(t, "linkTest.protect.versionRoute('chrome-2')", &r)
	if !r.Plain.OK || r.Plain.Result == nil {
		t.Fatalf("plain Browser.getVersion: %+v", r.Plain)
	}
	got := r.Plain.Result
	if got.Product != "Chrome/154.0.8037.98" || got.ProtocolVersion != "1.3" || got.Revision != "@abc" || got.UserAgent != "Mozilla/5.0 Fake" || got.JSVersion != "13.4.114" {
		t.Fatalf("Chrome fields changed: %+v", got)
	}
	if got.Slicc == nil || got.Slicc.Route != "link" || got.Slicc.Attacker != nil {
		t.Fatalf("plain slicc route: %+v", got.Slicc)
	}
	wantPlain := []string{"jsVersion", "product", "protocolVersion", "revision", "slicc", "userAgent"}
	if len(r.PlainKeys) != len(wantPlain) {
		t.Fatalf("plain keys %v want %v", r.PlainKeys, wantPlain)
	}
	for i, k := range wantPlain {
		if r.PlainKeys[i] != k {
			t.Fatalf("plain keys %v want %v", r.PlainKeys, wantPlain)
		}
	}
	if !r.Poisoned.OK || r.Poisoned.Result == nil {
		t.Fatalf("poisoned Browser.getVersion: %+v", r.Poisoned)
	}
	poisoned := r.Poisoned.Result
	if poisoned.Product != got.Product || poisoned.ProtocolVersion != got.ProtocolVersion || poisoned.Revision != got.Revision || poisoned.UserAgent != got.UserAgent || poisoned.JSVersion != got.JSVersion {
		t.Fatalf("poisoned Chrome fields changed: %+v", poisoned)
	}
	if poisoned.Slicc == nil || poisoned.Slicc.Route != "link" || poisoned.Slicc.Attacker != nil {
		t.Fatalf("poisoned slicc was forwarded: %+v", poisoned.Slicc)
	}
	if len(r.PoisonedKeys) != len(wantPlain) {
		t.Fatalf("poisoned keys %v want %v", r.PoisonedKeys, wantPlain)
	}
	for i, k := range wantPlain {
		if r.PoisonedKeys[i] != k {
			t.Fatalf("poisoned keys %v want %v", r.PoisonedKeys, wantPlain)
		}
	}
	if r.Failed.OK || r.Failed.Error == nil {
		t.Fatalf("error Browser.getVersion: %+v", r.Failed)
	}
	if r.Failed.Result != nil {
		t.Fatalf("error reply gained a result: %s", r.Failed.Result)
	}
	if r.Failed.Error.Code != -32000 || r.Failed.Error.Message != "Browser.getVersion failed upstream" {
		t.Fatalf("error reply changed: %+v", r.Failed.Error)
	}
	if r.Failed.Error.Data != nil {
		t.Fatalf("error reply gained data: %s", r.Failed.Error.Data)
	}
	for _, k := range r.FailedKeys {
		if k == "slicc" {
			t.Fatalf("error reply gained slicc: keys=%v", r.FailedKeys)
		}
	}
}
