package agentctl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type SnapshotStream[T any] struct {
	s     Pipe
	r     *bufio.Reader
	event string
	parse func([]byte) (T, error)
	errs  func(error) error
	First T
}

func openSnapshots[T any](ctx context.Context, l Link, open any, event string, parse func([]byte) (T, error), errs func(error) error) (*SnapshotStream[T], error) {
	if !l.Allowed() {
		return nil, ErrNotAllowed
	}
	s, err := l.Open(ctx, open)
	if err != nil {
		return nil, errs(err)
	}
	first, err := parse(s.Opened())
	if err != nil {
		_ = s.Reset("EPROTO")
		return nil, err
	}
	_ = s.CloseWrite()
	return &SnapshotStream[T]{s: s, r: bufio.NewReaderSize(s, 64*1024), event: event, parse: parse, errs: errs, First: first}, nil
}

func (st *SnapshotStream[T]) Next() (T, error) {
	ev := &Stream{s: st.s, r: st.r}
	for {
		line, err := ev.readLine()
		if err != nil {
			var zero T
			return zero, st.errs(err)
		}
		var head struct {
			T string `json:"t"`
		}
		if json.Unmarshal(line, &head) != nil || head.T != st.event {
			continue
		}
		if s, err := st.parse(line); err == nil {
			return s, nil
		}
	}
}

func (st *SnapshotStream[T]) Close() error { return st.s.Reset("ECANCELED") }

func rawObject(b []byte, what string) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("seven sent %s that isn't a JSON object", what)
	}
	delete(raw, "t")
	return raw, nil
}

func snapshotErr(err error, missing error, hint string) error {
	code, msg, remote := remoteCode(err)
	if m, ok := linkMessage(err); ok && msg == "" {
		msg = m
	}
	switch {
	case !remote:
		return err
	case code == "EINVAL":
		return missing
	case code == "EOPNOTSUPP" && (msg == "" || strings.Contains(msg, hint)):
		return missing
	case code == "EOPNOTSUPP":
		return &UnavailableError{Message: msg}
	}
	return mapErr(err)
}

func SnapshotLine(event string, raw map[string]json.RawMessage, schema int, received time.Time) []byte {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		switch k {
		case "schema", "event", "receivedAt":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, `{"schema":%d,"event":%q`, schema, event)
	for _, k := range keys {
		var value bytes.Buffer
		if json.Compact(&value, raw[k]) != nil {
			continue
		}
		name, _ := json.Marshal(k)
		fmt.Fprintf(&b, ",%s:%s", name, value.Bytes())
	}
	stamp, _ := json.Marshal(received.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, `,"receivedAt":%s}`, stamp)
	return []byte(b.String())
}

func ReconnectingLine(schema, attempt int, at time.Time, wait time.Duration) []byte {
	b, _ := json.Marshal(struct {
		Schema  int       `json:"schema"`
		Event   string    `json:"event"`
		Attempt int       `json:"attempt"`
		RetryAt time.Time `json:"retryAt"`
		Wait    int64     `json:"wait"`
	}{schema, "reconnecting", attempt, at, wait.Milliseconds()})
	return b
}
