package integration

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"
)

func TestZZPageLoadStress(t *testing.T) {
	if os.Getenv("ZZ_STRESS") == "" {
		t.Skip()
	}
	var times []time.Duration
	for i := 0; i < 40; i++ {
		ok := t.Run("iter", func(t *testing.T) {
			b := launchBrowser(t)
			srv := pageServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var res struct {
				TargetID string `json:"targetId"`
			}
			url := srv.URL + "/index.html"
			start := time.Now()
			if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &res); err != nil {
				t.Fatal(err)
			}
			s, err := b.client.Attach(ctx, res.TargetID)
			if err != nil {
				t.Fatal(err)
			}
			p := &page{t: t, b: b, target: res.TargetID, session: s}
			p.waitLoaded(url, 60*time.Second)
			d := time.Since(start)
			times = append(times, d)
			t.Logf("iteration %d: loaded in %s; requests:\n%s", i, d, pageRequests.since(start))
		})
		_ = ok
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("load times sorted: %v", times)
}
