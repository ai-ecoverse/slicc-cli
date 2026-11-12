package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPCloseProtectedKeepsLiveSevenUIAndVault(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		osLive := "https://seven.sliccy.ai/os/?n=kept-os"
		vaultLive := "https://vault.sliccy.ai/?n=kept-vault"
		errorUnreachable := "https://vault.sliccy.ai/missing?n=kept-error"
		ordinary := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
		osTab := map[string]any{"targetId": "T-os", "type": "page", "url": allowed + "/os-seed"}
		vaultTab := map[string]any{"targetId": "T-vault", "type": "page", "url": allowed + "/vault-seed"}
		errorTab := map[string]any{"targetId": "T-error", "type": "page", "url": allowed + "/error-seed"}
		bodies := map[string]*map[string]any{
			"P-ordinary": &ordinary, "T-os": &osTab, "T-vault": &vaultTab, "T-error": &errorTab,
		}
		closed := map[string]bool{}
		created := func(info map[string]any) string {
			raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
			return string(raw)
		}
		changed := func(info map[string]any) string {
			raw, _ := json.Marshal(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": info}})
			return string(raw)
		}
		navigated := func(session, id, url, unreachable string) string {
			frame := map[string]any{"id": id, "url": url}
			if unreachable != "" {
				frame["unreachableUrl"] = unreachable
			}
			raw, _ := json.Marshal(map[string]any{"sessionId": session, "method": "Page.frameNavigated", "params": map[string]any{"frame": frame}})
			return string(raw)
		}
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.setDiscoverTargets":
				return "{}", []string{created(ordinary), created(osTab), created(vaultTab), created(errorTab)}
			case "Target.getTargets":
				list := []any{}
				for id, info := range bodies {
					if closed[id] {
						continue
					}
					list = append(list, *info)
				}
				raw, _ := json.Marshal(map[string]any{"targetInfos": list})
				return string(raw), nil
			case "Target.getTargetInfo":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				if closed[p.TargetID] {
					return "", nil
				}
				if info, ok := bodies[p.TargetID]; ok {
					raw, _ := json.Marshal(map[string]any{"targetInfo": *info})
					return string(raw), nil
				}
			case "Target.attachToTarget":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				session := "S-" + p.TargetID
				events := []string{}
				switch p.TargetID {
				case "T-os":
					osTab["url"] = osLive
					events = append(events, changed(osTab), navigated(session, "T-os", osLive, ""))
				case "T-vault":
					vaultTab["url"] = vaultLive
					events = append(events, changed(vaultTab), navigated(session, "T-vault", vaultLive, ""))
				case "T-error":
					errorTab["url"] = "chrome-error://chromewebdata/"
					events = append(events, changed(errorTab), navigated(session, "T-error", "chrome-error://chromewebdata/", errorUnreachable))
				}
				return `{"sessionId":"` + session + `"}`, events
			case "Target.closeTarget":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				closed[p.TargetID] = true
				raw, _ := json.Marshal(map[string]any{"method": "Target.targetDestroyed", "params": map[string]any{"targetId": p.TargetID}})
				return `{"success":true}`, []string{string(raw)}
			case "Page.enable", "Runtime.enable", "Runtime.runIfWaitingForDebugger", "Target.detachFromTarget", "Target.setAutoAttach", "Fetch.enable", "Network.enable", "Network.setBypassServiceWorker", "Page.getFrameTree", "Page.getNavigationHistory", "Page.setLifecycleEventsEnabled":
				return "{}", nil
			}
			return "{}", nil
		}
	})
	var result struct {
		OS        cdpOutcome `json:"os"`
		OSGone    bool       `json:"osGone"`
		Vault     cdpOutcome `json:"vault"`
		VaultGone bool       `json:"vaultGone"`
		Error     cdpOutcome `json:"error"`
		ErrorGone bool       `json:"errorGone"`
	}
	env.evalJSON(t, "linkTest.protect.closeProtectedKeptFake('chrome-2')", &result)
	switch {
	case !result.OS.refused() || result.OSGone:
		t.Fatalf("closing a stream-touched live /os/ tab: %+v", result)
	case !result.Vault.refused() || result.VaultGone:
		t.Fatalf("closing a stream-touched live vault tab: %+v", result)
	case !result.Error.OK || !result.ErrorGone:
		t.Fatalf("closing a stream-touched committed error page: %+v", result)
	}
}
