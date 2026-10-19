package main

import (
	"context"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

type settleEnd int

const (
	settled settleEnd = iota
	settleTimedOut
	settleInterrupted
	settleLost
)

const settledLine = `{"t":"settled"}` + "\n"

type settler struct {
	quiet    time.Duration
	deadline <-chan struct{}
	show     func(agentctl.Event)
}

func (s settler) run(ctx context.Context, st *agentctl.Stream) settleEnd {
	defer func() { _ = st.Close() }()
	busy := st.Busy == nil || *st.Busy
	events := make(chan agentctl.Event)
	lost := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			e, err := st.Next()
			if err != nil {
				close(lost)
				return
			}
			select {
			case events <- e:
			case <-stop:
				return
			}
		}
	}()
	quiet := time.NewTimer(s.quiet)
	defer quiet.Stop()
	for {
		var fire <-chan time.Time
		if !busy {
			fire = quiet.C
		}
		select {
		case <-ctx.Done():
			return settleInterrupted
		case <-s.deadline:
			return settleTimedOut
		case <-lost:
			return settleLost
		case e := <-events:
			if e.T == agentctl.EventBusy {
				busy = e.Busy
			}
			s.show(e)
			quiet.Reset(s.quiet)
		case <-fire:
			return settled
		}
	}
}
