package status

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

// KindStatusline is the Record.Kind of the metrics Claude Code's statusLine
// command forwards (see clients/claude/statusline).
const KindStatusline = "Statusline"

const (
	// A statusline update is archived only when the cost grew by costStep or
	// the context use moved by contextStep percentage points since the last
	// archived one; the statusline refreshes after every assistant message.
	costStep    = 0.01
	contextStep = 1.0
	// loggedFor: how long the last archived cost of a session is remembered
	// after it was archived (a session resumed later is counted from there).
	loggedFor = 60 * 24 * time.Hour
)

// Statusline is what the server keeps from the JSON Claude Code passes to its
// statusLine command on every refresh.
type Statusline struct {
	Agent     string
	Host      string
	SessionID string
	Cwd       string
	Model     string
	Title     string // session_name
	At        time.Time

	ContextPct    float64 // 0–100; 0 when unknown (before the first reply, right after /compact)
	ContextTokens int
	ContextLimit  int
	CostUsd       float64
	LinesAdded    int
	LinesRemoved  int
}

// metricsMark is the last archived cost and context use of a session.
type metricsMark struct {
	Cost    float64   `json:"cost"`
	Context float64   `json:"ctx"`
	At      time.Time `json:"at"`
}

// statuslinePayload follows https://code.claude.com/docs/en/statusline
// ("Available data"). Numbers are read as float64 so an integer sent as 1.0,
// or the other way round, never makes the payload unreadable.
type statuslinePayload struct {
	SessionID   string          `json:"session_id"`
	SessionName string          `json:"session_name"`
	Cwd         string          `json:"cwd"`
	Model       json.RawMessage `json:"model"`
	Workspace   struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cost struct {
		TotalCostUsd      float64 `json:"total_cost_usd"`
		TotalLinesAdded   float64 `json:"total_lines_added"`
		TotalLinesRemoved float64 `json:"total_lines_removed"`
	} `json:"cost"`
	ContextWindow struct {
		TotalInputTokens  float64  `json:"total_input_tokens"`
		ContextWindowSize float64  `json:"context_window_size"`
		UsedPercentage    *float64 `json:"used_percentage"` // null early in a session
		CurrentUsage      *struct {
			InputTokens              float64 `json:"input_tokens"`
			CacheCreationInputTokens float64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     float64 `json:"cache_read_input_tokens"`
		} `json:"current_usage"` // null before the first API call and after /compact
	} `json:"context_window"`
}

// ParseStatusline reads the statusLine JSON of a Claude Code session.
func ParseStatusline(host string, body []byte, at time.Time) (Statusline, error) {
	var p statuslinePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Statusline{}, err
	}
	if p.SessionID == "" {
		return Statusline{}, errors.New("no session_id")
	}
	cw := p.ContextWindow
	tokens := cw.TotalInputTokens
	if tokens <= 0 && cw.CurrentUsage != nil {
		u := cw.CurrentUsage
		tokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	}
	pct := 0.0
	switch {
	case cw.UsedPercentage != nil:
		pct = *cw.UsedPercentage
	case tokens > 0 && cw.ContextWindowSize > 0:
		pct = tokens / cw.ContextWindowSize * 100
	}
	return Statusline{
		Agent:         "claude",
		Host:          host,
		SessionID:     p.SessionID,
		Cwd:           firstNonEmpty(p.Cwd, p.Workspace.CurrentDir),
		Model:         clip(statuslineModel(p.Model)),
		Title:         clip(p.SessionName),
		At:            at,
		ContextPct:    math.Round(min(max(pct, 0), 100)*10) / 10,
		ContextTokens: count(tokens),
		ContextLimit:  count(cw.ContextWindowSize),
		CostUsd:       math.Round(max(p.Cost.TotalCostUsd, 0)*1e6) / 1e6,
		LinesAdded:    count(p.Cost.TotalLinesAdded),
		LinesRemoved:  count(p.Cost.TotalLinesRemoved),
	}, nil
}

// statuslineModel prefers the model id, which is what hooks report.
func statuslineModel(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	_ = json.Unmarshal(raw, &o)
	return firstNonEmpty(o.ID, o.DisplayName)
}

func count(f float64) int {
	if f <= 0 || f > math.MaxInt32 {
		return 0
	}
	return int(math.Round(f))
}

// ApplyStatusline stores the metrics on their session. It never changes the
// session's state, details, history or UpdatedAt: the statusline refreshes on
// its own and says nothing about whether the agent works. A session the
// server doesn't know yet is created (idle) only when the payload names its
// directory; otherwise the update is dropped.
//
// It returns a record to archive when the cost or the context use changed
// enough since the session's last archived one (see costStep, contextStep).
func (s *Store) ApplyStatusline(m Statusline) (rec Record, archive bool) {
	if m.SessionID == "" {
		return Record{}, false
	}
	key := m.Agent + "/" + m.Host + "/" + m.SessionID

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, known := s.sessions[key]
	if !known {
		if m.Cwd == "" {
			return Record{}, false
		}
		sess = &Session{
			Key: key, Agent: m.Agent, Host: m.Host, SessionID: m.SessionID,
			State: StateIdle, StartedAt: m.At, UpdatedAt: m.At,
		}
		sess.addTimeline(m.At)
		s.sessions[key] = sess
	}
	if sess.Cwd == "" && m.Cwd != "" {
		sess.Cwd = m.Cwd
		sess.Project = projectName(m.Cwd)
	}
	if m.Model != "" {
		sess.Model = m.Model
	}
	if sess.Task == "" && m.Title != "" {
		sess.Task = m.Title
	}
	sess.ContextPct, sess.ContextTokens, sess.ContextLimit = m.ContextPct, m.ContextTokens, m.ContextLimit
	sess.CostUsd, sess.LinesAdded, sess.LinesRemoved = m.CostUsd, m.LinesAdded, m.LinesRemoved
	at := m.At
	sess.MetricsAt = &at
	s.dirty = true

	if s.logged == nil {
		s.logged = map[string]metricsMark{}
	}
	last, seen := s.logged[key]
	delta := m.CostUsd - last.Cost
	if delta < 0 {
		// The cost went down: the session's counter restarted, so everything
		// it shows now is new.
		delta = m.CostUsd
	}
	if !(delta >= costStep || math.Abs(m.ContextPct-last.Context) >= contextStep ||
		(!seen && (m.CostUsd > 0 || m.ContextPct > 0))) {
		return Record{}, false
	}
	if !seen {
		for k, v := range s.logged {
			if m.At.Sub(v.At) > loggedFor {
				delete(s.logged, k)
			}
		}
	}
	s.logged[key] = metricsMark{Cost: m.CostUsd, Context: m.ContextPct, At: m.At}
	return Record{
		At: m.At, Key: key, Agent: sess.Agent, Host: sess.Host, SessionID: sess.SessionID,
		Project: sess.Project, Cwd: sess.Cwd, Kind: KindStatusline,
		State: sess.State, Prev: sess.State, Title: sess.Task, Model: sess.Model, Mode: sess.Mode,
		CostUsd: m.CostUsd, CostDelta: math.Round(delta*1e6) / 1e6,
		ContextPct: m.ContextPct, ContextTokens: m.ContextTokens,
		LinesAdded: m.LinesAdded, LinesRemoved: m.LinesRemoved,
	}, true
}
