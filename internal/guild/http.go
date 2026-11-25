package guild

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Handler is the owner-token API. Provider keys are not in any response.
func Handler(svc *Service, token, allowOrigin string) http.Handler {
	mux := http.NewServeMux()
	h := &api{svc: svc, token: token, origin: allowOrigin}
	mux.HandleFunc("GET /v1/health", h.health)
	mux.HandleFunc("GET /v1/characters", h.authed(h.characters))
	mux.HandleFunc("GET /v1/threads", h.authed(h.threads))
	mux.HandleFunc("POST /v1/threads", h.authed(h.createThread))
	mux.HandleFunc("GET /v1/threads/{id}/messages", h.authed(h.messages))
	mux.HandleFunc("POST /v1/threads/{id}/messages", h.authed(h.postMessage))
	mux.HandleFunc("POST /v1/threads/{id}/lines", h.authed(h.postLine))
	mux.HandleFunc("POST /v1/threads/{id}/respond", h.authed(h.respond))
	return mux
}

type api struct {
	svc    *Service
	token  string
	origin string
}

func (a *api) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *api) authed(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.origin {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 || a.token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (a *api) characters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"characters": a.svc.Characters()})
}

func (a *api) threads(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"threads": a.svc.Threads()})
}

func (a *api) createThread(w http.ResponseWriter, r *http.Request) {
	var in struct {
		With string `json:"with"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	thread, err := a.svc.OpenDM(in.With)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (a *api) messages(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	msgs, err := a.svc.Messages(r.PathValue("id"), after)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if msgs == nil {
		msgs = []Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (a *api) postMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Reply string `json:"reply,omitempty"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	msg, err := a.svc.Post(r.Context(), id, in.Text, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// "hold" lets Vercel AI Gateway write the replies. The VM providers run
	// only when the caller asks via /respond (the fallback).
	if in.Reply != "hold" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			_ = a.svc.Respond(ctx, id)
		}()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"message": msg, "pending": in.Reply != "hold"})
}

func (a *api) postLine(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From string `json:"from"`
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg, err := a.svc.RecordCharacter(r.PathValue("id"), in.From, in.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

func (a *api) respond(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_ = a.svc.Respond(ctx, id)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"pending": true, "via": "provider"})
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return errors.New("bad json")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
