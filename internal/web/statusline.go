package web

import (
	"io"
	"net/http"

	"agent-status/internal/status"
)

// statusline takes the JSON Claude Code passes to its statusLine command,
// forwarded as is by clients/claude/statusline: context window use and cost.
func (s *Server) statusline(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	m, err := status.ParseStatusline(hostOf(r), body, s.now())
	if err != nil {
		s.log.Warn("bad statusline payload", "err", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if rec, ok := s.store.ApplyStatusline(m); ok && s.archive != nil {
		if err := s.archive.Write(rec); err != nil {
			s.log.Warn("archive statusline", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
