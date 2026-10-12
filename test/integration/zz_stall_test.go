package integration

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type stallLog struct {
	mu    sync.Mutex
	lines []string
	stop  chan struct{}
}

var stalls = &stallLog{stop: make(chan struct{})}

func (s *stallLog) start() {
	if os.Getenv("CI") == "" {
		return
	}
	go func() {
		last := time.Now()
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case now := <-t.C:
				if gap := now.Sub(last); gap > time.Second {
					census := chromeCensus()
					s.mu.Lock()
					s.lines = append(s.lines, fmt.Sprintf("STALL %s..%s gap %s\n%s", last.Format("15:04:05.000"), now.Format("15:04:05.000"), gap.Round(time.Millisecond), census))
					s.mu.Unlock()
					now = time.Now()
				}
				last = now
			}
		}
	}()
}

func (s *stallLog) report() {
	close(s.stop)
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(os.Stderr, "STALLS: %d\n", len(s.lines))
	for _, l := range s.lines {
		fmt.Fprintln(os.Stderr, l)
	}
}
