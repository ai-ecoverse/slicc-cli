package ws

import (
	"encoding/binary"
	"unicode/utf8"
)

const (
	RecordText   = 1
	RecordBinary = 2
	RecordClose  = 8

	RecordHeader   = 5
	MaxMessage     = 64 << 20
	MaxCloseReason = 123

	CloseNormal        = 1000
	CloseProtocolError = 1002
	CloseNoStatus      = 1005
	CloseMessageTooBig = 1009
)

func Record(t byte, payload []byte) []byte {
	out := make([]byte, RecordHeader+len(payload))
	out[0] = t
	binary.BigEndian.PutUint32(out[1:], uint32(len(payload)))
	copy(out[RecordHeader:], payload)
	return out
}

func ClosePayload(code int, reason string) []byte {
	if code == CloseNoStatus {
		return []byte{}
	}
	reason = truncate(reason, MaxCloseReason)
	out := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(out, uint16(code))
	copy(out[2:], reason)
	return out
}

func CloseRecord(code int, reason string) []byte {
	return Record(RecordClose, ClosePayload(code, reason))
}

func ParseClose(p []byte) (int, string, bool) {
	if len(p) == 0 {
		return CloseNoStatus, "", true
	}
	if len(p) < 2 || len(p) > 2+MaxCloseReason {
		return 0, "", false
	}
	code := int(binary.BigEndian.Uint16(p))
	reason := string(p[2:])
	if !SendableCode(code) || !utf8.ValidString(reason) {
		return 0, "", false
	}
	return code, reason, true
}

func SendableCode(code int) bool {
	return (code >= 1000 && code <= 1003) || (code >= 1007 && code <= 1014) || (code >= 3000 && code <= 4999)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
