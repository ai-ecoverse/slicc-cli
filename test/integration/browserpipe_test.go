package integration

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

type browserPipe struct {
	toChrome, fromChrome *os.File
	childIn, childOut    *os.File
	reader               *bufio.Reader
}

func newBrowserPipe(t *testing.T) *browserPipe {
	t.Helper()
	childIn, toChrome, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromChrome, childOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &browserPipe{toChrome: toChrome, fromChrome: fromChrome, childIn: childIn, childOut: childOut, reader: bufio.NewReader(fromChrome)}
	t.Cleanup(func() {
		_ = p.toChrome.Close()
		_ = p.fromChrome.Close()
	})
	return p
}

func (p *browserPipe) prep(t *testing.T) func(cmd *exec.Cmd) func() {
	return func(cmd *exec.Cmd) func() {
		passBrowserPipe(t, cmd, p)
		return func() {
			_ = p.childIn.Close()
			_ = p.childOut.Close()
		}
	}
}

func (p *browserPipe) call(t *testing.T, method string, params, result any) {
	t.Helper()
	msg, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
	if _, err := p.toChrome.Write(append(msg, 0)); err != nil {
		t.Fatalf("write %s to Chrome's debugging pipe: %v", method, err)
	}
	type answer struct {
		data []byte
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		data, err := p.reader.ReadBytes(0)
		got <- answer{data, err}
	}()
	select {
	case a := <-got:
		if a.err != nil {
			t.Fatalf("read %s from Chrome's debugging pipe: %v", method, a.err)
		}
		var reply struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(a.data[:len(a.data)-1], &reply); err != nil || reply.Error != nil {
			t.Fatalf("%s over Chrome's debugging pipe: %s (%v)", method, a.data, err)
		}
		if result != nil {
			_ = json.Unmarshal(reply.Result, result)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("Chrome never answered %s on its debugging pipe", method)
	}
}
