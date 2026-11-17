package integration

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	scalePages     = 60
	scaleIframes   = 600
	scaleProtected = 8
	scaleWorkers   = 40
	scaleLimit     = time.Second
)

type scaleTargets struct {
	infos  map[string]string
	order  []string
	shown  []string
	hidden []string
}

func newScaleTargets(seven string) *scaleTargets {
	s := &scaleTargets{infos: map[string]string{}}
	add := func(id, typ, url, parent string, shown bool) {
		info := fmt.Sprintf(`{"targetId":%q,"type":%q,"url":%q,"title":%q,"attached":false,"browserContextId":"CTX"`, id, typ, url, url)
		if parent != "" {
			info += fmt.Sprintf(`,"parentFrameId":%q`, parent)
		}
		s.infos[id] = info + "}"
		s.order = append(s.order, id)
		if shown {
			s.shown = append(s.shown, id)
		} else {
			s.hidden = append(s.hidden, id)
		}
	}
	for i := range scalePages {
		add(fmt.Sprintf("PAGE%03d", i), "page", fmt.Sprintf("https://site%d.example/articles/%d?ref=feed#top", i, i), "", true)
	}
	for i := range scaleIframes {
		add(fmt.Sprintf("FRAME%03d", i), "iframe", fmt.Sprintf("https://ads%d.example/pagead/ads?client=ca-pub-%d&format=300x250&url=https%%3A%%2F%%2Fsite%d.example%%2F", i, i, i%scalePages), fmt.Sprintf("PAGE%03d", i%scalePages), true)
	}
	for i := range scaleProtected {
		add(fmt.Sprintf("SEVEN%03d", i), "iframe", fmt.Sprintf("%s/doc?n=scale-secret-%d", seven, i), fmt.Sprintf("PAGE%03d", i%scalePages), false)
	}
	for i := range scaleWorkers {
		add(fmt.Sprintf("WORKER%03d", i), "service_worker", fmt.Sprintf("https://site%d.example/sw.js", i%scalePages), "", true)
	}
	return s
}

func (s *scaleTargets) list() string {
	infos := make([]string, len(s.order))
	for i, id := range s.order {
		infos[i] = s.infos[id]
	}
	return `{"targetInfos":[` + strings.Join(infos, ",") + `]}`
}

func (s *scaleTargets) events() []string {
	var events []string
	for _, id := range s.order {
		events = append(events, `{"method":"Target.targetCreated","params":{"targetInfo":`+s.infos[id]+`}}`)
	}
	return events
}

func TestCDPGuardKeepsUpWithHundredsOfIframes(t *testing.T) {
	var targets *scaleTargets
	env, fake := setupFakeProtect(t, func(seven, _ string) fakeScript {
		targets = newScaleTargets(seven)
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.setDiscoverTargets":
				return "{}", targets.events()
			case "Target.getTargets":
				return targets.list(), nil
			case "Target.getTargetInfo":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				if info, ok := targets.infos[p.TargetID]; ok {
					return `{"targetInfo":` + info + `}`, nil
				}
			}
			return "{}", nil
		}
	})
	var r struct {
		Open   float64  `json:"open"`
		List   float64  `json:"list"`
		Info   float64  `json:"info"`
		Listed []string `json:"listed"`
		Shown  []string `json:"shown"`
		Hidden []struct {
			OK       bool   `json:"ok"`
			TargetID string `json:"targetId"`
			Code     int    `json:"code"`
		} `json:"hidden"`
	}
	env.evalJSON(t, "linkTest.protect.scale("+jsArgs("chrome-2", targets.shown, targets.hidden)+")", &r)
	discovered := false
	for _, raw := range fake.received() {
		if strings.Contains(raw, `"Target.setDiscoverTargets"`) {
			discovered = true
		}
	}
	if !discovered {
		t.Fatalf("the guard never asked the fake browser for its targets: %v", fake.received())
	}
	ms := func(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }
	t.Logf("%d targets (%d iframes): open %s, getTargets %s, %d getTargetInfo %s", len(targets.order), scaleIframes+scaleProtected, ms(r.Open), ms(r.List), len(targets.order), ms(r.Info))
	for name, took := range map[string]time.Duration{"opening the CDP stream": ms(r.Open), "Target.getTargets": ms(r.List)} {
		if took > scaleLimit {
			t.Errorf("%s with %d targets took %s, over %s", name, len(targets.order), took, scaleLimit)
		}
	}
	if took := ms(r.Info); took > 2*scaleLimit {
		t.Errorf("%d Target.getTargetInfo calls took %s, over %s", len(targets.order), took, 2*scaleLimit)
	}
	for _, id := range targets.shown {
		if !slices.Contains(r.Listed, id) {
			t.Errorf("getTargets left out the ordinary target %s", id)
		}
	}
	if len(r.Shown) != len(targets.shown) {
		t.Errorf("getTargetInfo answered %d of %d ordinary targets", len(r.Shown), len(targets.shown))
	}
	if len(r.Hidden) != len(targets.hidden) {
		t.Fatalf("getTargetInfo answered %d of %d protected iframes", len(r.Hidden), len(targets.hidden))
	}
	for i, id := range targets.hidden {
		if slices.Contains(r.Listed, id) {
			t.Errorf("getTargets listed the protected iframe %s", id)
		}
		if h := r.Hidden[i]; h.OK || h.Code != -32602 {
			t.Errorf("getTargetInfo on the protected iframe %s: %+v", id, h)
		}
	}
}
