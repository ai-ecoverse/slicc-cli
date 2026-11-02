package screen

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"image/jpeg"
	"io"
	"net"
	"os"
	"time"
)

func ReadRecord(c net.Conn, header any, maxBody int, timeout time.Duration) ([]byte, error) {
	return readRecord(c, header, maxBody, timeout, nil)
}

func readRecord(c net.Conn, header any, maxBody int, timeout time.Duration, start func(time.Time)) ([]byte, error) {
	if e := c.SetReadDeadline(time.Time{}); e != nil {
		return nil, e
	}
	var size [4]byte
	if _, e := io.ReadFull(c, size[:1]); e != nil {
		return nil, e
	}
	now := time.Now()
	if start != nil {
		start(now)
	}
	if e := c.SetReadDeadline(now.Add(timeout)); e != nil {
		return nil, e
	}
	if _, e := io.ReadFull(c, size[1:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > HeaderBytes {
		return nil, ErrSize
	}
	h := make([]byte, int(n))
	if _, e := io.ReadFull(c, h); e != nil {
		return nil, e
	}
	if e := Strict(h, header, HeaderBytes); e != nil {
		return nil, e
	}
	if _, e := io.ReadFull(c, size[:]); e != nil {
		return nil, e
	}
	n = binary.BigEndian.Uint32(size[:])
	if uint64(n) > uint64(maxBody) {
		return nil, ErrSize
	}
	body := make([]byte, int(n))
	if _, e := io.ReadFull(c, body); e != nil {
		return nil, e
	}
	return body, nil
}
func WriteRecord(c net.Conn, header any, body []byte, timeout time.Duration) error {
	h, e := json.Marshal(header)
	if e != nil {
		return ErrProtocol
	}
	if len(h) == 0 || len(h) > HeaderBytes || len(body) > FrameBytes {
		return ErrSize
	}
	if e = c.SetWriteDeadline(time.Now().Add(timeout)); e != nil {
		return e
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(h)))
	for _, p := range [][]byte{n[:], h} {
		if e = writeFull(c, p); e != nil {
			return e
		}
	}
	binary.BigEndian.PutUint32(n[:], uint32(len(body)))
	if e = writeFull(c, n[:]); e != nil {
		return e
	}
	return writeFull(c, body)
}
func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, e := w.Write(p)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
func CheckFrame(f Frame, body []byte, b Binding, last Counter, limits Limits) error {
	if f.Type != "frame" || f.V != 1 || f.Codec != "jpeg" || !f.Binding.Valid() {
		return ErrProtocol
	}
	if f.Binding != b {
		return ErrStale
	}
	n, e := f.Seq.Value()
	if e != nil {
		return e
	}
	old, e := last.Value()
	if e != nil {
		return e
	}
	if n <= old {
		return ErrStale
	}
	if _, e = f.Captured.Value(); e != nil {
		return e
	}
	if !limits.Valid() {
		return ErrProtocol
	}
	if f.Width <= 0 || f.Height <= 0 || f.Width > limits.LongEdge || f.Height > limits.LongEdge || int64(f.Width)*int64(f.Height) > int64(limits.Pixels) || len(body) > limits.FrameBytes {
		return ErrSize
	}
	if len(body) < 4 || body[0] != 255 || body[1] != 216 || body[len(body)-2] != 255 || body[len(body)-1] != 217 {
		return ErrProtocol
	}
	cfg, e := jpeg.DecodeConfig(bytes.NewReader(body))
	if e != nil || cfg.Width != f.Width || cfg.Height != f.Height {
		return ErrProtocol
	}
	return nil
}

type frameSink interface {
	ScreenBufferRoom(int) bool
	TryWrite([]byte) (bool, error)
	Done() <-chan struct{}
}
type frameConn struct {
	net.Conn
	s          frameSink
	b          *Broker
	generation uint64
	deadline   time.Time
}

func (c frameConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		if !c.b.enabled.Load() || c.b.generation.Load() != c.generation || (c.b.authorized != nil && !c.b.authorized()) {
			return total, ErrAccess
		}
		if c.expired() {
			return total, os.ErrDeadlineExceeded
		}
		n := min(len(p), link.MaxPayload)
		ok := false
		var e error
		if c.s.ScreenBufferRoom(n + link.HeaderSize) {
			ok, e = c.s.TryWrite(p[:n])
		}
		if e != nil {
			return total, e
		}
		if ok {
			p = p[n:]
			total += n
			continue
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-c.s.Done():
			timer.Stop()
			return total, ErrAccess
		}
		if c.expired() {
			return total, os.ErrDeadlineExceeded
		}
	}
	return total, nil
}
func (c frameConn) expired() bool { return time.Now().After(c.deadline) }
