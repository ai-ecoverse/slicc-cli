package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/coder/websocket"
)

const (
	guardCheckID      = 0x7fffff00
	guardCheckTimeout = 2 * time.Second
	methodNotFound    = -32601
)

var ErrUnguarded = errors.New("it has no browser session, so slicc can't watch its tabs or keep seven's origin out of them")

func checkGuard(ctx context.Context, ep browser.Endpoint) error {
	if ep.WSOnly {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, guardCheckTimeout)
	defer cancel()
	conn, _, err := dial(ctx, ep)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.CloseNow() }()
	req, _ := json.Marshal(map[string]any{"id": guardCheckID, "method": "Target.attachToBrowserTarget"})
	if conn.Write(ctx, websocket.MessageText, req) != nil {
		return nil
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil
		}
		var m struct {
			ID    int `json:"id"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &m) != nil || m.ID != guardCheckID {
			continue
		}
		if m.Error != nil && m.Error.Code == methodNotFound {
			return ErrUnguarded
		}
		return nil
	}
}
