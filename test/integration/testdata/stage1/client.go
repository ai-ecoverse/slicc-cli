package stage1

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type Client struct {
	Stream  *link.Stream
	next    atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan map[string]any
	Events  chan map[string]any
	Done    chan struct{}
}

func NewClient(t *testing.T, stream *link.Stream) *Client {
	t.Helper()
	client := &Client{Stream: stream, pending: map[int64]chan map[string]any{}, Events: make(chan map[string]any, 1024), Done: make(chan struct{})}
	go func() {
		defer close(client.Done)
		for {
			var head [4]byte
			if _, err := io.ReadFull(stream, head[:]); err != nil {
				return
			}
			length := binary.BigEndian.Uint32(head[:])
			if length > 1<<20 {
				return
			}
			data := make([]byte, length)
			if _, err := io.ReadFull(stream, data); err != nil {
				return
			}
			var packet map[string]any
			if json.Unmarshal(data, &packet) != nil {
				return
			}
			if id, ok := packet["id"].(float64); ok {
				client.mu.Lock()
				reply := client.pending[int64(id)]
				client.mu.Unlock()
				if reply != nil {
					reply <- packet
				}
			} else {
				select {
				case client.Events <- packet:
				case <-stream.Done():
					return
				}
			}
		}
	}()
	return client
}

func (c *Client) Call(t *testing.T, method, session string, params map[string]any) map[string]any {
	t.Helper()
	id := c.next.Add(1)
	reply := make(chan map[string]any, 1)
	c.mu.Lock()
	c.pending[id] = reply
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	packet := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		packet["sessionId"] = session
	}
	data, _ := json.Marshal(packet)
	framed := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(framed, uint32(len(data)))
	copy(framed[4:], data)
	_ = c.Stream.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Stream.Write(framed); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	select {
	case packet := <-reply:
		return packet
	case <-ctx.Done():
		t.Fatalf("%s: %v", method, ctx.Err())
	case <-c.Done:
		t.Fatalf("%s: stream ended: %v", method, c.Stream.Err())
	}
	return nil
}

func Result(t *testing.T, packet map[string]any) map[string]any {
	t.Helper()
	result, ok := packet["result"].(map[string]any)
	if !ok {
		t.Fatalf("CDP call failed: %v", packet["error"])
	}
	return result
}

func Refused(t *testing.T, packet map[string]any) {
	t.Helper()
	error, ok := packet["error"].(map[string]any)
	if !ok {
		t.Fatalf("CDP call was admitted: %v", packet)
	}
	message, _ := error["message"].(string)
	if len(message) < len("slicc: refused:") || message[:len("slicc: refused:")] != "slicc: refused:" {
		t.Fatalf("not a guard refusal: %v", error)
	}
}

func (c *Client) Paused(t *testing.T) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		select {
		case event := <-c.Events:
			if event["method"] == "Debugger.paused" {
				return event
			}
		case <-ctx.Done():
			t.Fatal("no published normal pause")
		case <-c.Done:
			t.Fatal("stream ended before pause")
		}
	}
}
