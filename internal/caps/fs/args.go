package fs

import (
	"bytes"
	"encoding/json"
	"math"
)

const maxSafe = 1<<53 - 1

type args map[string]json.RawMessage

func (a args) has(k string) bool {
	_, ok := a[k]
	return ok
}

func (a args) isNull(k string) bool {
	return bytes.Equal(bytes.TrimSpace(a[k]), []byte("null"))
}

func (a args) str(k string) (string, bool) {
	var s string
	if !a.has(k) || a.isNull(k) || json.Unmarshal(a[k], &s) != nil {
		return "", false
	}
	return s, true
}

func (a args) path() (string, error) {
	if !a.has("path") || a.isNull("path") {
		return "", nil
	}
	s, ok := a.str("path")
	if !ok {
		return "", fail("EINVAL")
	}
	return s, nil
}

func (a args) required(k string) (string, error) {
	s, ok := a.str(k)
	if !ok {
		return "", fail("EINVAL")
	}
	return s, nil
}

func (a args) truthy(k string) bool {
	raw := bytes.TrimSpace(a[k])
	if len(raw) == 0 {
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

func (a args) number(k string) (float64, bool) {
	var f float64
	raw := bytes.TrimSpace(a[k])
	if len(raw) == 0 || raw[0] == '"' || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &f) != nil {
		return 0, false
	}
	return f, true
}

func safeInt(f float64) bool {
	return f == math.Trunc(f) && f >= 0 && f <= maxSafe
}

func (a args) offset(k string) (int64, error) {
	f, ok := a.number(k)
	if !ok || !safeInt(f) {
		return 0, fail("EINVAL")
	}
	return int64(f), nil
}

func (a args) mode(k string) (uint32, bool, error) {
	if !a.has(k) {
		return 0, false, nil
	}
	f, ok := a.number(k)
	if !ok || f != math.Trunc(f) || f < 0 || f > 0o7777 {
		return 0, true, fail("EINVAL")
	}
	return uint32(f), true, nil
}

func (a args) fh() (uint64, error) {
	f, ok := a.number("fh")
	if !ok || !safeInt(f) || f < 1 {
		return 0, fail("EBADF")
	}
	return uint64(f), nil
}

func (a args) wantsWrite() bool {
	return a.truthy("write") || a.truthy("create") || a.truthy("truncate") || a.truthy("exclusive")
}
