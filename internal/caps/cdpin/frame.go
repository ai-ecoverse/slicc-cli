package cdpin

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

const MaxMessage = 64 << 20

var (
	errMidMessage = errors.New("EPROTO: END inside a message")
	errTooBig     = errors.New("EMSGSIZE: a message over 64 MiB")
	ErrNoLink     = errors.New("no link to seven")
)

type Refused struct {
	Code    string
	Message string
}

func (e *Refused) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type CDPError struct{ Message string }

func (e *CDPError) Error() string { return e.Message }

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errMidMessage
		}
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxMessage {
		return nil, errTooBig
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errMidMessage
		}
		return nil, err
	}
	return buf, nil
}

func writeFrame(w io.Writer, data []byte) error {
	msg := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(msg, uint32(len(data)))
	copy(msg[4:], data)
	_, err := w.Write(msg)
	return err
}

func refusal(err error, l *link.Link) error {
	var re *link.ResetError
	switch {
	case l != nil && closed(l.Done()):
		return ErrNoLink
	case errors.As(err, &re) && re.Remote:
		return &Refused{Code: re.Code, Message: re.Message}
	case errors.Is(err, link.ErrClosed):
		return ErrNoLink
	}
	return err
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func open(ctx context.Context, l *link.Link) (*link.Stream, error) {
	if l == nil || closed(l.Done()) {
		return nil, ErrNoLink
	}
	st, err := l.Open(ctx, map[string]string{"kind": Kind}, link.OpenOptions{})
	if err != nil {
		return nil, refusal(err, l)
	}
	return st, nil
}

func finish(st *link.Stream) {
	_ = st.CloseWrite()
	go func() {
		t := time.AfterFunc(5*time.Second, func() { _ = st.Reset("ECONNRESET: closed") })
		defer t.Stop()
		_, _ = io.Copy(io.Discard, st)
	}()
}

type answer struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func call(st *link.Stream, id int64, method string, params any, within time.Duration) (json.RawMessage, error) {
	return callOn(st, id, "", method, params, within)
}

func callOn(st *link.Stream, id int64, session, method string, params any, within time.Duration) (json.RawMessage, error) {
	req := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		req["sessionId"] = session
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	_ = st.SetReadDeadline(time.Now().Add(within))
	defer func() { _ = st.SetReadDeadline(time.Time{}) }()
	if err := writeFrame(st, body); err != nil {
		return nil, refusal(err, st.Link())
	}
	for {
		msg, err := readFrame(st)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, &CDPError{Message: "seven closed the connection"}
			}
			return nil, refusal(err, st.Link())
		}
		var a answer
		if json.Unmarshal(msg, &a) != nil || a.ID == nil || *a.ID != id {
			continue
		}
		if a.Error != nil {
			return nil, &CDPError{Message: a.Error.Message}
		}
		return a.Result, nil
	}
}

type Version struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Product         string          `json:"product"`
	Revision        string          `json:"revision"`
	UserAgent       string          `json:"userAgent"`
	JSVersion       string          `json:"jsVersion"`
	Raw             json.RawMessage `json:"-"`
}

func Probe(ctx context.Context, l *link.Link) (Version, error) {
	st, err := open(ctx, l)
	if err != nil {
		return Version{}, err
	}
	defer finish(st)
	return openedVersion(st)
}

func openedVersion(st *link.Stream) (Version, error) {
	var opened struct {
		Version json.RawMessage `json:"version"`
	}
	var v Version
	if json.Unmarshal(st.OpenedPayload(), &opened) != nil || len(opened.Version) == 0 || json.Unmarshal(opened.Version, &v) != nil {
		return v, fmt.Errorf("seven's answer has no browser version")
	}
	v.Raw = opened.Version
	return v, nil
}
