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
		_, data, err := rl.conn.Read(ctx)
		if err != nil {
			return message{}, fmt.Errorf("%s: %w", method, err)
		}
		v := rl.g.fromBrowser(data)
		for _, out := range v.send {
			_ = rl.conn.Write(ctx, websocket.MessageText, out)
		}
	}
}

func (rl *relay) checkPage(data []byte) ([]byte, bool) {
	v := rl.g.fromPage(data)
	if v.history != nil {
		v = rl.resolveHistory(v.history)
	}
	if v.reply != nil {
		if err := rl.writePage(v.reply); err != nil {
			_ = rl.conn.CloseNow()
			return nil, false
		}
	}
	return v.forward, true
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
