package facade

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	recControl = 1
	recData    = 2
	recEnd     = 3

	maxControl = 64 << 10
	maxData    = 64 << 10
	maxRecord  = 16 << 20
)

var errMalformed = errors.New("a malformed record")

type record struct {
	typ     byte
	payload []byte
}

func writeRecord(w io.Writer, typ byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

func readRecord(r io.Reader) (record, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return record{}, errMalformed
		}
		return record{}, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	typ := head[0]
	switch {
	case typ == recControl && n > maxControl:
		return record{}, fmt.Errorf("%w: a control record of %d bytes", errMalformed, n)
	case typ == recEnd && n != 0:
		return record{}, fmt.Errorf("%w: an end record with a payload", errMalformed)
	case typ != recControl && typ != recData && typ != recEnd:
		return record{}, fmt.Errorf("%w: record type %d", errMalformed, typ)
	case n > maxRecord:
		return record{}, fmt.Errorf("%w: a record of %d bytes", errMalformed, n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return record{}, errMalformed
	}
	return record{typ: typ, payload: payload}, nil
}
