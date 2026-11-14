package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	idpOrigin = "https://ims-na1.adobelogin.com"
	stgOrigin = "https://ims-na1-stg1.adobelogin.com"
)

func scriptedAnswers(answers map[string]string, after map[string][]string) fakeScript {
	return func(method string, _ json.RawMessage) (string, []string) {
		if r, ok := answers[method]; ok {
			return r, after[method]
		}
		return "{}", after[method]
	}
}

func credentialLogClean(t *testing.T, env *protectEnv) {
	t.Helper()
	log := env.a.stderr.String()
	for _, leak := range []string{"secret", "adobelogin"} {
		if strings.Contains(log, leak) {
			t.Errorf("slicc's debug log names %q:\n%s", leak, log)
		}
	}
}

func withPage(script fakeScript) fakeScript {
	page := guardWaitScript("{}")
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Target.createTarget", "Target.attachToTarget", "Browser.getVersion":
			return page(method, params)
		}
		return script(method, params)
	}
}

func outcomesOf(t *testing.T, env *protectEnv, calls [][]any) ([]callOutcome, string) {
	t.Helper()
	var r struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.callsOnPage("+jsArgs("chrome-2", calls)+")", &r)
	b, _ := json.Marshal(r)
	if len(r.Out) != len(calls) {
		t.Fatalf("calls: %s", b)
	}
	return r.Out, string(b)
}

func TestCDPWithholdsSignInCookiesFromExport(t *testing.T) {
	jar := `{"cookies":[` +
		`{"name":"idp","value":"secret-idp","domain":"ims-na1.adobelogin.com","path":"/"},` +
		`{"name":"parent","value":"secret-parent","domain":".adobelogin.com","path":"/"},` +
		`{"name":"stg","value":"secret-stg","domain":"IMS-NA1-STG1.adobelogin.com.","path":"/"},` +
		`{"name":"chips","value":"secret-chips","domain":"cdn.example.test","path":"/","partitionKey":{"topLevelSite":"https://adobelogin.com","hasCrossSiteAncestor":true}},` +
		`{"name":"plain","value":"kept-plain","domain":"localhost","path":"/"},` +
		`{"name":"adobe","value":"kept-adobe","domain":".adobe.com","path":"/"},` +
		`{"name":"authui","value":"kept-authui","domain":"auth.services.adobe.com","path":"/"},` +
		`{"name":"adobeid","value":"kept-adobeid","domain":".adobeid-na1.services.adobe.com","path":"/"},` +
		`{"name":"sub","value":"kept-sub","domain":"x.ims-na1.adobelogin.com","path":"/"},` +
		`{"name":"lookalike","value":"kept-lookalike","domain":"ims-na1.adobelogin.com.example.test","path":"/"}]}`
	env, _ := setupFakeProtect(t, func(string, string) fakeScript {
		return withPage(scriptedAnswers(map[string]string{
			"Network.getAllCookies": jar,
			"Network.getCookies":    jar,
			"Storage.getCookies":    jar,
			"Page.getCookies":       jar,
		}, nil))
	})
	calls := [][]any{
		{"Network.getAllCookies", map[string]any{}},
		{"Network.getCookies", map[string]any{"urls": []string{idpOrigin + "/ims/authorize", env.allowed + "/"}}},
		{"Network.getCookies", map[string]any{}},
		{"Storage.getCookies", map[string]any{}},
		{"Page.getCookies", map[string]any{}, "page"},
	}
	out, b := outcomesOf(t, env, calls)
	for i, o := range out {
		text := string(o.Result)
		if !o.OK || strings.Contains(text, "secret") || !strings.Contains(text, "kept-plain") || !strings.Contains(text, "kept-lookalike") || !strings.Contains(text, "kept-sub") || !strings.Contains(text, "kept-adobe") || !strings.Contains(text, "kept-authui") || !strings.Contains(text, "kept-adobeid") {
			t.Errorf("call %d %s: want the sign-in cookies withheld and the others kept, got %s", i, o.Method, b)
		}
	}
	credentialLogClean(t, env)
}

func TestCDPRefusesSignInCookieWrites(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript { return withPage(scriptedAnswers(nil, nil)) })
	partition := map[string]any{"topLevelSite": "https://adobelogin.com", "hasCrossSiteAncestor": false}
	calls := [][]any{
		{"Network.setCookie", map[string]any{"name": "w", "value": "secret-w1", "url": idpOrigin + "/"}},
		{"Network.setCookie", map[string]any{"name": "w", "value": "secret-w2", "domain": ".adobelogin.com", "path": "/"}},
		{"Network.setCookie", map[string]any{"name": "w", "value": "secret-w3", "url": stgOrigin + ":8443/"}},
		{"Network.setCookie", map[string]any{"name": "w", "value": "secret-w4", "url": env.allowed + "/", "secure": true, "partitionKey": partition}},
		{"Network.setCookies", map[string]any{"cookies": []any{map[string]any{"name": "ok", "value": "1", "url": env.allowed + "/"}, map[string]any{"name": "w", "value": "secret-w5", "domain": "ims-na1.adobelogin.com", "path": "/"}}}},
		{"Network.deleteCookies", map[string]any{"name": "secret-w6", "url": idpOrigin + "/"}},
		{"Network.deleteCookies", map[string]any{"name": "secret-w7", "domain": "adobelogin.com"}},
		{"Page.deleteCookie", map[string]any{"cookieName": "secret-w8", "url": idpOrigin + "/ims"}, "page"},
		{"Storage.setCookies", map[string]any{"cookies": []any{map[string]any{"name": "w", "value": "secret-w9", "domain": "ims-na1-stg1.adobelogin.com", "path": "/"}}}},
		{"Network.clearBrowserCookies", map[string]any{}},
		{"Storage.clearCookies", map[string]any{}},
		{"Network.setCookie", map[string]any{"name": "plain", "value": "kept", "url": env.allowed + "/"}},
		{"Network.setCookie", map[string]any{"name": "adobe", "value": "kept", "domain": ".adobe.com", "path": "/"}},
		{"Network.deleteCookies", map[string]any{"name": "plain", "url": env.allowed + "/"}},
	}
	out, b := outcomesOf(t, env, calls)
	for i, o := range out {
		refuse := i < 11
		switch {
		case refuse && (o.OK || !strings.HasPrefix(o.Error.Message, "slicc: refused: ")):
			t.Errorf("call %d %s: want refused, got %s", i, o.Method, b)
		case refuse && strings.Contains(o.Error.Message, "adobelogin"):
			t.Errorf("call %d %s: the refusal names the origin: %q", i, o.Method, o.Error.Message)
		case !refuse && !o.OK:
			t.Errorf("call %d %s: the control was refused: %s", i, o.Method, b)
		}
	}
	for _, m := range fake.received() {
		if strings.Contains(m, "secret-w") {
			t.Errorf("the browser got %s", m)
		}
	}
	credentialLogClean(t, env)
}

func credentialEventScript(allowed string) []string {
	ev := func(method, params string) string {
		return `{"method":"` + method + `","params":` + params + `}`
	}
	return []string{
		netEvent("requestWillBeSent", `{"requestId":"idp","request":{"url":"`+idpOrigin+`/ims/token","headers":{"Cookie":"s=secret-rq","Authorization":"Bearer kept-auth"}}}`),
		netEvent("requestWillBeSentExtraInfo", `{"requestId":"idp","associatedCookies":[{"blockedReasons":[],"cookie":{"name":"s","value":"secret-assoc","domain":"ims-na1.adobelogin.com","path":"/"}}],"headers":{"Cookie":"s=secret-xrq"}}`),
		netEvent("responseReceived", `{"requestId":"idp","response":{"url":"`+idpOrigin+`/ims/token","headers":{"Set-Cookie":"s=secret-rs"},"requestHeaders":{"Cookie":"s=secret-rsrq"},"headersText":"HTTP/1.1 200 OK\r\nSet-Cookie: s=secret-rstxt\r\n\r\n"}}`),
		netEvent("responseReceivedExtraInfo", `{"requestId":"idp","statusCode":200,"headers":{"Set-Cookie":"s=secret-xrs"},"headersText":"HTTP/1.1 200 OK\r\nSet-Cookie: s=secret-xrstxt\r\n\r\n","blockedCookies":[{"blockedReasons":["SecureOnly"],"cookieLine":"s=secret-line"}]}`),
		netEvent("loadingFinished", `{"requestId":"idp","timestamp":1,"encodedDataLength":1}`),
		netEvent("requestWillBeSent", `{"requestId":"sib","request":{"url":"https://adobelogin.com/x","headers":{}}}`),
		netEvent("requestWillBeSentExtraInfo", `{"requestId":"sib","associatedCookies":[{"blockedReasons":[],"cookie":{"name":"p","value":"secret-sibassoc","domain":".adobelogin.com","path":"/"}}],"headers":{"Cookie":"p=secret-sib"}}`),
		netEvent("responseReceived", `{"requestId":"sib","response":{"url":"https://adobelogin.com/x","headers":{}}}`),
		netEvent("responseReceivedExtraInfo", `{"requestId":"sib","statusCode":200,"headers":{"Set-Cookie":"p=secret-sibset; Domain=.adobelogin.com; Path=/"}}`),
		netEvent("requestWillBeSent", `{"requestId":"plain","request":{"url":"`+allowed+`/x","headers":{}}}`),
		netEvent("requestWillBeSentExtraInfo", `{"requestId":"plain","associatedCookies":[],"headers":{"Cookie":"a=kept-cookie"}}`),
		netEvent("responseReceived", `{"requestId":"plain","response":{"url":"`+allowed+`/x","headers":{"Set-Cookie":"a=kept-set"}}}`),
		netEvent("responseReceivedExtraInfo", `{"requestId":"plain","statusCode":200,"headers":{"Set-Cookie":"a=kept-xset"}}`),
		ev("Fetch.requestPaused", `{"requestId":"fidp","request":{"url":"`+idpOrigin+`/ims/authorize","headers":{"Cookie":"s=secret-fetch"}},"resourceType":"XHR","responseStatusCode":200,"responseHeaders":[{"name":"Set-Cookie","value":"s=secret-fetchset"},{"name":"X-Kept","value":"kept-fetchhdr"}]}`),
		ev("Fetch.requestPaused", `{"requestId":"fplain","request":{"url":"`+allowed+`/y","headers":{"Cookie":"a=kept-fetch"}},"resourceType":"XHR"}`),
		netEvent("webSocketCreated", `{"requestId":"ws","url":"wss://ims-na1.adobelogin.com/socket"}`),
		netEvent("webSocketWillSendHandshakeRequest", `{"requestId":"ws","timestamp":1,"wallTime":1,"request":{"headers":{"Cookie":"s=secret-ws"}}}`),
		netEvent("webSocketHandshakeResponseReceived", `{"requestId":"ws","timestamp":1,"response":{"status":101,"statusText":"","headers":{"Set-Cookie":"s=secret-wsset"},"requestHeaders":{"Cookie":"s=secret-wsrq"}}}`),
		ev("DOMStorage.domStorageItemAdded", `{"storageId":{"securityOrigin":"`+idpOrigin+`","isLocalStorage":true},"key":"k","newValue":"secret-dom"}`),
		ev("DOMStorage.domStorageItemUpdated", `{"storageId":{"storageKey":"`+stgOrigin+`/","isLocalStorage":true},"key":"k","oldValue":"","newValue":"secret-domkey"}`),
		ev("Storage.indexedDBContentUpdated", `{"origin":"`+stgOrigin+`","storageKey":"`+stgOrigin+`/","bucketId":"b","databaseName":"secret-idb","objectStoreName":"o"}`),
		ev("Storage.cacheStorageContentUpdated", `{"origin":"`+idpOrigin+`","storageKey":"`+idpOrigin+`/","bucketId":"b","cacheName":"secret-cache"}`),
		ev("Storage.sharedStorageAccessed", `{"accessTime":1,"scope":"window","type":"documentGet","mainFrameId":"F","ownerOrigin":"`+idpOrigin+`","ownerSite":"https://adobelogin.com","params":{"key":"secret-shared"}}`),
		ev("DOMStorage.domStorageItemAdded", `{"storageId":{"securityOrigin":"`+allowed+`","isLocalStorage":true},"key":"k","newValue":"kept-dom"}`),
		netEvent("requestWillBeSent", `{"requestId":"end","request":{"url":"`+allowed+`/end","headers":{}}}`),
		netEvent("loadingFinished", `{"requestId":"end","timestamp":1,"encodedDataLength":1}`),
	}
}

func TestCDPStripsSignInCookiesFromEvents(t *testing.T) {
	env, _ := setupFakeProtect(t, func(_, allowed string) fakeScript {
		return afterEnable(credentialEventScript(allowed))
	})
	var r struct {
		Events []struct {
			Method string `json:"method"`
			Text   string `json:"text"`
		} `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.eventsAfter("+jsArgs("chrome-2", [][]any{{"Network.enable", map[string]any{}}}, "Network.loadingFinished", "end")+")", &r)
	b, _ := json.Marshal(r)
	for _, e := range r.Events {
		if strings.Contains(e.Text, "secret") {
			t.Errorf("%s carried a sign-in cookie or storage entry: %s", e.Method, e.Text)
		}
	}
	for _, kept := range []string{"kept-auth", "kept-cookie", "kept-set", "kept-xset", "kept-fetch", "kept-fetchhdr", "kept-dom", "/ims/token", "/ims/authorize", "wss://ims-na1.adobelogin.com/socket"} {
		if !strings.Contains(string(b), kept) {
			t.Errorf("an event lost %s: %s", kept, b)
		}
	}
	credentialLogClean(t, env)
}

func TestCDPRefusesSignInStorageCommands(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript {
		return withPage(func(method string, _ json.RawMessage) (string, []string) {
			switch method {
			case "Storage.getTrustTokens":
				return `{"tokens":[{"issuerOrigin":"` + idpOrigin + `","count":1},{"issuerOrigin":"https://issuer.example.test","count":2}]}`, nil
			case "Storage.getAffectedUrlsForThirdPartyCookieMetadata":
				return `{"matchedUrls":["` + idpOrigin + `/x","https://cdn.example.test/y"]}`, nil
			case "Storage.getStorageKeyForFrame":
				return `{"storageKey":"https://kept.example.test/"}`, nil
			case "IndexedDB.requestDatabaseNames":
				return `{"databaseNames":["kept-db"]}`, nil
			case "CacheStorage.requestCacheNames":
				return `{"caches":[{"cacheId":"c","securityOrigin":"https://x.ims-na1.adobelogin.com","storageKey":"https://x.ims-na1.adobelogin.com/","cacheName":"kept-subcache"}]}`, nil
			}
			if strings.HasPrefix(method, "Storage.") || strings.HasPrefix(method, "DOMStorage.") || strings.HasPrefix(method, "IndexedDB.") || strings.HasPrefix(method, "CacheStorage.") || strings.HasPrefix(method, "FileSystem.") {
				return `{"entries":[{"key":"k","value":"secret-store"}]}`, nil
			}
			return "{}", nil
		})
	})
	refused := [][]any{
		{"DOMStorage.getDOMStorageItems", map[string]any{"storageId": map[string]any{"securityOrigin": idpOrigin, "isLocalStorage": true}}, "page"},
		{"DOMStorage.getDOMStorageItems", map[string]any{"storageId": map[string]any{"storageKey": stgOrigin + "/", "isLocalStorage": false}}, "page"},
		{"DOMStorage.setDOMStorageItem", map[string]any{"storageId": map[string]any{"securityOrigin": idpOrigin, "isLocalStorage": true}, "key": "k", "value": "v"}, "page"},
		{"IndexedDB.requestDatabaseNames", map[string]any{"securityOrigin": idpOrigin}, "page"},
		{"IndexedDB.requestData", map[string]any{"storageKey": idpOrigin + "/", "databaseName": "d", "objectStoreName": "o", "indexName": "", "skipCount": 0, "pageSize": 10}, "page"},
		{"IndexedDB.getMetadata", map[string]any{"storageBucket": map[string]any{"storageKey": idpOrigin + "/"}, "databaseName": "d", "objectStoreName": "o"}, "page"},
		{"CacheStorage.requestCacheNames", map[string]any{"securityOrigin": "https://IMS-NA1.adobelogin.com."}, "page"},
		{"CacheStorage.requestEntries", map[string]any{"cacheId": idpOrigin + "/|c1"}, "page"},
		{"FileSystem.getDirectory", map[string]any{"bucketFileSystemLocator": map[string]any{"storageKey": idpOrigin + "/", "pathComponents": []string{}}}},
		{"Storage.clearDataForOrigin", map[string]any{"origin": idpOrigin, "storageTypes": "all"}},
		{"Storage.clearDataForStorageKey", map[string]any{"storageKey": "https://cdn.example.test/^0https://adobelogin.com", "storageTypes": "all"}},
		{"Storage.getUsageAndQuota", map[string]any{"origin": "http://ims-na1.adobelogin.com:8080"}},
		{"Storage.getSharedStorageEntries", map[string]any{"ownerOrigin": idpOrigin}},
		{"Storage.getSharedStorageMetadata", map[string]any{"ownerOrigin": idpOrigin}},
		{"Storage.getInterestGroupDetails", map[string]any{"ownerOrigin": idpOrigin, "name": "g"}},
	}
	answered := [][]any{
		{"Storage.getTrustTokens", map[string]any{}},
		{"Storage.getAffectedUrlsForThirdPartyCookieMetadata", map[string]any{"firstPartyUrl": "https://example.test/", "thirdPartyUrls": []string{idpOrigin + "/x", "https://cdn.example.test/y"}}},
		{"Storage.getStorageKeyForFrame", map[string]any{"frameId": "F"}},
		{"IndexedDB.requestDatabaseNames", map[string]any{"securityOrigin": env.allowed}, "page"},
		{"CacheStorage.requestCacheNames", map[string]any{"securityOrigin": "https://x.ims-na1.adobelogin.com"}, "page"},
		{"IndexedDB.requestDatabaseNames", map[string]any{"securityOrigin": "https://auth.services.adobe.com"}, "page"},
		{"IndexedDB.requestDatabaseNames", map[string]any{"securityOrigin": "https://adobeid-na1.services.adobe.com"}, "page"},
		{"IndexedDB.requestDatabaseNames", map[string]any{"securityOrigin": "https://x.ims-na1.adobelogin.com"}, "page"},
	}
	out, b := outcomesOf(t, env, append(append([][]any{}, refused...), answered...))
	for i, o := range out {
		switch {
		case i < len(refused) && (o.OK || !strings.HasPrefix(o.Error.Message, "slicc: refused: ")):
			t.Errorf("call %d %s: want refused, got %s", i, o.Method, b)
		case i < len(refused) && strings.Contains(o.Error.Message, "adobelogin"):
			t.Errorf("call %d %s: the refusal names the origin: %q", i, o.Method, o.Error.Message)
		case i >= len(refused) && !o.OK:
			t.Errorf("call %d %s: refused: %s", i, o.Method, b)
		}
	}
	tokens, urls, key, names := string(out[len(refused)].Result), string(out[len(refused)+1].Result), string(out[len(refused)+2].Result), string(out[len(refused)+3].Result)
	if strings.Contains(tokens, "adobelogin") || !strings.Contains(tokens, "issuer.example.test") {
		t.Errorf("Storage.getTrustTokens: %s", tokens)
	}
	if strings.Contains(urls, "adobelogin") || !strings.Contains(urls, "cdn.example.test") {
		t.Errorf("Storage.getAffectedUrlsForThirdPartyCookieMetadata: %s", urls)
	}
	if sub := string(out[len(refused)+4].Result); !strings.Contains(sub, "kept-subcache") {
		t.Errorf("a sub-host of a sign-in origin lost its caches: %s", sub)
	}
	for i := len(refused) + 5; i < len(out); i++ {
		if !strings.Contains(string(out[i].Result), "kept-db") {
			t.Errorf("call %d: a login-UI host or a sub-host lost its databases: %s", i, out[i].Result)
		}
	}
	if !strings.Contains(key, "kept.example.test") || !strings.Contains(names, "kept-db") {
		t.Errorf("the controls lost their answers: %s %s", key, names)
	}
	if strings.Contains(b, "secret") {
		t.Errorf("an answer carried sign-in storage: %s", b)
	}
	for _, m := range fake.received() {
		for _, c := range refused {
			if strings.Contains(m, `"`+c[0].(string)+`"`) && strings.Contains(m, "adobelogin") {
				t.Errorf("the browser got %s", m)
			}
		}
	}
	credentialLogClean(t, env)
}

func TestCDPKeepsSignInPagesDrivable(t *testing.T) {
	page := idpOrigin + "/ims/authorize/v2"
	info := `{"targetId":"T1","type":"page","url":"` + page + `","title":"Sign in","attached":false}`
	env, _ := setupFakeProtect(t, func(string, string) fakeScript {
		attach := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.createTarget":
				return `{"targetId":"T1"}`, []string{`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + info + `}}`}
			case "Target.getTargets":
				return `{"targetInfos":[` + info + `]}`, nil
			case "Target.attachToTarget":
				result, after := attach(method, params)
				for i := range after {
					after[i] = strings.Replace(after[i], `"url":"about:blank"`, `"url":"`+page+`"`, 1)
				}
				return result, after
			}
			return attach(method, params)
		}
	})
	var r map[string]cdpOutcome
	var raw map[string]json.RawMessage
	env.evalJSON(t, "linkTest.protect.drivePage("+jsArgs("chrome-2", page)+")", &raw)
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"create", "targets", "attach", "navigate", "evaluate", "document", "click", "fill"} {
		if !r[k].OK {
			t.Errorf("%s on a sign-in page: %s", k, b)
		}
	}
	if !strings.Contains(string(raw["targets"]), page) {
		t.Errorf("the sign-in page's listing lost its URL: %s", raw["targets"])
	}
}

func cookieIssue(id, details string) string {
	return `{"method":"Audits.issueAdded","params":{"issue":{"code":"CookieIssue","issueId":"` + id + `","details":{"cookieIssueDetails":{` + details + `"cookieWarningReasons":[],"cookieExclusionReasons":["ExcludeSameSiteStrict"],"operation":"SetCookie"}}}}}`
}

func deprecationIssue(id, url string) string {
	return `{"method":"Audits.issueAdded","params":{"issue":{"code":"DeprecationIssue","issueId":"` + id + `","details":{"deprecationIssueDetails":{"sourceCodeLocation":{"url":"` + url + `","lineNumber":1,"columnNumber":1},"type":"X"}}}}}`
}

func TestCDPDropsCookieIssuesThatNameSignInOrSevensCookies(t *testing.T) {
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		host := strings.TrimPrefix(strings.Split(seven, ":")[1], "//")
		return scriptedAnswers(nil, map[string][]string{"Network.enable": {
			cookieIssue("secret-req", `"request":{"requestId":"r1","url":"`+idpOrigin+`/ims/token"},"rawCookieLine":"s=1",`),
			cookieIssue("secret-domain", `"cookie":{"name":"s","path":"/","domain":".adobelogin.com"},`),
			cookieIssue("secret-url", `"cookieUrl":"`+stgOrigin+`/x",`),
			cookieIssue("secret-raw", `"rawCookieLine":"s=1; Domain=adobelogin.com",`),
			cookieIssue("secret-rawonly", `"rawCookieLine":"s=1",`),
			cookieIssue("secret-sevenraw", `"rawCookieLine":"s=1; Domain=`+host+`",`),
			cookieIssue("secret-sevenurl", `"cookieUrl":"`+seven+`/x",`),
			deprecationIssue("secret-dep", idpOrigin+"/ims/app.js"),
			cookieIssue("kept-issue", `"cookie":{"name":"a","path":"/","domain":"example.test"},"cookieUrl":"https://example.test/","request":{"requestId":"q","url":"https://example.test/"},"rawCookieLine":"a=1; Domain=example.test",`),
			deprecationIssue("kept-dep", allowed+"/app.js"),
			netEvent("loadingFinished", `{"requestId":"end","timestamp":1,"encodedDataLength":1}`),
		}})
	})
	var r struct {
		Events []struct {
			Method string `json:"method"`
			Text   string `json:"text"`
		} `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.eventsAfter("+jsArgs("chrome-2", [][]any{{"Network.enable", map[string]any{}}}, "Network.loadingFinished", "end")+")", &r)
	b, _ := json.Marshal(r)
	for _, e := range r.Events {
		if strings.Contains(e.Text, "secret") {
			t.Errorf("%s reached the page: %s", e.Method, e.Text)
		}
	}
	for _, kept := range []string{"kept-issue", "kept-dep"} {
		if !strings.Contains(string(b), kept) {
			t.Errorf("the control %s was dropped: %s", kept, b)
		}
	}
	credentialLogClean(t, env)
}
