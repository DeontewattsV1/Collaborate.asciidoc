package collab

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Store *Store
	Token string
	mu    sync.Mutex
	subs  map[chan Event]struct{}
}

func NewServer(s *Store, token string) *Server {
	return &Server{Store: s, Token: token, subs: map[chan Event]struct{}{}}
}
func (s *Server) broadcast(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default: /* clients replay on reconnect */
		}
	}
}
func (s *Server) authenticated(w http.ResponseWriter, r *http.Request) bool {
	if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	}
	if status == 500 {
		log.Printf("internal error: %v", err)
	}
	jsonReply(w, status, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		jsonReply(w, 400, map[string]string{"error": "invalid request: " + err.Error()})
		return false
	}
	return true
}
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in RegisterInput
		if !decode(w, r, &in) {
			return
		}
		e, err := s.Store.Register(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, e)
	})
	mux.HandleFunc("/v1/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in MessageInput
		if !decode(w, r, &in) {
			return
		}
		e, err := s.Store.Say(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, e)
	})
	mux.HandleFunc("/v1/lease", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in LeaseInput
		if !decode(w, r, &in) {
			return
		}
		l, e, err := s.Store.Claim(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, map[string]any{"lease": l, "event": e})
	})
	mux.HandleFunc("/v1/renew", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in RenewInput
		if !decode(w, r, &in) {
			return
		}
		l, e, err := s.Store.Renew(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, map[string]any{"lease": l, "event": e})
	})
	mux.HandleFunc("/v1/release", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in ReleaseInput
		if !decode(w, r, &in) {
			return
		}
		e, err := s.Store.Release(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, e)
	})
	mux.HandleFunc("/v1/receipt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in ReceiptInput
		if !decode(w, r, &in) {
			return
		}
		e, err := s.Store.Receipt(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, e)
	})
	mux.HandleFunc("/v1/gate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in GateInput
		if !decode(w, r, &in) {
			return
		}
		e, err := s.Store.Gate(in)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcast(e)
		jsonReply(w, 201, e)
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if r.URL.Query().Get("after") == "" {
			after = 0
			err = nil
		}
		if err != nil || after < 0 {
			http.Error(w, "invalid cursor", 400)
			return
		}
		events, err := s.Store.Events(after)
		if err != nil {
			fail(w, err)
			return
		}
		jsonReply(w, 200, events)
	})
	mux.HandleFunc("/v1/watch", s.watch)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(w, r) {
			return
		}
		// Prevent browsers from using this bearer-token API cross-origin.
		if origin := r.Header.Get("Origin"); origin != "" {
			http.Error(w, "origin not permitted", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) Handler() http.Handler { return s.routes() }
func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after = 0
		err = nil
	}
	if err != nil || after < 0 {
		http.Error(w, "invalid cursor", 400)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	ch := make(chan Event, 256)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subs, ch); s.mu.Unlock() }()
	events, err := s.Store.Events(after)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, ": stream-open\n\n")
	flusher.Flush()
	write := func(e Event) error {
		if e.Seq <= after {
			return nil
		}
		b, _ := json.Marshal(e)
		_, err := fmt.Fprintf(w, "id: %d\nevent: collab\ndata: %s\n\n", e.Seq, b)
		if err == nil {
			flusher.Flush()
			after = e.Seq
		}
		return err
	}
	for _, e := range events {
		if write(e) != nil {
			return
		}
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case e := <-ch:
			if err := write(e); err != nil {
				return
			}
		case <-ticker.C: // Reconcile any dropped notifications.
			events, err := s.Store.Events(after)
			if err != nil {
				return
			}
			for _, e := range events {
				if write(e) != nil {
					return
				}
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// SafeListenAddr allows loopback by default; public exposure requires a separately configured TLS proxy.
func SafeListenAddr(addr string) error {
	if !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") && !strings.HasPrefix(addr, "[::1]:") {
		return errors.New("non-loopback binding refused; deploy behind a dedicated authenticated TLS proxy")
	}
	return nil
}
