package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

const HistoryTimeout = 10 * time.Second

var allTargets = map[string]any{"filter": []any{map[string]any{}}}

func (rl *relay) bootstrap(ctx context.Context) error {
	res, err := rl.syncCall(ctx, "", "Target.attachToBrowserTarget", nil)
	if err != nil {
		return err
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(res.Result, &att) != nil || att.SessionID == "" {
		return errors.New("Target.attachToBrowserTarget: no session")
	}
	rl.g.setRelaySession(att.SessionID)
	discover := map[string]any{"discover": true, "filter": allTargets["filter"]}
	if _, err := rl.syncCall(ctx, att.SessionID, "Target.setDiscoverTargets", discover); err != nil {
		return err
	}
	res, err = rl.syncCall(ctx, att.SessionID, "Target.getTargets", allTargets)
	if err != nil {
		return err
	}
	var list struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	if err := json.Unmarshal(res.Result, &list); err != nil {
		return fmt.Errorf("Target.getTargets: %w", err)
	}
	rl.g.observeAll(list.TargetInfos)
	return nil
}

func (rl *relay) syncCall(ctx context.Context, sessionID, method string, params any) (message, error) {
	msg, c := rl.g.call(sessionID, method, params)
	defer rl.g.cancel(c)
	if err := rl.conn.Write(ctx, websocket.MessageText, msg); err != nil {
		return message{}, fmt.Errorf("%s: %w", method, err)
	}
	for {
		select {
		case m := <-c.ch:
			if m.Error != nil {
				return m, fmt.Errorf("%s: %s", method, m.Error)
			}
			return m, nil
		default:
		}
		_, data, err := rl.reader.readMessage(ctx, rl.conn)
		if err != nil {
			return message{}, fmt.Errorf("%s: %w", method, err)
		}
		v := rl.g.fromBrowser(data)
		for _, out := range v.send {
			_ = rl.conn.Write(ctx, websocket.MessageText, out)
		}
		releaseReservations(v.releases)
	}
}

func (rl *relay) checkPage(data []byte) ([]byte, bool) {
	c, id, ok := parseCommand(data)
	return rl.admit(c, id, ok)
}

func (rl *relay) admit(c *command, id int64, ok bool) ([]byte, bool) {
	v := rl.g.fromCommand(c, id, ok)
	for {
		if v.history != nil {
			v = rl.resolveHistory(v.history)
		}
		if v.extension != nil {
			v = rl.resolveExtensionShown(v.extension)
		}
		if v.wait == nil {
			break
		}
		select {
		case <-v.wait:
		case <-rl.s.Done():
			return nil, false
		}
		v = rl.g.fromCommand(c, id, ok)
	}
	if v.reply != nil {
		if err := rl.writePage(v.reply); err != nil {
			_ = rl.conn.CloseNow()
			return nil, false
		}
	}
	return v.forward, true
}

func (rl *relay) resolveExtensionShown(c *command) pageVerdict {
	rl.g.mu.Lock()
	t := rl.g.sessionTargetLocked(c.session)
	frames := []string(nil)
	if t != nil {
		frames = rl.g.extensionFrameIDsLocked(t.id)
	}
	if t == nil || len(frames) == 0 {
		rl.g.mu.Unlock()
		return rl.g.afterExtensionShown(c, false, true)
	}
	ch := make(chan struct {
		shown bool
		ok    bool
	}, 1)
	send := rl.g.probeExtensionShownLocked(c.session, t.id, frames, func(shown, ok bool) [][]byte {
		ch <- struct {
			shown bool
			ok    bool
		}{shown, ok}
		return nil
	})
	rl.g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), HistoryTimeout)
	defer cancel()
	for _, msg := range send {
		if err := rl.conn.Write(ctx, websocket.MessageText, msg); err != nil {
			return rl.g.afterExtensionShown(c, true, false)
		}
	}
	select {
	case result := <-ch:
		return rl.g.afterExtensionShown(c, result.shown, result.ok)
	case <-ctx.Done():
		return rl.g.afterExtensionShown(c, true, false)
	case <-rl.s.Done():
		return rl.g.afterExtensionShown(c, true, false)
	}
}

func (rl *relay) resolveHistory(c *command) pageVerdict {
	msg, call := rl.g.call(c.session, "Page.getNavigationHistory", nil)
	defer rl.g.cancel(call)
	ctx, cancel := context.WithTimeout(context.Background(), HistoryTimeout)
	defer cancel()
	if err := rl.conn.Write(ctx, websocket.MessageText, msg); err != nil {
		return rl.g.afterHistory(c, message{}, false)
	}
	select {
	case m := <-call.ch:
		return rl.g.afterHistory(c, m, m.Error == nil)
	case <-ctx.Done():
	case <-rl.s.Done():
	}
	return rl.g.afterHistory(c, message{}, false)
}

const (
	maxParked      = 4096
	maxParkedBytes = 64 << 20
)

func (rl *relay) park(data []byte) bool {
	c, _, ok := parseCommand(data)
	if !ok || rl.g.startsUnheld(c) {
		return false
	}
	key := parkKey{session: c.session}
	if rl.g.browserScope(c.session) {
		key.target = c.str("targetId")
		if key.target == "" {
			return false
		}
	}
	rl.parkMu.Lock()
	defer rl.parkMu.Unlock()
	q, waiting := rl.parked[key]
	if !waiting {
		if rl.g.commandWait(c) == nil {
			return false
		}
		go rl.release(key)
	}
	if rl.parkedCount >= maxParked || rl.parkedBytes+len(data) > maxParkedBytes {
		rl.kill("ENOBUFS: too many commands wait for a target's guard")
		return true
	}
	reservation := rl.s.ReserveAllocation(len(data), false)
	if reservation == nil {
		rl.kill("ENOBUFS: shared HTTP/CDP allocation limit")
		return true
	}
	rl.parkedCount++
	rl.parkedBytes += len(data)
	copyData := make([]byte, len(data))
	copy(copyData, data)
	rl.parked[key] = append(q, parkedMessage{data: copyData, reservation: reservation})
	return true
}

func (rl *relay) release(key parkKey) {
	for {
		rl.parkMu.Lock()
		q := rl.parked[key]
		if len(q) == 0 {
			delete(rl.parked, key)
			rl.g.forgetUnknown(key.session)
			rl.parkMu.Unlock()
			return
		}
		item := q[0]
		data := item.data
		rl.parkMu.Unlock()
		if c, _, ok := parseCommand(data); ok {
			rl.g.awaitCommand(c, rl.g.commandWait(c), rl.s.Done())
		}
		rl.parkMu.Lock()
		rl.parked[key][0] = parkedMessage{}
		rl.parked[key] = rl.parked[key][1:]
		rl.parkedCount--
		rl.parkedBytes -= len(data)
		rl.parkMu.Unlock()
		out, ok := rl.checkPage(data)
		if ok && out != nil {
			ok = rl.conn.Write(context.Background(), websocket.MessageText, out) == nil
		}
		item.reservation.Release()
		if !ok {
			rl.clearParked(key)
			return
		}
	}
}

func (rl *relay) clearParked(key parkKey) {
	rl.parkMu.Lock()
	defer rl.parkMu.Unlock()
	for _, item := range rl.parked[key] {
		rl.parkedCount--
		rl.parkedBytes -= len(item.data)
		item.reservation.Release()
	}
	delete(rl.parked, key)
}
