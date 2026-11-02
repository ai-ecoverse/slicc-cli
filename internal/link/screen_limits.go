package link

import (
	"errors"
	"github.com/pion/webrtc/v4"
	"time"
)

const ScreenAbuseReason = "seven opened streams faster than slicc allows; closed the link"

type rateBucket struct {
	at     time.Time
	tokens float64
}

func (b *rateBucket) take(now time.Time, rate float64) bool {
	if b.at.IsZero() {
		b.tokens = rate
	} else {
		b.tokens = min(rate, b.tokens+now.Sub(b.at).Seconds()*rate)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type screenLimits struct {
	frameChannels map[*channel]struct{}
	refusalStart  time.Time
	onLimit       func()
	clock         func() time.Time
	shed          bool
	peerChannels  int
	peerReserved  int
	idFull        bool
	channelRate   rateBucket
	ownStreams    int
	ownPending    int
	ownOpens      rateBucket
	opens         rateBucket
	credit        rateBucket
	control       rateBucket
	refused       [2]int
	refusalSecond int64
}

func (l *Link) OnScreenLimit(f func()) { l.mu.Lock(); l.screen.onLimit = f; l.mu.Unlock() }
func (l *Link) screenEnabled() bool    { return l.opts.Hello.Screen != nil }
func (l *Link) shedScreen() {
	l.mu.Lock()
	f := l.screen.onLimit
	if l.screen.shed {
		l.mu.Unlock()
		return
	}
	l.screen.shed = true
	l.mu.Unlock()
	if f != nil {
		f()
	}
}
func (l *Link) admitChannel() bool {
	if !l.screenEnabled() {
		return true
	}
	limit := 260
	if l.role.peer() == RoleNode {
		limit = 16
	}
	l.mu.Lock()
	ok := l.screen.peerChannels < limit
	if ok {
		l.screen.peerChannels++
	}
	l.mu.Unlock()
	if !ok {
		l.shedScreen()
	}
	return ok
}
func (l *Link) admitOpen() bool {
	if !l.screenEnabled() {
		return true
	}
	streams, pending, rate := 256, 32, 512.0
	if l.role.peer() == RoleNode {
		streams, pending, rate = 16, 8, 32
	}
	l.mu.Lock()
	live, waiting := 0, 0
	for _, s := range l.streams {
		if !s.local {
			live++
			select {
			case <-s.answered:
			default:
				waiting++
			}
		}
	}
	ok := live+l.screen.peerReserved < streams && waiting+l.screen.peerReserved < pending && l.screen.opens.take(l.screenNow(), rate)
	if ok {
		l.screen.peerReserved++
	}
	l.mu.Unlock()
	if !ok {
		l.shedScreen()
	}
	return ok
}
func (l *Link) refuse(ch *channel, f Frame) {
	_ = ch.send(EncodeFrame(FrameReset, f.ID, []byte("EBUSY")))
	l.mu.Lock()
	now := l.screenNow().Unix()
	prev := l.screen.refusalSecond
	if now != prev {
		if now == prev+1 {
			l.screen.refused[0] = l.screen.refused[1]
		} else {
			l.screen.refused[0] = 0
		}
		l.screen.refused[1] = 0
		l.screen.refusalSecond = now
	}
	if l.screen.refusalStart.IsZero() || now > prev+1 {
		l.screen.refusalStart = l.screenNow()
	}
	l.screen.refused[1]++
	abuse := l.screenNow().Sub(l.screen.refusalStart) >= 2*time.Second && l.screen.refused[0] > 32 && l.screen.refused[1] > 32
	l.mu.Unlock()
	if !ch.control {
		_ = ch.dc.Close()
	}
	if abuse {
		l.fail(errors.New(ScreenAbuseReason))
	}
}
func (l *Link) screenFrame(t FrameType) {
	if !l.screenEnabled() {
		return
	}
	l.mu.Lock()
	ok := true
	switch t {
	case FrameCredit:
		ok = l.screen.credit.take(l.screenNow(), 8192)
	case FrameOpen, FrameOpened, FrameEnd, FrameReset:
		ok = l.screen.control.take(l.screenNow(), 1024)
	}
	l.mu.Unlock()
	if !ok {
		l.shedScreen()
	}
}

func (l *Link) ScreenAllowed() bool { l.mu.Lock(); defer l.mu.Unlock(); return !l.screen.shed }

func (l *Link) reserveOwn() bool {
	if !l.screenEnabled() {
		return true
	}
	limit, pending, rate := 16, 8, 32.0
	if l.role == RolePage {
		limit, pending, rate = 256, 32, 512
	}
	l.mu.Lock()
	ok := l.screen.ownStreams < limit && l.screen.ownPending < pending && l.screen.ownOpens.take(l.screenNow(), rate)
	if ok {
		l.screen.ownStreams++
		l.screen.ownPending++
	}
	l.mu.Unlock()
	if !ok {
		l.shedScreen()
	}
	return ok
}
func (l *Link) finishOwn(keep bool) {
	if !l.screenEnabled() {
		return
	}
	l.mu.Lock()
	l.screen.ownPending--
	if !keep {
		l.screen.ownStreams--
	}
	l.mu.Unlock()
}

func (l *Link) screenNow() time.Time {
	if l.screen.clock != nil {
		return l.screen.clock()
	}
	return time.Now()
}

func (s *Stream) ScreenBufferRoom(n int) bool {
	l := s.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.screen.frameChannels == nil {
		l.screen.frameChannels = map[*channel]struct{}{}
	}
	l.screen.frameChannels[s.ch] = struct{}{}
	total := uint64(n)
	for ch := range l.screen.frameChannels {
		queued := ch.dc.BufferedAmount()
		if queued == 0 && ch != s.ch && (ch.dc.ReadyState() == webrtc.DataChannelStateClosed || ch.dc.ReadyState() == webrtc.DataChannelStateClosing) {
			delete(l.screen.frameChannels, ch)
			continue
		}
		total += queued
		if total > 1<<20 {
			return false
		}
	}
	return true
}
