package tray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultRequestTimeout = 30 * time.Second
	DefaultRetryBudget    = 8 * time.Second
	DefaultRetryBackoff   = time.Second
	maxRetryBackoff       = 4 * time.Second
	requestAttempts       = 4
	maxResponseBytes      = 1 << 20
	maxSnippet            = 120
)

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type Candidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

type sessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type bootstrapFailure struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	RetryAfterMs *int   `json:"retryAfterMs"`
}

type bootstrapStatus struct {
	ControllerID     string            `json:"controllerId"`
	BootstrapID      string            `json:"bootstrapId"`
	Attempt          int               `json:"attempt"`
	State            string            `json:"state"`
	Cursor           int               `json:"cursor"`
	MaxRetries       int               `json:"maxRetries"`
	RetriesRemaining int               `json:"retriesRemaining"`
	RetryAfterMs     *int              `json:"retryAfterMs"`
	Failure          *bootstrapFailure `json:"failure"`
}

type bootstrapEvent struct {
	Sequence  int                 `json:"sequence"`
	Type      string              `json:"type"`
	Offer     *sessionDescription `json:"offer"`
	Candidate *Candidate          `json:"candidate"`
	Failure   *bootstrapFailure   `json:"failure"`
}

type attachResult struct {
	Action       string           `json:"action"`
	Code         string           `json:"code"`
	RetryAfterMs *int             `json:"retryAfterMs"`
	Error        string           `json:"error"`
	Bootstrap    *bootstrapStatus `json:"bootstrap"`
	JoinURL      string           `json:"joinUrl"`
}

type hubResponse struct {
	Role       string           `json:"role"`
	Trust      string           `json:"trust"`
	Code       string           `json:"code"`
	Error      string           `json:"error"`
	Result     *attachResult    `json:"result"`
	Bootstrap  *bootstrapStatus `json:"bootstrap"`
	Events     []bootstrapEvent `json:"events"`
	IceServers []ICEServer      `json:"iceServers"`
	successor  string
	superseded bool
}

type client struct {
	http    *http.Client
	timeout time.Duration
	budget  time.Duration
	backoff time.Duration
	red     *redactor
	logf    func(format string, args ...any)
}

func newHTTPClient(base *http.Client) *http.Client {
	c := http.Client{}
	if base != nil {
		c = *base
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

type transientError struct{ err *Error }

func (c *client) post(ctx context.Context, joinURL string, body map[string]any) (*hubResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(c.budget)
	backoff := c.backoff
	var last *Error
	for attempt := 1; attempt <= requestAttempts; attempt++ {
		resp, err := c.attempt(ctx, joinURL, payload, deadline)
		var te *transientError
		if !errors.As(err, &te) {
			return resp, err
		}
		last = te.err
		if attempt == requestAttempts || time.Until(deadline) <= backoff {
			break
		}
		c.logf("tray: %s, retrying in %s", last.Message, backoff)
		if err := sleepCtx(ctx, nil, backoff); err != nil {
			return nil, err
		}
		backoff = min(backoff*2, maxRetryBackoff)
	}
	return nil, last
}

func (c *client) attempt(ctx context.Context, joinURL string, payload []byte, deadline time.Time) (*hubResponse, error) {
	bound := time.Now().Add(c.timeout)
	timedOutBy := c.timeout
	if deadline.Before(bound) {
		bound = deadline
		timedOutBy = c.budget
	}
	reqCtx, cancel := context.WithDeadline(ctx, bound)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, joinURL, bytes.NewReader(payload))
	if err != nil {
		return nil, &Error{Code: CodeNetwork, Message: "cannot build the request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.networkError(ctx, reqCtx, err, timedOutBy)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, c.networkError(ctx, reqCtx, err, timedOutBy)
	}
	return c.decode(resp, data)
}

func (c *client) decode(resp *http.Response, data []byte) (*hubResponse, error) {
	successor := firstNonEmpty(
		SuccessorFromLinkHeader(resp.Header),
		redirectLocation(resp.StatusCode, resp.Header.Get("Location")),
	)
	var out hubResponse
	jsonErr := json.Unmarshal(data, &out)
	if successor != "" {
		c.red.add(successor)
		out.successor, out.superseded = successor, true
		return &out, nil
	}
	if jsonErr != nil {
		e := &Error{Code: CodeBadResponse, Message: fmt.Sprintf("hub answered HTTP %d without JSON%s", resp.StatusCode, c.snippet(data))}
		if resp.StatusCode >= 500 || len(bytes.TrimSpace(data)) > 0 {
			e.Retryable = true
			return nil, &transientError{err: e}
		}
		return nil, e
	}
	if out.Result != nil && out.Result.Code == CodeSuperseded {
		out.successor, out.superseded = strings.TrimSpace(out.Result.JoinURL), true
		c.red.add(out.successor)
		return &out, nil
	}
	if out.Role != "follower" {
		return nil, c.refusal(resp.StatusCode, &out)
	}
	return &out, nil
}

func (c *client) refusal(status int, out *hubResponse) error {
	code := out.Code
	if code == "" {
		code = fmt.Sprintf("HTTP_%d", status)
	}
	msg := c.red.redact(out.Error)
	if msg == "" {
		msg = fmt.Sprintf("hub answered HTTP %d", status)
	}
	e := &Error{Code: code, Message: msg, Retryable: status >= 500 || status == http.StatusTooManyRequests || code == CodeBootstrapNotFound}
	if status >= 500 {
		return &transientError{err: e}
	}
	return e
}

func (c *client) snippet(data []byte) string {
	s := strings.Join(strings.Fields(string(data)), " ")
	if s == "" {
		return ""
	}
	if len(s) > maxSnippet {
		s = s[:maxSnippet] + "…"
	}
	return ": " + c.red.redact(s)
}

func (c *client) networkError(ctx, reqCtx context.Context, err error, limit time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return &transientError{err: &Error{Code: CodeRequestTimeout, Message: fmt.Sprintf("hub did not answer within %s", limit), Retryable: true}}
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return &transientError{err: &Error{Code: CodeNetwork, Message: c.red.redact(err.Error()), Retryable: true}}
}

func (e *transientError) Error() string { return e.err.Error() }

func redirectLocation(status int, location string) string {
	if status < 300 || status >= 400 || location == "" {
		return ""
	}
	u, err := url.Parse(location)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return ""
	}
	q := u.Query()
	q.Del("json")
	u.RawQuery = q.Encode()
	return u.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
