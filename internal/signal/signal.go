package signal

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
)

const (
	TypeHello     = "hello"
	TypeOffer     = "offer"
	TypeAnswer    = "answer"
	TypeCandidate = "candidate"
	TypeBye       = "bye"

	Version = 1
)

type Candidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type Message struct {
	T          string
	V          int
	Nonce      string
	SDP        string
	Candidate  *Candidate
	Reason     string
	ICEServers []ICEServer
}

type wire struct {
	T         string          `json:"t"`
	V         int             `json:"v,omitempty"`
	Nonce     string          `json:"nonce,omitempty"`
	SDP       string          `json:"sdp,omitempty"`
	Candidate json.RawMessage `json:"candidate,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	w := wire{T: m.T, V: m.V, Nonce: m.Nonce, SDP: m.SDP, Reason: m.Reason}
	if m.T == TypeCandidate {
		w.Candidate = json.RawMessage("null")
		if m.Candidate != nil {
			b, err := json.Marshal(m.Candidate)
			if err != nil {
				return nil, err
			}
			w.Candidate = b
		}
	}
	return json.Marshal(w)
}

func (m *Message) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = Message{T: w.T, V: w.V, Nonce: w.Nonce, SDP: w.SDP, Reason: w.Reason}
	if len(w.Candidate) > 0 && string(w.Candidate) != "null" {
		var c Candidate
		if err := json.Unmarshal(w.Candidate, &c); err != nil {
			return err
		}
		m.Candidate = &c
	}
	return nil
}

func Parse(b []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, err
	}
	if m.T == "" {
		return Message{}, errors.New("signal: message without t")
	}
	return m, nil
}

type Conn interface {
	Recv(ctx context.Context) (Message, error)
	Send(ctx context.Context, m Message) error
	Close() error
}

var ErrClosed = errors.New("signal: closed")

func NewNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func Hello(nonce string) Message {
	return Message{T: TypeHello, V: Version, Nonce: nonce}
}
