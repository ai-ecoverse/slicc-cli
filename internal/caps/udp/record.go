package udp

import (
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"strconv"
)

const (
	RecordDatagram    = 1
	RecordUnreachable = 2

	MaxPayload4 = 65507
	MaxPayload6 = 65527
	MaxHost     = 64
)

type protoError string

func (e protoError) Error() string { return string(e) }

func Record(t byte, host string, port uint16, payload []byte) []byte {
	out := make([]byte, 0, 9+len(host)+len(payload))
	out = append(out, t)
	out = binary.BigEndian.AppendUint16(out, uint16(len(host)))
	out = append(out, host...)
	out = binary.BigEndian.AppendUint16(out, port)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	return append(out, payload...)
}

func MaxPayload(family int) int {
	if family == 6 {
		return MaxPayload6
	}
	return MaxPayload4
}

type datagram struct {
	to      netip.AddrPort
	payload []byte
}

func readRecord(r io.Reader, family int, buf []byte) (datagram, error) {
	var head [3]byte
	if _, err := io.ReadFull(r, head[:1]); err != nil {
		return datagram{}, err
	}
	if _, err := io.ReadFull(r, head[1:]); err != nil {
		return datagram{}, short(err)
	}
	if head[0] != RecordDatagram {
		return datagram{}, protoError("record type " + strconv.Itoa(int(head[0])) + " from the page")
	}
	hl := int(binary.BigEndian.Uint16(head[1:]))
	if hl == 0 || hl > MaxHost {
		return datagram{}, protoError("the destination is not an IP literal")
	}
	rest := make([]byte, hl+6)
	if _, err := io.ReadFull(r, rest); err != nil {
		return datagram{}, short(err)
	}
	addr, err := netip.ParseAddr(string(rest[:hl]))
	switch {
	case err != nil || addr.Zone() != "":
		return datagram{}, protoError("the destination is not an IP literal")
	case family == 4 && !addr.Is4(), family == 6 && (!addr.Is6() || addr.Is4In6()):
		return datagram{}, protoError("the destination is not an IPv" + strconv.Itoa(family) + " address")
	}
	port := binary.BigEndian.Uint16(rest[hl:])
	n := binary.BigEndian.Uint32(rest[hl+2:])
	switch {
	case port == 0:
		return datagram{}, protoError("the destination port is 0")
	case int64(n) > int64(MaxPayload(family)):
		return datagram{}, protoError("a datagram over " + strconv.Itoa(MaxPayload(family)) + " bytes")
	}
	payload := buf[:n]
	if _, err := io.ReadFull(r, payload); err != nil {
		return datagram{}, short(err)
	}
	return datagram{to: netip.AddrPortFrom(addr, port), payload: payload}, nil
}

func short(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return protoError("END inside a record")
	}
	return err
}
