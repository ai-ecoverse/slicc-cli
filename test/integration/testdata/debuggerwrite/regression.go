package cdp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/coder/websocket"
)

type debuggerWriteUpgrade struct {
	net.Conn
	headers http.Header
}

func (u *debuggerWriteUpgrade) Header() http.Header { return u.headers }
func (u *debuggerWriteUpgrade) WriteHeader(status int) {
	_, _ = fmt.Fprintf(u.Conn, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	_ = u.headers.Write(u.Conn)
	_, _ = io.WriteString(u.Conn, "\r\n")
}
func (u *debuggerWriteUpgrade) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return u.Conn, bufio.NewReadWriter(bufio.NewReader(u.Conn), bufio.NewWriter(u.Conn)), nil
}

type debuggerWriteTransport struct{ peer chan *websocket.Conn }

func (tr debuggerWriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	client, peer := net.Pipe()
	go func() {
		parsed, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			_ = peer.Close()
			return
		}
		socket, err := websocket.Accept(&debuggerWriteUpgrade{Conn: peer, headers: http.Header{}}, parsed, nil)
		if err != nil {
			_ = peer.Close()
			return
		}
		tr.peer <- socket
	}()
	if err := req.Write(client); err != nil {
		_ = client.Close()
		return nil, err
	}
	reply, err := http.ReadResponse(bufio.NewReader(client), req)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	reply.Body = client
	return reply, nil
}

func TestPrivateDebuggerWriteFailureResetsPageStream(t *testing.T) {
	transport := debuggerWriteTransport{peer: make(chan *websocket.Conn, 1)}
	socket, _, err := websocket.Dial(t.Context(), "ws://memory.test/relay", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	peer := <-transport.peer
	t.Cleanup(func() { _ = peer.CloseNow() })
	_ = socket.CloseNow()
	stream := link.DebuggerWriteMemoryStream()
	t.Cleanup(func() { _ = stream.Reset("ECANCELED") })
	rl := &relay{conn: socket, s: stream}
	stopped := make(chan struct{})
	go func() { rl.toBrowser(); close(stopped) }()
	if rl.writeDebugger([]byte(`{"id":1,"sessionId":"S","method":"Debugger.resume"}`)) {
		t.Fatal("a closed browser accepted a private resume")
	}
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("private write failure left the page stream open")
	}
	if !rl.browserDone.Load() {
		t.Fatal("browserDone was not set")
	}
	var reset *link.ResetError
	if !errors.As(stream.Err(), &reset) || reset.Code != "ECONNRESET" {
		t.Fatal("missing page stream reset", stream.Err())
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("relay page reader remained blocked")
	}
	var data [1]byte
	if n, err := stream.Read(data[:]); n != 0 || !errors.As(err, &reset) {
		t.Fatal("page read did not terminate", n, err)
	}
	if _, err := stream.Write([]byte("not forwarded")); !errors.As(err, &reset) {
		t.Fatal("page write did not terminate", err)
	}
	if err := stream.CloseWrite(); !errors.As(err, &reset) && err != io.EOF {
		t.Fatal("page stream was not reset", err)
	}
}
