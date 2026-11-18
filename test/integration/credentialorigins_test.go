package integration

import (
	"encoding/base64"
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
		{"Storage.overrideQuotaForOrigin", map[string]any{"origin": idpOrigin, "quotaSize": 1}},
		{"Storage.trackIndexedDBForOrigin", map[string]any{"origin": idpOrigin}},
		{"Storage.untrackIndexedDBForOrigin", map[string]any{"origin": stgOrigin}},
		{"Storage.trackCacheStorageForOrigin", map[string]any{"origin": idpOrigin}},
		{"Storage.trackCacheStorageForStorageKey", map[string]any{"storageKey": idpOrigin + "/"}},
		{"IndexedDB.deleteDatabase", map[string]any{"securityOrigin": idpOrigin, "databaseName": "d"}, "page"},
		{"CacheStorage.deleteCache", map[string]any{"cacheId": idpOrigin + "/|c1"}, "page"},
		{"DOMStorage.removeDOMStorageItem", map[string]any{"storageId": map[string]any{"securityOrigin": idpOrigin, "isLocalStorage": true}, "key": "k"}, "page"},
		{"DOMStorage.clearDOMStorageItems", map[string]any{"storageId": map[string]any{"storageKey": stgOrigin + "/", "isLocalStorage": false}}, "page"},
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
		{"IndexedDB.requestDatabaseNames", map[string]any{"storageKey": "https://cdn.example.test/^0https://adobe.com"}, "page"},
	}
	controls := [][]any{
		{"Storage.overrideQuotaForOrigin", map[string]any{"origin": env.allowed, "quotaSize": 1}},
		{"Storage.trackIndexedDBForOrigin", map[string]any{"origin": env.allowed}},
		{"Storage.untrackIndexedDBForOrigin", map[string]any{"origin": env.allowed}},
		{"Storage.trackCacheStorageForOrigin", map[string]any{"origin": env.allowed}},
		{"Storage.trackCacheStorageForStorageKey", map[string]any{"storageKey": env.allowed + "/"}},
		{"IndexedDB.deleteDatabase", map[string]any{"securityOrigin": env.allowed, "databaseName": "d"}, "page"},
		{"CacheStorage.deleteCache", map[string]any{"cacheId": env.allowed + "/|c1"}, "page"},
		{"DOMStorage.removeDOMStorageItem", map[string]any{"storageId": map[string]any{"securityOrigin": env.allowed, "isLocalStorage": true}, "key": "k"}, "page"},
		{"DOMStorage.clearDOMStorageItems", map[string]any{"storageId": map[string]any{"storageKey": env.allowed + "/", "isLocalStorage": false}}, "page"},
	}
	calls := append(append(append([][]any{}, refused...), answered...), controls...)
	out, b := outcomesOf(t, env, calls)
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
	for i := len(refused) + 5; i < len(refused)+len(answered); i++ {
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
			if strings.Contains(m, `"`+c[0].(string)+`"`) && strings.Contains(m, "adobelogin") && !strings.Contains(m, "x.ims-na1.adobelogin.com") {
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

func cookieLineScript(allowed string) []string {
	entry := func(line string) string {
		b, _ := json.Marshal(map[string]any{"blockedReasons": []string{"SecureOnly"}, "cookieLine": line})
		return string(b)
	}
	lines := func(ls ...string) string {
		out := make([]string, len(ls))
		for i, l := range ls {
			out[i] = entry(l)
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	mismatch := `{"blockedReasons":[],"cookieLine":"id=secret-mismatch; Domain=.adobelogin.com","cookie":{"name":"id","value":"x","domain":"example.test","path":"/"}}`
	return []string{
		netEvent("requestWillBeSent", `{"requestId":"line","request":{"url":"`+allowed+`/line","headers":{}}}`),
		netEvent("requestWillBeSentExtraInfo", `{"requestId":"line","associatedCookies":`+lines("id=secret-assocline; Domain=.adobelogin.com", "a=kept-assocline; Domain=example.test")+`,"headers":{}}`),
		netEvent("responseReceived", `{"requestId":"line","response":{"url":"`+allowed+`/line","headers":{}}}`),
		netEvent("responseReceivedExtraInfo", `{"requestId":"line","statusCode":200,"headers":{},"blockedCookies":`+lines(
			`id=secret-quoted; Domain=".adobelogin.com"; Path=/`,
			"id=secret-case; DoMaIn=IMS-NA1.AdobeLogin.COM",
			"id=secret-dot; domain=.ims-na1-stg1.adobelogin.com.",
			"id=secret-repeat; Domain=example.test; Domain=adobelogin.com",
			"id=secret-ctl\x01; Domain=example.test",
			"id=secret-nonhost; Domain=ad obe/login",
			"a=kept-line; Domain=example.test; Path=/",
			"b=kept-hostonly; Path=/",
			"c=kept-adobe; Domain=.adobe.com",
		)+`,"exemptedCookies":[`+mismatch+`,`+entry("id=secret-exempt; Domain=ims-na1.adobelogin.com")+`,`+entry("d=kept-exempt; Domain=www.adobe.com")+`]}`),
		netEvent("loadingFinished", `{"requestId":"line","timestamp":1,"encodedDataLength":1}`),
		netEvent("requestWillBeSent", `{"requestId":"end","request":{"url":"`+allowed+`/end","headers":{}}}`),
		netEvent("loadingFinished", `{"requestId":"end","timestamp":1,"encodedDataLength":1}`),
	}
}

func TestCDPDropsSignInCookieLinesFromExtraInfo(t *testing.T) {
	env, _ := setupFakeProtect(t, func(_, allowed string) fakeScript {
		return afterEnable(cookieLineScript(allowed))
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
			t.Errorf("%s carried a sign-in cookie line: %s", e.Method, e.Text)
		}
	}
	for _, kept := range []string{"kept-assocline", "kept-line", "kept-hostonly", "kept-adobe", "kept-exempt"} {
		if !strings.Contains(string(b), kept) {
			t.Errorf("an ordinary cookie line lost %s: %s", kept, b)
		}
	}
	credentialLogClean(t, env)
}

func plantDoorPaused(id, url string) string {
	return `{"method":"Fetch.requestPaused","params":{"requestId":"` + id + `","request":{"url":"` + url + `","headers":{}},"resourceType":"XHR","responseStatusCode":200}}`
}

func plantDoorWillBeSent(id, url string) string {
	return `{"sessionId":"$SESSION","method":"Network.requestWillBeSent","params":{"requestId":"` + id + `","loaderId":"L","documentURL":"` + url + `","request":{"url":"` + url + `","headers":{}},"timestamp":1,"wallTime":1,"initiator":{"type":"other"},"type":"XHR"}}`
}

func plantDoorPausedLinked(id, url string) string {
	return `{"sessionId":"$SESSION","method":"Fetch.requestPaused","params":{"requestId":"` + id + `","networkId":"` + id + `","request":{"url":"` + url + `","headers":{}},"resourceType":"XHR","responseStatusCode":200}}`
}

func plantDoorLinked(id, url string) []string {
	return []string{plantDoorWillBeSent(id, url), plantDoorPausedLinked(id, url)}
}

func plantDoorScript(seven, allowed string) fakeScript {
	sub := "https://x.ims-na1.adobelogin.com/x"
	events := []string{
		plantDoorPaused("fidp-dom", idpOrigin+"/ims"),
		plantDoorPaused("fidp-host", idpOrigin+"/ims"),
		plantDoorPaused("fidp-sc2", idpOrigin+"/ims"),
		plantDoorPaused("fidp-bin", idpOrigin+"/ims"),
		plantDoorPaused("fidp-cont", idpOrigin+"/ims"),
		plantDoorPaused("fplain-dom", allowed+"/x"),
		plantDoorPaused("fplain-bin", allowed+"/x"),
		plantDoorPaused("fplain-badbin", allowed+"/x"),
		plantDoorPaused("fplain-ok", allowed+"/x"),
		plantDoorPaused("fplain-adobe", allowed+"/x"),
		plantDoorPaused("fplain-cont", allowed+"/x"),
		plantDoorPaused("fsub-dom", sub),
		plantDoorPaused("fsub-host", sub),
		plantDoorPaused("fwww", "https://www.adobe.com/"),
		plantDoorPaused("fcookie-hdr", allowed+"/y"),
		plantDoorPaused("fseven", seven+"/x"),
		plantDoorPaused("fvault", "https://vault.sliccy.ai/x"),
	}
	return afterEnable(events)
}

func setCookieHeaders(name, value string) []map[string]string {
	return []map[string]string{{"name": name, "value": value}}
}

func TestCDPRefusesPlantingSignInCookiesThroughFetch(t *testing.T) {
	env, fake := setupFakeProtect(t, plantDoorScript)
	plantDom := "sid=PLANTED; Domain=.adobelogin.com; HttpOnly"
	plantHost := "sid=PLANTED; Path=/; HttpOnly"
	controlHost := "sid=CONTROL; Path=/; HttpOnly"
	plantAdobe := "sid=kept; Domain=.adobe.com; Path=/"
	plantBin := base64.StdEncoding.EncodeToString([]byte("Set-Cookie: sid=PLANTED; Domain=.adobelogin.com"))
	calls := [][]any{
		{"Network.enable", map[string]any{}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fidp-dom", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantDom)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fidp-host", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantHost)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fidp-sc2", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie2", plantHost)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fidp-bin", "responseCode": 200, "binaryResponseHeaders": plantBin}},
		{"Fetch.continueResponse", map[string]any{"requestId": "fidp-cont", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantDom)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fplain-dom", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantDom)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fplain-bin", "responseCode": 200, "binaryResponseHeaders": plantBin}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fplain-badbin", "responseCode": 200, "binaryResponseHeaders": "@@@"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fsub-dom", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantDom)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fsub-host", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", controlHost)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fplain-ok", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", "a=kept; Path=/")}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fplain-adobe", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantAdobe)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fwww", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", "a=kept; Path=/")}},
		{"Fetch.continueResponse", map[string]any{"requestId": "fplain-cont", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", "a=kept; Path=/")}},
		{"Fetch.continueRequest", map[string]any{"requestId": "fcookie-hdr", "headers": []map[string]string{{"name": "Cookie", "value": "sid=injected"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fseven", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantHost)}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fvault", "responseCode": 200, "responseHeaders": setCookieHeaders("Set-Cookie", plantHost)}},
	}
	var r struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", calls)+")", &r)
	b, _ := json.Marshal(r)
	if len(r.Out) != len(calls) {
		t.Fatalf("calls: %s", b)
	}
	refused := map[int]string{
		1: "sign-in origin", 2: "sign-in origin", 3: "sign-in origin", 4: "sign-in origin", 5: "sign-in origin",
		6: "sign-in origin", 7: "sign-in origin", 8: "sign-in origin", 9: "sign-in origin",
		16: "didn't pass this paused request", 17: "didn't pass this paused request",
	}
	allowed := map[int]bool{0: true, 10: true, 11: true, 12: true, 13: true, 14: true, 15: true}
	for i, o := range r.Out {
		want, refuse := refused[i]
		switch {
		case refuse && (o.OK || !strings.HasPrefix(o.Error.Message, "slicc: refused: ") || !strings.Contains(o.Error.Message, want)):
			t.Errorf("call %d %s: want refused with %q, got %+v (%s)", i, o.Method, want, o, b)
		case refuse && strings.Contains(o.Error.Message, "adobelogin"):
			t.Errorf("call %d %s: the refusal names the origin: %q", i, o.Method, o.Error.Message)
		case allowed[i] && !o.OK:
			t.Errorf("call %d %s: the control was refused: %s", i, o.Method, b)
		case !refuse && !allowed[i]:
			t.Fatalf("call %d unclassified", i)
		}
	}
	if r.Out[16].OK || r.Out[17].OK {
		t.Fatalf("seven/vault pause is client-fulfillable; stop — pre-existing hole: seven=%+v vault=%+v", r.Out[16], r.Out[17])
	}
	for _, m := range fake.received() {
		if strings.Contains(m, "PLANTED") {
			t.Errorf("the browser got a planted cookie: %s", m)
		}
		for _, id := range []string{"fidp-dom", "fidp-host", "fidp-sc2", "fidp-bin", "fidp-cont", "fplain-dom", "fplain-bin", "fplain-badbin", "fsub-dom", "fseven", "fvault"} {
			if strings.Contains(m, `"requestId":"`+id+`"`) && (strings.Contains(m, `"Fetch.fulfillRequest"`) || strings.Contains(m, `"Fetch.continueResponse"`)) {
				t.Errorf("a refused plant reached the browser: %s", m)
			}
		}
	}
	credentialLogClean(t, env)
}

func plantBinaryParts(parts ...string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(parts, "\x00")))
}

func hostilePlantScript(_, allowed string) fakeScript {
	events := []string{
		plantDoorPaused("hidp-map", idpOrigin+"/ims"),
		plantDoorPaused("hidp-space", idpOrigin+"/ims"),
		plantDoorPaused("hidp-ctlname", idpOrigin+"/ims"),
		plantDoorPaused("hidp-ctlval", idpOrigin+"/ims"),
		plantDoorPaused("hidp-nonstr", idpOrigin+"/ims"),
		plantDoorPaused("hidp-phrasebad", idpOrigin+"/ims"),
		plantDoorPaused("hidp-phrasectl", idpOrigin+"/ims"),
		plantDoorPaused("hidp-phrasecookie", idpOrigin+"/ims"),
		plantDoorPaused("hidp-code", idpOrigin+"/ims"),
		plantDoorPaused("hplain-map", allowed+"/x"),
		plantDoorPaused("hplain-ok", allowed+"/x"),
		plantDoorPaused("hplain-nf", allowed+"/x"),
		plantDoorPaused("hplain-404", allowed+"/x"),
		plantDoorPaused("hplain-empty", allowed+"/x"),
		plantDoorPaused("hplain-colon", allowed+"/x"),
		plantDoorPaused("hplain-long", allowed+"/x"),
		plantDoorPaused("hplain-set", allowed+"/x"),
		plantDoorPaused("hplain-cont", allowed+"/x"),
	}
	return afterEnable(events)
}

func TestCDPRefusesHostilePlantFetchShapes(t *testing.T) {
	env, fake := setupFakeProtect(t, hostilePlantScript)
	longPhrase := strings.Repeat("a", 4500)
	calls := [][]any{
		{"Network.enable", map[string]any{}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-map", "responseCode": 200, "responseHeaders": map[string]any{"Set-Cookie": "sid=PLANTED; Path=/"}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-space", "responseCode": 200, "responseHeaders": []map[string]any{{"name": " Set-Cookie", "value": "sid=PLANTED; Path=/"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-ctlname", "responseCode": 200, "responseHeaders": []map[string]any{{"name": "Set-Cookie\x01", "value": "sid=PLANTED; Path=/"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-ctlval", "responseCode": 200, "responseHeaders": []map[string]any{{"name": "Set-Cookie", "value": "sid=PLANTED\r\nX: 1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-nonstr", "responseCode": 200, "responseHeaders": []map[string]any{{"name": "Set-Cookie", "value": 7}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-phrasebad", "responseCode": 200, "responsePhrase": 7}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-phrasectl", "responseCode": 200, "responsePhrase": "OK\nSet-Cookie: sid=PLANTED"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-phrasecookie", "responseCode": 200, "responsePhrase": "OK: sid=PLANTED; Domain=.adobelogin.com"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-code", "responseCode": "200", "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-map", "responseCode": 200, "responseHeaders": map[string]any{"X-Kept": "1"}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-ok", "responseCode": 200, "responsePhrase": "OK", "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-nf", "responseCode": 200, "responsePhrase": "Not Found", "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-404", "responseCode": 200, "responsePhrase": "404", "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-empty", "responseCode": 200, "responsePhrase": "", "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-colon", "responseCode": 200, "responsePhrase": "Moved: see Location", "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-long", "responseCode": 200, "responsePhrase": longPhrase, "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-set", "responseCode": 200, "responseHeaders": []map[string]string{{"name": "Set-Cookie", "value": "a=CONTROL; Path=/"}}}},
		{"Fetch.continueResponse", map[string]any{"requestId": "hplain-cont", "responseCode": 200, "responsePhrase": "OK", "responseHeaders": []map[string]string{{"name": "Set-Cookie", "value": "a=CONTROL; Path=/"}}}},
	}
	var r struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", calls)+")", &r)
	b, _ := json.Marshal(r)
	if len(r.Out) != len(calls) {
		t.Fatalf("calls: %s", b)
	}
	refusedIDs := map[string]bool{
		"hidp-map": true, "hidp-space": true, "hidp-ctlname": true, "hidp-ctlval": true, "hidp-nonstr": true,
		"hidp-phrasebad": true, "hidp-phrasectl": true, "hidp-phrasecookie": true, "hidp-code": true, "hplain-map": true,
	}
	for i, o := range r.Out {
		id, _ := calls[i][1].(map[string]any)["requestId"].(string)
		refuse := refusedIDs[id]
		switch {
		case i == 0 && !o.OK:
			t.Errorf("Network.enable refused: %s", b)
		case refuse && (o.OK || !strings.HasPrefix(o.Error.Message, "slicc: refused: ") || !strings.Contains(o.Error.Message, "sign-in origin")):
			t.Errorf("call %d %s %s: want hostile shape refused, got %+v (%s)", i, o.Method, id, o, b)
		case refuse && strings.Contains(o.Error.Message, "adobelogin"):
			t.Errorf("call %d %s: the refusal names the origin: %q", i, o.Method, o.Error.Message)
		case !refuse && i > 0 && !o.OK:
			t.Errorf("call %d %s %s: the control was refused: %s", i, o.Method, id, b)
		}
	}
	for _, m := range fake.received() {
		if strings.Contains(m, "PLANTED") {
			t.Errorf("the browser got a planted cookie: %s", m)
		}
		for id := range refusedIDs {
			if strings.Contains(m, `"requestId":"`+id+`"`) && (strings.Contains(m, `"Fetch.fulfillRequest"`) || strings.Contains(m, `"Fetch.continueResponse"`)) {
				t.Errorf("a refused hostile shape reached the browser: %s", m)
			}
		}
	}
	credentialLogClean(t, env)
}

func hostileBinaryPlantScript(_, allowed string) fakeScript {
	var events []string
	for _, row := range []struct{ id, url string }{
		{"sanity-ok", allowed + "/x"},
		{"hidp-binlf", idpOrigin + "/ims"},
		{"hidp-binstatuslf", idpOrigin + "/ims"},
		{"hidp-bincr", idpOrigin + "/ims"},
		{"hidp-binfold", idpOrigin + "/ims"},
		{"hidp-binfold2", idpOrigin + "/ims"},
		{"hidp-binnum", idpOrigin + "/ims"},
		{"hidp-binarr", idpOrigin + "/ims"},
		{"hidp-binobj", idpOrigin + "/ims"},
		{"hidp-bintab", idpOrigin + "/ims"},
		{"hidp-binctl", idpOrigin + "/ims"},
		{"hidp-bindel", idpOrigin + "/ims"},
		{"hidp-binhttp", idpOrigin + "/ims"},
		{"hidp-binstatus2", idpOrigin + "/ims"},
		{"hidp-binbad", idpOrigin + "/ims"},
		{"hidp-binempty", idpOrigin + "/ims"},
		{"hidp-binx", idpOrigin + "/ims"},
		{"hplain-binset", allowed + "/x"},
	} {
		events = append(events, plantDoorLinked(row.id, row.url)...)
	}
	return afterEnable(events)
}

func TestCDPRefusesHostileBinaryPlantFetchShapes(t *testing.T) {
	env, fake := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		return withPage(hostileBinaryPlantScript(seven, allowed))
	})
	page := "page"
	calls := [][]any{
		{"Network.enable", map[string]any{}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "sanity-ok", "responseCode": 200, "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binlf", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-A: 1\nSet-Cookie: sid=PLANTED; Domain=.adobelogin.com")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binstatuslf", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("HTTP/1.1 200 OK\nSet-Cookie: sid=PLANTED; Domain=.adobelogin.com")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-bincr", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-A: 1\r")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binfold", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-A: 1", " X-Kept: 1")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binfold2", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-A: 1", " Domain=.adobelogin.com")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binnum", "responseCode": 200, "binaryResponseHeaders": 7}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binarr", "responseCode": 200, "binaryResponseHeaders": []any{}}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binobj", "responseCode": 200, "binaryResponseHeaders": map[string]any{"x": 1}}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-bintab", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("\tX-Kept: 1")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binctl", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-\x01A: 1")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-bindel", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-\x7fA: 1")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binhttp", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("http/1.1 200 OK")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binstatus2", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-A: 1", "HTTP/1.1 200 OK")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binbad", "responseCode": 200, "binaryResponseHeaders": "@@@"}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binempty", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("", "")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hidp-binx", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("X-Kept: 1")}, page},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "hplain-binset", "responseCode": 200, "binaryResponseHeaders": plantBinaryParts("Set-Cookie: a=CONTROL; Path=/")}, page},
	}
	out, b := outcomesOf(t, env, calls)
	refusedIDs := map[string]bool{
		"hidp-binlf": true, "hidp-binstatuslf": true, "hidp-bincr": true, "hidp-binfold": true, "hidp-binfold2": true,
		"hidp-binnum": true, "hidp-binarr": true, "hidp-binobj": true,
		"hidp-bintab": true, "hidp-binctl": true, "hidp-bindel": true, "hidp-binhttp": true, "hidp-binstatus2": true, "hidp-binbad": true,
	}
	for i, o := range out {
		id, _ := calls[i][1].(map[string]any)["requestId"].(string)
		refuse := refusedIDs[id]
		switch {
		case i == 0 && !o.OK:
			t.Errorf("Network.enable refused: %s", b)
		case i == 1 && !o.OK:
			t.Errorf("sanity control refused (fixture never handed the pause): %s", b)
		case refuse && (o.OK || !strings.HasPrefix(o.Error.Message, "slicc: refused: ") || !strings.Contains(o.Error.Message, "sign-in origin")):
			t.Errorf("call %d %s %s: want hostile shape refused, got %+v (%s)", i, o.Method, id, o, b)
		case refuse && strings.Contains(o.Error.Message, "adobelogin"):
			t.Errorf("call %d %s: the refusal names the origin: %q", i, o.Method, o.Error.Message)
		case !refuse && i > 0 && !o.OK:
			t.Errorf("call %d %s %s: the control was refused: %s", i, o.Method, id, b)
		}
	}
	for _, m := range fake.received() {
		if strings.Contains(m, "PLANTED") {
			t.Errorf("the browser got a planted cookie: %s", m)
		}
		for id := range refusedIDs {
			if strings.Contains(m, `"requestId":"`+id+`"`) && (strings.Contains(m, `"Fetch.fulfillRequest"`) || strings.Contains(m, `"Fetch.continueResponse"`)) {
				t.Errorf("a refused hostile shape reached the browser: %s", m)
			}
		}
	}
	credentialLogClean(t, env)
}
