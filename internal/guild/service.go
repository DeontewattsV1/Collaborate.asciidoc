package guild

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"collaborate/internal/adapter"
	"collaborate/internal/agentterm"
)

const (
	ownerID     = "owner"
	ownerName   = "Deonte"
	guildThread = "guild"
)

var mentionRE = regexp.MustCompile(`@([a-z0-9-]{2,32})`)

// Service is the chat bridge. Every human and character line is appended to
// the agentterm log through adapter.Broker.Say or the same append the broker uses.
type Service struct {
	Term     *agentterm.Terminal
	Reg      *Registry
	Demo     bool
	Getenv   func(string) string
	Complete Completer
	Guard    Guard
	mu       sync.Mutex
}

// Open loads or creates the signed guild log beside the agent terminal keys.
func Open(dir string, demo bool) (*Service, error) {
	if dir == "" {
		dir = ".agentterm"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := agentterm.LoadOrCreateKey(filepath.Join(dir, "guild.key"))
	if err != nil {
		return nil, err
	}
	term, err := agentterm.Open(filepath.Join(dir, "guild.jsonl"), key, agentterm.NewState(nil, time.Hour, agentterm.ExecPolicy{}), nil)
	if err != nil {
		return nil, err
	}
	s := &Service{Term: term, Reg: Load(), Demo: demo, Getenv: os.Getenv}
	if err := s.ensureGuild(); err != nil {
		_ = term.Close()
		return nil, err
	}
	return s, nil
}

func (s *Service) getenv(key string) string {
	if s.Getenv == nil {
		return os.Getenv(key)
	}
	return s.Getenv(key)
}

// PublicKey is the hex key agentterm verify needs for this log.
func (s *Service) PublicKey() string {
	return hex.EncodeToString(s.Term.PublicKey())
}

func (s *Service) ensureGuild() error {
	if _, ok := s.thread(guildThread); ok {
		return nil
	}
	members := []string{ownerID}
	for _, c := range s.Reg.Agents() {
		members = append(members, c.ID)
	}
	_, err := s.append(agentterm.ChatBody{
		Thread: guildThread, ID: "thread-guild", Kind: "thread", Title: "The Dev Guild", Members: members,
	})
	return err
}

func (s *Service) append(body agentterm.ChatBody) (agentterm.Entry, error) {
	return s.Term.Append(agentterm.Entry{Kind: agentterm.KindChat, Agent: "guild", Body: agentterm.MustBody(body)})
}

func (s *Service) entries() []agentterm.Entry {
	entries, _ := s.Term.Since(-1, 0)
	return entries
}

func decode(e agentterm.Entry) (agentterm.ChatBody, bool) {
	if e.Kind != agentterm.KindChat {
		return agentterm.ChatBody{}, false
	}
	var b agentterm.ChatBody
	if err := json.Unmarshal([]byte(e.Body), &b); err != nil {
		return agentterm.ChatBody{}, false
	}
	return b, true
}

// Thread is a group or a 1:1.
type Thread struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Members []string `json:"members"`
	Group   bool     `json:"group"`
	Last    string   `json:"last,omitempty"`
	LastAt  string   `json:"last_at,omitempty"`
}

func (s *Service) thread(id string) (Thread, bool) {
	var found Thread
	ok := false
	for _, e := range s.entries() {
		b, isChat := decode(e)
		if !isChat || b.Thread != id || b.Kind != "thread" {
			continue
		}
		found = Thread{ID: b.Thread, Title: b.Title, Members: append([]string(nil), b.Members...), Group: len(b.Members) > 2}
		ok = true
	}
	if !ok {
		return Thread{}, false
	}
	for _, e := range s.entries() {
		b, isChat := decode(e)
		if !isChat || b.Thread != id || (b.Kind != "message" && b.Kind != "system") {
			continue
		}
		found.Last = b.Text
		found.LastAt = e.Time
	}
	return found, true
}

// Threads lists every conversation in the log.
func (s *Service) Threads() []Thread {
	seen := map[string]bool{}
	var out []Thread
	for _, e := range s.entries() {
		b, ok := decode(e)
		if !ok || b.Kind != "thread" || seen[b.Thread] {
			continue
		}
		seen[b.Thread] = true
		if t, ok := s.thread(b.Thread); ok {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == guildThread {
			return true
		}
		if out[j].ID == guildThread {
			return false
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// OpenDM ensures a 1:1 thread with one character.
func (s *Service) OpenDM(id string) (Thread, error) {
	c, ok := s.Reg.Get(id)
	if !ok {
		return Thread{}, fmt.Errorf("unknown character %q", id)
	}
	threadID := "dm-" + id
	if t, ok := s.thread(threadID); ok {
		return t, nil
	}
	if _, err := s.append(agentterm.ChatBody{
		Thread: threadID, ID: "thread-" + threadID, Kind: "thread",
		Title: c.Name, Members: []string{ownerID, id},
	}); err != nil {
		return Thread{}, err
	}
	t, _ := s.thread(threadID)
	return t, nil
}

// Message is one bubble.
type Message struct {
	Seq       int64          `json:"seq"`
	ID        string         `json:"id"`
	Thread    string         `json:"thread"`
	From      string         `json:"from"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Avatar    string         `json:"avatar,omitempty"`
	Color     string         `json:"color,omitempty"`
	Text      string         `json:"text"`
	Mentions  []string       `json:"mentions,omitempty"`
	Time      string         `json:"time"`
	Status    string         `json:"status,omitempty"`
	Mine      bool           `json:"mine"`
	Character *CharacterView `json:"character,omitempty"`
}

// CharacterView is the public card. It has personality and the enable hint,
// never a key.
type CharacterView struct {
	Character
	Online bool   `json:"online"`
	Note   string `json:"note"`
}

// Characters returns the cast with presence filled in.
func (s *Service) Characters() []CharacterView {
	out := make([]CharacterView, 0, len(s.Reg.All()))
	for _, c := range s.Reg.All() {
		p := c.Presence(s.getenv, s.Demo)
		out = append(out, CharacterView{Character: c, Online: p.Online, Note: p.Note})
	}
	return out
}

func (s *Service) view(id string) CharacterView {
	if id == ownerID {
		return CharacterView{Character: Character{ID: ownerID, Name: ownerName, Kind: "owner", Color: "#0A84FF"}, Online: true, Note: "You"}
	}
	c, ok := s.Reg.Get(id)
	if !ok {
		return CharacterView{Character: Character{ID: id, Name: id, Kind: "system"}}
	}
	p := c.Presence(s.getenv, s.Demo)
	return CharacterView{Character: c, Online: p.Online, Note: p.Note}
}

// Messages returns the thread, optionally after a log sequence.
func (s *Service) Messages(thread string, after int64) ([]Message, error) {
	if _, ok := s.thread(thread); !ok {
		return nil, fmt.Errorf("unknown thread %q", thread)
	}
	var out []Message
	var lastOwner int = -1
	for _, e := range s.entries() {
		b, ok := decode(e)
		if !ok || b.Thread != thread || (b.Kind != "message" && b.Kind != "system") {
			continue
		}
		if e.Seq <= after {
			continue
		}
		msg := s.bubble(e, b)
		out = append(out, msg)
		if msg.Mine {
			lastOwner = len(out) - 1
		}
	}
	// Delivered until a later character speaks; then the owner's latest line is read.
	if lastOwner >= 0 {
		read := false
		for i := lastOwner + 1; i < len(out); i++ {
			if !out[i].Mine && out[i].Kind != "system" {
				read = true
				break
			}
		}
		if read {
			out[lastOwner].Status = "Read"
		} else if out[lastOwner].Status == "" {
			out[lastOwner].Status = "Delivered"
		}
	}
	return out, nil
}

func (s *Service) bubble(e agentterm.Entry, b agentterm.ChatBody) Message {
	v := s.view(b.From)
	kind := v.Kind
	if b.Kind == "system" {
		kind = "system"
	}
	status := ""
	if b.From == ownerID {
		status = "Delivered"
	}
	return Message{
		Seq: e.Seq, ID: b.ID, Thread: b.Thread, From: b.From, Name: v.Name, Kind: kind,
		Avatar: v.Avatar, Color: v.Color, Text: b.Text, Mentions: b.Mentions, Time: e.Time,
		Status: status, Mine: b.From == ownerID,
	}
}

// Post records Deonte's message and, when sync is set, the replies.
// Async callers should invoke Respond afterwards.
func (s *Service) Post(ctx context.Context, thread, text string, sync bool) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, errors.New("message is empty")
	}
	if len(text) > 4000 {
		return Message{}, errors.New("message is too long")
	}
	if _, ok := s.thread(thread); !ok {
		return Message{}, fmt.Errorf("unknown thread %q", thread)
	}
	mentions := s.parseMentions(text)
	entry, err := s.append(agentterm.ChatBody{
		Thread: thread, ID: newID("m"), Kind: "message", From: ownerID, Text: text, Mentions: mentions, Status: "delivered",
	})
	if err != nil {
		return Message{}, err
	}
	body, _ := decode(entry)
	msg := s.bubble(entry, body)
	if sync {
		if err := s.Respond(ctx, thread); err != nil {
			return msg, err
		}
	}
	return msg, nil
}

// Respond lets characters answer the latest unanswered owner message.
func (s *Service) Respond(ctx context.Context, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	th, ok := s.thread(threadID)
	if !ok {
		return fmt.Errorf("unknown thread %q", threadID)
	}
	var lastOwner string
	var lastOwnerMentions []string
	answered := false
	var history []agentterm.ChatBody
	for _, e := range s.entries() {
		b, isChat := decode(e)
		if !isChat || b.Thread != threadID || (b.Kind != "message" && b.Kind != "system") {
			continue
		}
		history = append(history, b)
		if b.From == ownerID && b.Kind == "message" {
			lastOwner = b.Text
			lastOwnerMentions = b.Mentions
			answered = false
			continue
		}
		if b.Kind == "message" {
			answered = true
		}
	}
	if lastOwner == "" || answered {
		return nil
	}
	queue := append([]string(nil), lastOwnerMentions...)
	if len(queue) == 0 {
		if !th.Group {
			for _, m := range th.Members {
				if m != ownerID {
					queue = append(queue, m)
				}
			}
		} else {
			queue = append(queue, "operator")
		}
	}
	guard := s.Guard.normalize()
	seen := map[string]bool{}
	turns := 0
	spent := estimateTokens(lastOwner)
	peers := s.peers(th)
	for len(queue) > 0 && guard.AllowTurn(turns) {
		id := queue[0]
		queue = queue[1:]
		if !Admit(seen, id) || id == ownerID || id == "system" {
			continue
		}
		c, ok := s.Reg.Get(id)
		if !ok {
			continue
		}
		presence := c.Presence(s.getenv, s.Demo)
		if !presence.Online {
			if _, err := s.append(agentterm.ChatBody{
				Thread: threadID, ID: newID("s"), Kind: "system", From: "system",
				Text: c.Name + " is offline. " + presence.Note,
			}); err != nil {
				return err
			}
			continue
		}
		if !guard.AllowSpend(spent) {
			if _, err := s.append(agentterm.ChatBody{
				Thread: threadID, ID: newID("s"), Kind: "system", From: "system",
				Text: "Operator closed the round. This message hit the guild cost cap.",
			}); err != nil {
				return err
			}
			break
		}
		turns++
		prompt := lastOwner
		system := c.SystemPrompt(peers)
		prior := s.prior(c.ID, history)
		text, tokens, err := s.complete(ctx, Job{Character: c, Thread: threadID, System: system, Prompt: prompt, Prior: prior})
		if err != nil {
			if _, werr := s.append(agentterm.ChatBody{
				Thread: threadID, ID: newID("s"), Kind: "system", From: "system",
				Text: c.Name + " couldn't reach the model. The key stays on the server. " + c.Enable,
			}); werr != nil {
				return werr
			}
			break
		}
		spent += tokens
		if th.Group {
			for _, m := range s.parseMentions(text) {
				if m != id && !seen[m] {
					queue = append(queue, m)
				}
			}
		}
		_ = text
	}
	return nil
}

func (s *Service) peers(th Thread) []Character {
	var out []Character
	for _, id := range th.Members {
		if c, ok := s.Reg.Get(id); ok {
			out = append(out, c)
		}
	}
	return out
}

func (s *Service) prior(self string, history []agentterm.ChatBody) []adapter.Turn {
	if len(history) > 0 {
		history = history[:len(history)-1] // the latest owner line is the prompt
	}
	if len(history) > 12 {
		history = history[len(history)-12:]
	}
	var out []adapter.Turn
	for _, b := range history {
		if b.Kind != "message" || strings.TrimSpace(b.Text) == "" {
			continue
		}
		if b.From == self {
			out = append(out, adapter.Turn{Role: "assistant", Text: b.Text})
			continue
		}
		name := b.From
		if b.From == ownerID {
			name = ownerName
		} else if c, ok := s.Reg.Get(b.From); ok {
			name = c.Name
		}
		out = append(out, adapter.Turn{Role: "user", Text: name + ": " + b.Text})
	}
	return out
}

func (s *Service) complete(ctx context.Context, job Job) (string, int, error) {
	if s.Demo || job.Character.Provider == "local" {
		text := clipBytes(job.Character.LocalReply(job.Prompt), 4000)
		tokens := estimateTokens(job.System, job.Prompt, text)
		if _, err := s.recordReply(job, text, tokens); err != nil {
			return "", 0, err
		}
		return text, tokens, nil
	}
	completer := s.Complete
	if completer == nil {
		completer = LiveCompleter{Getenv: s.getenv, OnSay: func(c Character, text string, tokens int) (adapter.EventAck, error) {
			entry, err := s.recordReply(Job{Character: c, Thread: job.Thread}, text, tokens)
			if err != nil {
				return adapter.EventAck{}, err
			}
			return adapter.EventAck{Seq: entry.Seq, Hash: entry.Hash}, nil
		}}
	}
	res, err := completer.Complete(ctx, job)
	if err != nil {
		return "", 0, err
	}
	text := clipBytes(strings.TrimSpace(res.Text), 4000)
	if text == "" {
		return "", 0, errors.New("empty reply")
	}
	if !res.Recorded {
		if _, err := s.recordReply(job, text, res.Tokens); err != nil {
			return "", 0, err
		}
	}
	if res.Tokens <= 0 {
		res.Tokens = estimateTokens(text)
	}
	return text, res.Tokens, nil
}

func (s *Service) recordReply(job Job, text string, tokens int) (agentterm.Entry, error) {
	text = clipBytes(strings.TrimSpace(text), 4000)
	return s.append(agentterm.ChatBody{
		Thread: job.Thread, ID: newID("m"), Kind: "message", From: job.Character.ID,
		Text: text, Mentions: s.parseMentions(text), Tokens: tokens, Status: "delivered",
	})
}

func (s *Service) parseMentions(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		id := m[1]
		if _, ok := s.Reg.Get(id); !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) == 8 {
			break
		}
	}
	return out
}

func newID(prefix string) string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return prefix + hex.EncodeToString(buf[:])
}

// Verify runs agentterm's checker on the in-memory chain.
func (s *Service) Verify() (agentterm.VerifyReport, error) {
	return s.Term.Verify()
}

// ServerPublicKey exposes the key for offline verify.
func ServerPublicKey(key ed25519.PublicKey) string {
	return hex.EncodeToString(key)
}
