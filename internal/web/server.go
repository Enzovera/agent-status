// Package web serves the hook endpoints and the read-only status page.
package web

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"agent-status/internal/stats"
	"agent-status/internal/status"
)

//go:embed index.html
var indexHTML []byte

//go:embed stats.html
var statsHTML []byte

const maxBody = 1 << 20

type Server struct {
	store    *status.Store
	archive  *status.Archive // nil: events are not archived
	machines *status.Machines
	log      *slog.Logger
	now      func() time.Time
}

func New(store *status.Store, archive *status.Archive, machines *status.Machines, log *slog.Logger) *Server {
	return &Server{store: store, archive: archive, machines: machines, log: log, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hook/claude", s.hook(status.ParseClaude))
	mux.HandleFunc("POST /hook/opencode", s.hook(status.ParseOpenCode))
	// Sent every minute by each client machine (the installer sets it up).
	mux.HandleFunc("POST /hook/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /api/sessions", s.sessions)
	// DELETE is not a "simple" request, so other sites can't trigger it from a browser.
	mux.HandleFunc("DELETE /api/sessions", s.remove)
	// PUT with a JSON body is not a "simple" request either.
	mux.HandleFunc("PUT /api/hosts", s.renameHost)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	// Statistics from the event archive (see internal/stats).
	mux.HandleFunc("GET /api/stats", s.apiStats)
	mux.HandleFunc("GET /api/report", s.apiReport)
	mux.HandleFunc("GET /api/events", s.apiEvents)
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(statsHTML)
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
		if rec, ok := s.store.Apply(ev); ok && s.archive != nil {
			if err := s.archive.Write(rec); err != nil {
				s.log.Warn("archive event", "err", err)
			}
		}
		// Empty 2xx body: Claude Code treats it as "no decision", so the hook never
		// blocks or adds context to the session.
		w.WriteHeader(http.StatusNoContent)
	}
}

// heartbeat marks the machine as online; the body is ignored.
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	s.machines.Beat(hostOf(r), ip, s.now())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"now":       s.now(),
		"sessions":  s.store.List(s.now()),
		"hostNames": s.store.HostNames(),
		"machines":  s.machines.List(s.now()),
	})
}

// renameHost sets the name shown for a machine: body {"host": "...", "name": "..."}.
// An empty name restores the name the agents report.
func (s *Server) renameHost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Host == "" {
		http.Error(w, "need JSON body with host", http.StatusBadRequest)
		return
	}
	if err := s.store.SetHostName(req.Host, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.log.Info("host renamed", "host", req.Host, "name", req.Name)
	w.WriteHeader(http.StatusNoContent)
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

// errNoArchive: statistics need the event archive, which can be turned off.
var errNoArchive = errors.New("archiwum zdarzeń jest wyłączone (serwer uruchomiony z -archive \"\" albo bez pliku -state); statystyki są niedostępne")

// period reads ?days= or ?from=&to= and answers 400/503 itself when it can't.
func (s *Server) period(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	if s.archive == nil {
		http.Error(w, errNoArchive.Error(), http.StatusServiceUnavailable)
		return time.Time{}, time.Time{}, false
	}
	from, to, err := stats.ParsePeriod(r.URL.Query(), s.now(), time.Local)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func (s *Server) loadStats(w http.ResponseWriter, r *http.Request) (*stats.Stats, bool) {
	from, to, ok := s.period(w, r)
	if !ok {
		return nil, false
	}
	st, err := stats.Load(s.archive.Dir(), stats.Options{
		From: from, To: to, Now: s.now(), Loc: time.Local, HostNames: s.store.HostNames(),
	})
	if err != nil {
		s.log.Warn("read archive", "err", err)
		http.Error(w, "read archive: "+err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return st, true
}

// apiStats: GET /api/stats?days=N | ?from=&to= — statistics as JSON.
func (s *Server) apiStats(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStats(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(st)
}

// apiReport: GET /api/report — the same statistics as Markdown for an AI agent.
func (s *Server) apiReport(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStats(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = stats.Report(w, st)
}

// apiEvents: GET /api/events — raw archive records of the period as JSON
// lines, optionally only ?host=, ?project=, ?key=.
func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.period(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	host, project, key := q.Get("host"), q.Get("project"), q.Get("key")
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	bw := bufio.NewWriterSize(w, 64*1024)
	enc := json.NewEncoder(bw)
	err := status.ReadArchive(s.archive.Dir(), from, to, func(rec status.Record) error {
		if (host != "" && rec.Host != host) || (project != "" && rec.Project != project) || (key != "" && rec.Key != key) {
			return nil
		}
		return enc.Encode(rec)
	})
	if err != nil {
		// Headers are gone already; the log is the only place left to tell.
		s.log.Warn("stream archive", "err", err)
	}
	_ = bw.Flush()
}
