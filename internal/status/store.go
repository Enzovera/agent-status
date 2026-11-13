// Package status keeps the last known state of every coding-agent session
// reported by hooks (Claude Code) and plugins (OpenCode).
package status

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateWorking State = "working"
	StateWaiting State = "waiting"
	StateIdle    State = "idle"
	StateError   State = "error"
	StateEnded   State = "ended"
)

// Event is a normalized report from any agent. Empty State means
// "nothing changed, the session is just alive".
type Event struct {
	Agent     string
	Host      string
	SessionID string
	Cwd       string
	Kind      string
	State     State
	Task      string
	Detail    string
	At        time.Time

	// Optional extras shown in the session details.
	Tool       string // tool about to run; counted per name
	Prompt     string // full user prompt
	Message    string // last assistant reply or error text
	Model      string
	Mode       string // permission mode
	Permission bool   // the agent asked for a permission
}

// HistoryEntry is one meaningful event in a session's recent past.
type HistoryEntry struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	State  State     `json:"state,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

type Session struct {
	Key       string    `json:"key"`
	Agent     string    `json:"agent"`
	Host      string    `json:"host"`
	SessionID string    `json:"sessionId"`
	Project   string    `json:"project"`
	Cwd       string    `json:"cwd"`
	State     State     `json:"state"`
	Task      string    `json:"task"`
	Detail    string    `json:"detail"`
	LastEvent string    `json:"lastEvent"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Stale     bool      `json:"stale"`

	Model       string         `json:"model,omitempty"`
	Mode        string         `json:"mode,omitempty"`
	LastPrompt  string         `json:"lastPrompt,omitempty"`
	LastMessage string         `json:"lastMessage,omitempty"`
	Prompts     int            `json:"prompts"`
	ToolCalls   int            `json:"toolCalls"`
	Permissions int            `json:"permissions"`
	Tools       map[string]int `json:"tools,omitempty"`
	History     []HistoryEntry `json:"history,omitempty"`
}

// clone copies the session deeply enough to be read outside the store lock.
func (s *Session) clone() Session {
	c := *s
	c.Tools = maps.Clone(s.Tools)
	c.History = append([]HistoryEntry(nil), s.History...)
	return c
}

const (
	staleAfter    = 10 * time.Minute
	keepEndedFor  = 2 * time.Hour
	keepSilentFor = 24 * time.Hour
	historyLen    = 30
)

type Store struct {
	mu       sync.Mutex
	path     string
	sessions map[string]*Session
	dirty    bool
}

// NewStore loads state from path if it exists. An empty path disables persistence.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, sessions: map[string]*Session{}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Session
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, sess := range list {
		s.sessions[sess.Key] = sess
	}
	return s, nil
}

func (s *Store) Apply(ev Event) {
	if ev.SessionID == "" {
		return
	}
	key := ev.Agent + "/" + ev.Host + "/" + ev.SessionID

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[key]
	if !ok {
		sess = &Session{
			Key:       key,
			Agent:     ev.Agent,
			Host:      ev.Host,
			SessionID: ev.SessionID,
			State:     StateIdle,
			StartedAt: ev.At,
		}
		s.sessions[key] = sess
	}
	if ev.Cwd != "" {
		sess.Cwd = ev.Cwd
		sess.Project = projectName(ev.Cwd)
	}
	if ev.Task != "" {
		sess.Task = ev.Task
	}
	if ev.State != "" {
		sess.State = ev.State
		sess.Detail = ev.Detail
	} else if ev.Detail != "" {
		sess.Detail = ev.Detail
	}
	if ev.Model != "" {
		sess.Model = ev.Model
	}
	if ev.Mode != "" {
		sess.Mode = ev.Mode
	}
	if ev.Prompt != "" {
		sess.LastPrompt = ev.Prompt
		sess.Prompts++
	}
	if ev.Message != "" {
		sess.LastMessage = ev.Message
	}
	if ev.Tool != "" {
		if sess.Tools == nil {
			sess.Tools = map[string]int{}
		}
		sess.Tools[ev.Tool]++
		sess.ToolCalls++
	}
	if ev.Permission {
		sess.Permissions++
	}
	sess.addHistory(ev)
	sess.LastEvent = ev.Kind
	sess.UpdatedAt = ev.At
	s.dirty = true
}

// addHistory records events that change something visible. A repeat of the
// previous entry only refreshes its time, so status heartbeats don't flood it.
func (s *Session) addHistory(ev Event) {
	if ev.State == "" && ev.Detail == "" {
		return
	}
	e := HistoryEntry{At: ev.At, Kind: ev.Kind, State: ev.State, Detail: ev.Detail}
	if n := len(s.History); n > 0 {
		last := &s.History[n-1]
		if last.Kind == e.Kind && last.State == e.State && last.Detail == e.Detail {
			last.At = e.At
			return
		}
	}
	s.History = append(s.History, e)
	if over := len(s.History) - historyLen; over > 0 {
		s.History = append([]HistoryEntry(nil), s.History[over:]...)
	}
}

// List returns sessions worth showing, most urgent first.
func (s *Store) List(now time.Time) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Session, 0, len(s.sessions))
	for key, sess := range s.sessions {
		age := now.Sub(sess.UpdatedAt)
		if age > keepSilentFor || (sess.State == StateEnded && age > keepEndedFor) {
			delete(s.sessions, key)
			s.dirty = true
			continue
		}
		c := sess.clone()
		c.Stale = c.State == StateWorking && age > staleAfter
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rank(out[i].State), rank(out[j].State)
		if ri != rj {
			return ri < rj
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

// Save writes the state file if anything changed since the last save.
func (s *Store) Save() error {
	s.mu.Lock()
	if s.path == "" || !s.dirty {
		s.mu.Unlock()
		return nil
	}
	list := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		c := sess.clone()
		list = append(list, &c)
	}
	s.dirty = false
	s.mu.Unlock()

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func rank(st State) int {
	switch st {
	case StateWaiting:
		return 0
	case StateError:
		return 1
	case StateWorking:
		return 2
	case StateIdle:
		return 3
	default:
		return 4
	}
}

func projectName(cwd string) string {
	// Paths come from other machines, so accept both separators.
	clean := strings.TrimRight(cwd, `/\`)
	if clean == "" {
		return cwd
	}
	return clean[strings.LastIndexAny(clean, `/\`)+1:]
}
