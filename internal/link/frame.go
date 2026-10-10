package link

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type FrameType byte

const (
	FrameOpen   FrameType = 1
	FrameOpened FrameType = 2
	FrameData   FrameType = 3
	FrameEnd    FrameType = 4
	FrameReset  FrameType = 5
	FrameCredit FrameType = 6
)

const (
	HeaderSize     = 5
	MaxPayload     = 65536
	MaxMessageSize = HeaderSize + MaxPayload
	Window         = 256 * 1024
)

func (t FrameType) String() string {
	switch t {
	case FrameOpen:
		return "OPEN"
	case FrameOpened:
		return "OPENED"
	case FrameData:
		return "DATA"
	case FrameEnd:
		return "END"
	case FrameReset:
		return "RESET"
	case FrameCredit:
		return "CREDIT"
	}
	return fmt.Sprintf("type %d", byte(t))
}

type Frame struct {
	Type    FrameType
	ID      uint32
	Payload []byte
}

var errMalformed = errors.New("malformed frame")

func EncodeFrame(t FrameType, id uint32, payload []byte) []byte {
	b := make([]byte, HeaderSize+len(payload))
	b[0] = byte(t)
	binary.BigEndian.PutUint32(b[1:5], id)
	copy(b[HeaderSize:], payload)
	return b
}

func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < HeaderSize {
		return Frame{}, fmt.Errorf("%w: %d bytes", errMalformed, len(b))
	}
	if len(b) > MaxMessageSize {
		return Frame{}, fmt.Errorf("%w: %d bytes is over the message size", errMalformed, len(b))
	}
	f := Frame{Type: FrameType(b[0]), ID: binary.BigEndian.Uint32(b[1:5]), Payload: b[HeaderSize:]}
	if f.Type < FrameOpen || f.Type > FrameCredit {
		return Frame{}, fmt.Errorf("%w: unknown type %d", errMalformed, b[0])
	}
	if f.ID == 0 {
		return Frame{}, fmt.Errorf("%w: stream id 0", errMalformed)
	}
	switch f.Type {
	case FrameEnd:
		if len(f.Payload) != 0 {
			return Frame{}, fmt.Errorf("%w: END with a payload", errMalformed)
		}
	case FrameCredit:
		if len(f.Payload) != 4 {
			return Frame{}, fmt.Errorf("%w: CREDIT of %d bytes", errMalformed, len(f.Payload))
		}
	}
	return f, nil
}

func creditPayload(n int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n))
	return b
}
