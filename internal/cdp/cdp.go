package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Event struct {
	SessionID string
	Method    string
	Params    json.RawMessage
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("cdp: %s (%d): %s", e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("cdp: %s (%d)", e.Message, e.Code)
}

var ErrClosed = errors.New("cdp: connection closed")

type Client struct {
	conn    *websocket.Conn
	writing chan struct{}

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan reply
	subs    map[*subscription]struct{}
	err     error
	done    chan struct{}
}

type reply struct {
	result json.RawMessage
	err    error
}

type inbound struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *Error          `json:"error"`
	SessionID string          `json:"sessionId"`
}

type outbound struct {
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func Discover(ctx context.Context, endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("cdp: bad endpoint %q: %w", endpoint, err)
	}
	switch u.Scheme {
	case "ws", "wss":
		return u.String(), nil
	case "http", "https":
	default:
		return "", fmt.Errorf("cdp: endpoint %q must be http(s) or ws(s)", endpoint)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/json/version"
	u.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := endpointClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cdp: discover %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cdp: discover %s: %s", u, resp.Status)
	}
	var info struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("cdp: discover %s: %w", u, err)
	}
	if info.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("cdp: discover %s: no webSocketDebuggerUrl", u)
	}
	return info.WebSocketDebuggerURL, nil
}

func Dial(ctx context.Context, endpoint string) (*Client, error) {
	wsURL, err := Discover(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: endpointClient})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("cdp: connect %s: %w", wsURL, err)
	}
	conn.SetReadLimit(-1)
	c := &Client{
		conn:    conn,
		writing: make(chan struct{}, 1),
		pending: map[int64]chan reply{},
		subs:    map[*subscription]struct{}{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) Close() error {
	c.fail(ErrClosed)
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	for id, ch := range c.pending {
		ch <- reply{err: err}
		delete(c.pending, id)
	}
	for s := range c.subs {
		s.end()
	}
	close(c.done)
}

func (c *Client) readLoop() {
	ctx := context.Background()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.fail(fmt.Errorf("cdp: read: %w", err))
			return
		}
		var msg inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Method != "" && msg.ID == 0 {
			c.dispatch(Event{SessionID: msg.SessionID, Method: msg.Method, Params: msg.Params})
			continue
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- reply{err: msg.Error}
		} else {
			ch <- reply{result: msg.Result}
		}
	}
}

func (c *Client) dispatch(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for s := range c.subs {
		s.push(ev)
	}
}

const writeTimeout = 10 * time.Second

func (c *Client) Call(ctx context.Context, sessionID, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	data, err := json.Marshal(outbound{ID: id, Method: method, Params: params, SessionID: sessionID})
	if err != nil {
		c.forget(id)
		return err
	}
	if err := c.send(ctx, data); err != nil {
		c.forget(id)
		return fmt.Errorf("cdp: %s: %w", method, err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result != nil && len(r.result) > 0 {
			return json.Unmarshal(r.result, result)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *Client) send(ctx context.Context, data []byte) error {
	select {
	case c.writing <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
	defer func() { <-c.writing }()
	if err := ctx.Err(); err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.conn.Write(wctx, websocket.MessageText, data)
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) Subscribe() *Subscription {
	s := &subscription{wake: make(chan struct{}, 1), quit: make(chan struct{}), out: make(chan Event)}
	c.mu.Lock()
	if c.err != nil {
		s.end()
	} else {
		c.subs[s] = struct{}{}
	}
	c.mu.Unlock()
	go s.pump()
	return &Subscription{c: c, s: s}
}

type Subscription struct {
	c *Client
	s *subscription
}

func (s *Subscription) Events() <-chan Event { return s.s.out }

func (s *Subscription) Close() {
	s.c.mu.Lock()
	delete(s.c.subs, s.s)
	s.c.mu.Unlock()
	s.s.once.Do(func() { close(s.s.quit) })
}

type subscription struct {
	mu    sync.Mutex
	queue []Event
	ended bool
	wake  chan struct{}
	quit  chan struct{}
	once  sync.Once
	out   chan Event
}

func (s *subscription) push(ev Event) {
	s.mu.Lock()
	if !s.ended {
		s.queue = append(s.queue, ev)
	}
	s.mu.Unlock()
	s.poke()
}

func (s *subscription) end() {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	s.poke()
}

func (s *subscription) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscription) pump() {
	defer close(s.out)
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			ended := s.ended
			s.mu.Unlock()
			if ended {
				return
			}
			select {
			case <-s.wake:
			case <-s.quit:
				return
			}
			continue
		}
		ev := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		select {
		case s.out <- ev:
		case <-s.quit:
			return
		}
	}
}

type Session struct {
	Client *Client
	ID     string
}

type TargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Attached bool   `json:"attached"`
}

func (c *Client) Targets(ctx context.Context) ([]TargetInfo, error) {
	var res struct {
		TargetInfos []TargetInfo `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &res); err != nil {
		return nil, err
	}
	return res.TargetInfos, nil
}

func (c *Client) Attach(ctx context.Context, targetID string) (*Session, error) {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	params := map[string]any{"targetId": targetID, "flatten": true}
	if err := c.Call(ctx, "", "Target.attachToTarget", params, &res); err != nil {
		return nil, err
	}
	return &Session{Client: c, ID: res.SessionID}, nil
}

func (s *Session) Call(ctx context.Context, method string, params, result any) error {
	return s.Client.Call(ctx, s.ID, method, params, result)
}

func (s *Session) Detach(ctx context.Context) error {
	return s.Client.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": s.ID}, nil)
}

type ExceptionError struct {
	Text string
}

func (e *ExceptionError) Error() string { return "cdp: exception: " + e.Text }

type EvaluateOptions struct {
	ContextID     int
	AwaitPromise  bool
	ReturnByValue bool
}

func (s *Session) Evaluate(ctx context.Context, expression string, opts EvaluateOptions, result any) error {
	params := map[string]any{"expression": expression, "returnByValue": opts.ReturnByValue, "awaitPromise": opts.AwaitPromise}
	if opts.ContextID != 0 {
		params["contextId"] = opts.ContextID
	}
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := s.Call(ctx, "Runtime.evaluate", params, &res); err != nil {
		return err
	}
	if d := res.ExceptionDetails; d != nil {
		text := d.Text
		if d.Exception != nil && d.Exception.Description != "" {
			text = d.Exception.Description
		}
		return &ExceptionError{Text: text}
	}
	if result != nil && len(res.Result.Value) > 0 {
		return json.Unmarshal(res.Result.Value, result)
	}
	return nil
}

func (s *Session) AddBinding(ctx context.Context, name string) error {
	return s.Call(ctx, "Runtime.addBinding", map[string]any{"name": name}, nil)
}

type BindingCalled struct {
	Name               string `json:"name"`
	Payload            string `json:"payload"`
	ExecutionContextID int    `json:"executionContextId"`
}

type ExecutionContext struct {
	ID       int    `json:"id"`
	Origin   string `json:"origin"`
	Name     string `json:"name"`
	UniqueID string `json:"uniqueId"`
	AuxData  struct {
		IsDefault bool   `json:"isDefault"`
		Type      string `json:"type"`
		FrameID   string `json:"frameId"`
	} `json:"auxData"`
}
