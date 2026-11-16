package adapter

import (
	"encoding/json"
	"fmt"
)

// EventAck is the broker ledger position returned after an accepted operation.
type EventAck struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

// Receipt describes a completed commit for the broker's signed receipt route.
type Receipt struct {
	Commit  string   `json:"commit"`
	DiffSHA string   `json:"diff_sha"`
	Files   []string `json:"files"`
}

// Broker is the intentionally small set of coordination capabilities exposed
// to adapters. Implementations should preserve the broker's authentication
// and signing behavior rather than making unsigned requests.
type Broker interface {
	Claim(intent string, paths []string) (EventAck, error)
	Write(path string, content []byte) (EventAck, error)
	Say(body string) (EventAck, error)
	Release(paths []string) (EventAck, error)
	Submit(receipt Receipt) (EventAck, error)
}

// Doer is the subset of the existing signed client required by SignedBroker.
// *client.Client implements this interface via its Do method.
type Doer interface {
	Do(method, path string, body any) ([]byte, error)
}

// SignedBroker maps typed capabilities to the existing signed broker routes.
// It delegates each request to the supplied signed client; it does not perform
// HTTP itself or hold signing keys.
type SignedBroker struct {
	client Doer
}

// NewSignedBroker wraps an existing signed protocol client.
func NewSignedBroker(c Doer) *SignedBroker { return &SignedBroker{client: c} }

func (b *SignedBroker) post(path string, body any) (EventAck, error) {
	if b == nil || b.client == nil {
		return EventAck{}, fmt.Errorf("adapter broker: signed client is nil")
	}
	data, err := b.client.Do("POST", path, body)
	if err != nil {
		return EventAck{}, err
	}
	var ack EventAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return EventAck{}, fmt.Errorf("decode broker acknowledgement: %w", err)
	}
	return ack, nil
}

// Claim requests an exclusive lease for the named paths.
func (b *SignedBroker) Claim(intent string, paths []string) (EventAck, error) {
	return b.post("/v1/claim", struct {
		Intent string   `json:"intent"`
		Paths  []string `json:"paths"`
	}{Intent: intent, Paths: paths})
}

// Write asks the broker to atomically apply content to a registered agent worktree.
// The broker rejects the operation unless the agent holds a covering live lease.
func (b *SignedBroker) Write(path string, content []byte) (EventAck, error) {
	return b.post("/v1/write", struct {
		Path    string `json:"path"`
		Content []byte `json:"content"`
	}{Path: path, Content: content})
}

// Say appends a short message to the collaboration session.
func (b *SignedBroker) Say(body string) (EventAck, error) {
	return b.post("/v1/say", struct {
		Body string `json:"body"`
	}{Body: body})
}

// Release releases leases for the named paths.
func (b *SignedBroker) Release(paths []string) (EventAck, error) {
	return b.post("/v1/release", struct {
		Paths []string `json:"paths"`
	}{Paths: paths})
}

// Submit appends a signed commit receipt.
func (b *SignedBroker) Submit(receipt Receipt) (EventAck, error) {
	return b.post("/v1/receipt", receipt)
}

var _ Broker = (*SignedBroker)(nil)
