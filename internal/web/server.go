// Package web serves the hook endpoints and the read-only status page.
package web

import (
	_ "embed"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"agent-status/internal/status"
)

//go:embed index.html
var indexHTML []byte

const maxBody = 1 << 20

type Server struct {
	store *status.Store
	log   *slog.Logger
	now   func() time.Time
}

func New(store *status.Store, log *slog.Logger) *Server {
	return &Server{store: store, log: log, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hook/claude", s.hook(status.ParseClaude))
	mux.HandleFunc("POST /hook/opencode", s.hook(status.ParseOpenCode))
	mux.HandleFunc("GET /api/sessions", s.sessions)
	// DELETE is not a "simple" request, so other sites can't trigger it from a browser.
	mux.HandleFunc("DELETE /api/sessions", s.remove)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	return mux
}

type parser func(host string, body []byte, at time.Time) (status.Event, error)

func (s *Server) hook(parse parser) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		ev, err := parse(hostOf(r), body, s.now())
		if err != nil {
			s.log.Warn("bad hook payload", "path", r.URL.Path, "err", err)
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		s.store.Apply(ev)
		// Empty 2xx body: Claude Code treats it as "no decision", so the hook never
		// blocks or adds context to the session.
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"now":      s.now(),
		"sessions": s.store.List(s.now()),
	})
}

// remove deletes sessions from the list: ?key=<key> (repeatable) or ?ended=1.
func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var n int
	switch {
	case q.Get("ended") == "1":
		n = s.store.RemoveEnded()
	case len(q["key"]) > 0:
		n = s.store.Remove(q["key"]...)
	default:
		http.Error(w, "need key or ended=1", http.StatusBadRequest)
		return
	}
	s.log.Info("sessions removed", "count", n)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]int{"removed": n})
}

// hostOf prefers the name set in the hook URL (?host=), then the client address.
func hostOf(r *http.Request) string {
	if h := r.URL.Query().Get("host"); h != "" {
		return h
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if net.ParseIP(ip).IsLoopback() {
		if name, err := os.Hostname(); err == nil {
			return name
		}
	}
	return ip
}
