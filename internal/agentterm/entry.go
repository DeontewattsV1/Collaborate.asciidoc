// Package agentterm implements a shared, append-only collaboration terminal
// for AI agents. Every entry is hash-chained to its predecessor and signed by
// the server; entries an agent asks for are also signed by that agent's
// per-session Ed25519 key, so an exported log can be verified offline without
// trusting the server that produced it beyond its published public key.
//
// The terminal is a coordination and evidence surface. It never pushes,
// merges or approves anything; work still lands only through pull requests
// that pass the repository's required checks.
package agentterm

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Entry is one immutable line of the shared terminal.
//
// Body is a compact JSON document carried as a string so that every verifier
// (Go, the browser view, a third-party script) hashes exactly the same bytes.
type Entry struct {
	Seq       int64  `json:"seq"`
	Time      string `json:"time"`
	Prev      string `json:"prev"`
	Kind      string `json:"kind"`
	Session   string `json:"session"`
	Agent     string `json:"agent"`
	Nonce     int64  `json:"nonce"`
	Body      string `json:"body"`
	AgentSig  string `json:"agent_sig"`
	Hash      string `json:"hash"`
	ServerSig string `json:"server_sig"`
}

// Kinds an agent may request. Each is signed with the agent's session key.
const (
	KindClockIn       = "clock_in"
	KindHeartbeat     = "heartbeat"
	KindClockOut      = "clock_out"
	KindClaim         = "claim"
	KindRelease       = "release"
	KindExecRequest   = "exec_request"
	KindRecord        = "record"
	KindCommit        = "commit"
	KindFinding       = "finding"
	KindFindingStatus = "finding_status"
	KindNote          = "note"
)

// Kinds only the server writes. They carry no agent signature.
const (
	KindGenesis    = "genesis"
	KindExecResult = "exec_result"
	KindExpire     = "expire"
	// KindChat is a Dev Guild message. The guild bridge writes it; agents
	// cannot request it. It stays in the same hash chain agentterm verify checks.
	KindChat = "chat"
)

var agentKinds = map[string]bool{
	KindClockIn: true, KindHeartbeat: true, KindClockOut: true, KindClaim: true,
	KindRelease: true, KindExecRequest: true, KindRecord: true, KindCommit: true,
	KindFinding: true, KindFindingStatus: true, KindNote: true,
}

var serverKinds = map[string]bool{KindGenesis: true, KindExecResult: true, KindExpire: true, KindChat: true}

// ChatBody is one Dev Guild transcript line stored in the signed log.
// JSON field names are part of the verified bytes. Add a field only together
// with the validator in State.apply.
type ChatBody struct {
	Thread     string   `json:"thread"`
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	From       string   `json:"from,omitempty"`
	Text       string   `json:"text,omitempty"`
	Mentions   []string `json:"mentions,omitempty"`
	Title      string   `json:"title,omitempty"`
	Members    []string `json:"members,omitempty"`
	Status     string   `json:"status,omitempty"`
	Tokens     int      `json:"tokens,omitempty"`
	Reasoning  string   `json:"reasoning,omitempty"`
	Tool       string   `json:"tool,omitempty"`
	Task       string   `json:"task,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	CardStatus string   `json:"card_status,omitempty"`
	Files      []string `json:"files,omitempty"`
}

// IsAgentKind reports whether kind is requested (and signed) by an agent.
func IsAgentKind(kind string) bool { return agentKinds[kind] }

// HashInput is the exact byte string hashed for an entry. It is deliberately
// a simple line format rather than JSON so independent verifiers agree.
func HashInput(e Entry) []byte {
	var b bytes.Buffer
	b.WriteString("agentterm/v1\n")
	b.WriteString(strconv.FormatInt(e.Seq, 10))
	for _, field := range []string{e.Time, e.Prev, e.Kind, e.Session, e.Agent, strconv.FormatInt(e.Nonce, 10), e.AgentSig} {
		b.WriteByte('\n')
		b.WriteString(field)
	}
	b.WriteByte('\n')
	b.WriteString(e.Body)
	return b.Bytes()
}

// ComputeHash returns the hex SHA-256 of HashInput(e).
func ComputeHash(e Entry) string {
	sum := sha256.Sum256(HashInput(e))
	return hex.EncodeToString(sum[:])
}

// RequestMessage is what an agent signs for every non-clock-in request. The
// nonce must strictly increase within a session, so a captured request cannot
// be replayed.
func RequestMessage(session, kind string, nonce int64, body string) []byte {
	return []byte("agentterm-req/v1\n" + session + "\n" + kind + "\n" + strconv.FormatInt(nonce, 10) + "\n" + body)
}

// ClockInMessage is what an agent signs, with the fresh session key it is
// registering, to prove possession of that key at clock-in.
func ClockInMessage(instance, branch, pubkey string, nonce int64) []byte {
	return []byte("agentterm-clockin/v1\n" + instance + "\n" + branch + "\n" + pubkey + "\n" + strconv.FormatInt(nonce, 10))
}

// ClockInBody is the body of a clock_in entry. The GitHub fields are filled
// by the server from the GitHub API, never from the client.
type ClockInBody struct {
	Instance    string `json:"instance"`
	Branch      string `json:"branch"`
	PubKey      string `json:"pubkey"`
	GitHubLogin string `json:"github_login"`
	GitHubID    int64  `json:"github_id"`
	AccountType string `json:"account_type"`
}

// CompactJSON validates that body is a single JSON object already in compact
// form. Requiring compact input means the bytes an agent signed are exactly
// the bytes stored and hashed.
func CompactJSON(body string) error {
	if !json.Valid([]byte(body)) {
		return errors.New("body is not valid JSON")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(body)); err != nil {
		return err
	}
	if buf.String() != body {
		return errors.New("body must be compact JSON (no insignificant whitespace)")
	}
	if len(body) == 0 || body[0] != '{' {
		return errors.New("body must be a JSON object")
	}
	return nil
}

// MustBody marshals v to compact JSON. json.Marshal output is always compact.
func MustBody(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// VerifyReport summarizes an offline verification.
type VerifyReport struct {
	Entries     int    `json:"entries"`
	Head        string `json:"head"`
	AgentSigned int    `json:"agent_signed"`
}

// VerifyChain checks every link, hash, server signature and agent signature
// of a complete log, starting from its genesis entry. It needs only the
// server's public key; agent keys are taken from each session's clock_in.
func VerifyChain(entries []Entry, serverKey ed25519.PublicKey) (VerifyReport, error) {
	var rep VerifyReport
	if len(serverKey) != ed25519.PublicKeySize {
		return rep, errors.New("server public key is required")
	}
	sessionKeys := map[string]ed25519.PublicKey{}
	sessionNonce := map[string]int64{}
	prev := ""
	var prevTime time.Time
	for i, e := range entries {
		where := fmt.Sprintf("entry %d", e.Seq)
		if e.Seq != int64(i) {
			return rep, fmt.Errorf("%s: sequence gap (want %d)", where, i)
		}
		if e.Prev != prev {
			return rep, fmt.Errorf("%s: prev link does not match previous hash", where)
		}
		if ComputeHash(e) != e.Hash {
			return rep, fmt.Errorf("%s: hash mismatch (entry altered)", where)
		}
		sig, err := hex.DecodeString(e.ServerSig)
		if err != nil || !ed25519.Verify(serverKey, []byte(e.Hash), sig) {
			return rep, fmt.Errorf("%s: invalid server signature", where)
		}
		t, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil {
			return rep, fmt.Errorf("%s: bad time", where)
		}
		if t.Before(prevTime) {
			return rep, fmt.Errorf("%s: time goes backwards", where)
		}
		prevTime = t
		if i == 0 && e.Kind != KindGenesis {
			return rep, errors.New("log must start with a genesis entry")
		}
		switch {
		case serverKinds[e.Kind]:
			if e.AgentSig != "" {
				return rep, fmt.Errorf("%s: server entry carries an agent signature", where)
			}
		case agentKinds[e.Kind]:
			if err := verifyAgentSig(e, sessionKeys, sessionNonce); err != nil {
				return rep, fmt.Errorf("%s: %w", where, err)
			}
			rep.AgentSigned++
		default:
			return rep, fmt.Errorf("%s: unknown kind %q", where, e.Kind)
		}
		prev = e.Hash
	}
	rep.Entries = len(entries)
	rep.Head = prev
	return rep, nil
}

func verifyAgentSig(e Entry, keys map[string]ed25519.PublicKey, nonces map[string]int64) error {
	sig, err := hex.DecodeString(e.AgentSig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("missing or malformed agent signature")
	}
	if e.Kind == KindClockIn {
		var b ClockInBody
		if err := json.Unmarshal([]byte(e.Body), &b); err != nil {
			return errors.New("bad clock_in body")
		}
		pub, err := hex.DecodeString(b.PubKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("bad session public key")
		}
		if !ed25519.Verify(pub, ClockInMessage(b.Instance, b.Branch, b.PubKey, e.Nonce), sig) {
			return errors.New("clock_in not signed by the registered session key")
		}
		if _, dup := keys[e.Session]; dup {
			return errors.New("session clocked in twice")
		}
		keys[e.Session] = pub
		nonces[e.Session] = e.Nonce
		return nil
	}
	pub, ok := keys[e.Session]
	if !ok {
		return errors.New("entry for a session that never clocked in")
	}
	if e.Nonce <= nonces[e.Session] {
		return errors.New("nonce did not increase (replayed request)")
	}
	if !ed25519.Verify(pub, RequestMessage(e.Session, e.Kind, e.Nonce, e.Body), sig) {
		return errors.New("not signed by the session's key")
	}
	nonces[e.Session] = e.Nonce
	return nil
}
