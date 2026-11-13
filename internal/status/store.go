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
	"unicode/utf8"
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

	// Ask is a question the agent will show the user when its turn ends;
	// the event that ends the turn (EndsTurn) then means "waiting", not "idle".
	Ask      string
	EndsTurn bool
	// KeepWaiting: the State is only a reminder that the agent is idle and
	// must not hide that it still waits for an answer.
	KeepWaiting bool
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
	// PendingAsk is the question to show once the current turn ends.
	PendingAsk string `json:"pendingAsk,omitempty"`
	History     []HistoryEntry `json:"history,omitempty"`
	// Timeline holds only state changes, kept much longer than History, so the
	// page can draw what a session was doing over the last hours.
	Timeline []StateChange `json:"timeline,omitempty"`
}

// StateChange marks the moment a session entered a state.
type StateChange struct {
	At    time.Time `json:"at"`
	State State     `json:"state"`
}

// clone copies the session deeply enough to be read outside the store lock.
func (s *Session) clone() Session {
	c := *s
	c.Tools = maps.Clone(s.Tools)
	c.History = append([]HistoryEntry(nil), s.History...)
	c.Timeline = append([]StateChange(nil), s.Timeline...)
	return c
}

const (
	staleAfter    = 10 * time.Minute
	keepEndedFor  = 2 * time.Hour
	keepSilentFor = 24 * time.Hour
	historyLen    = 30
	timelineFor   = 24 * time.Hour
	timelineLen   = 2000
)

const (
	maxHostName  = 64  // runes in the display name of a machine
	maxHostNames = 500 // renamed machines kept at once
)

// ErrTooManyNames is returned when no more machines can be renamed.
var ErrTooManyNames = errors.New("too many renamed machines")

type Store struct {
	mu       sync.Mutex
	path     string
	sessions map[string]*Session
	// names maps a machine as agents report it to the name shown on the page.
	// Unlike sessions, it never expires.
	names map[string]string
	dirty bool
}

// stateFile is the on-disk format. Older versions stored only the session list.
type stateFile struct {
	Sessions  []*Session        `json:"sessions"`
	HostNames map[string]string `json:"hostNames,omitempty"`
}

// NewStore loads state from path if it exists. An empty path disables persistence.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, sessions: map[string]*Session{}, names: map[string]string{}}
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
	var st stateFile
	if strings.HasPrefix(strings.TrimSpace(string(b)), "[") {
		err = json.Unmarshal(b, &st.Sessions)
	} else {
		err = json.Unmarshal(b, &st)
	}
	if err != nil {
		return nil, err
	}
	for _, sess := range st.Sessions {
		s.sessions[sess.Key] = sess
	}
	maps.Copy(s.names, st.HostNames)
	return s, nil
}

// HostNames returns the display names of renamed machines, keyed by the
// machine name agents report.
func (s *Store) HostNames() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.names)
}

// SetHostName sets the name shown for a machine. An empty name, or the
// machine's own name, goes back to what the agents report.
func (s *Store) SetHostName(host, name string) error {
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxHostName {
		name = string([]rune(name)[:maxHostName])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.names[host]
	if name == "" || name == host {
		if had {
			delete(s.names, host)
			s.dirty = true
		}
		return nil
	}
	if !had && len(s.names) >= maxHostNames {
		return ErrTooManyNames
	}
	if old != name {
		s.names[host] = name
		s.dirty = true
	}
	return nil
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
	if ev.Prompt != "" || ev.State == StateEnded {
		sess.PendingAsk = ""
	}
	if ev.Ask != "" {
		sess.PendingAsk = ev.Ask
	}
	if ev.EndsTurn && sess.PendingAsk != "" {
		ev.State = StateWaiting
		ev.Detail = sess.PendingAsk
		sess.PendingAsk = ""
	}
	if ev.KeepWaiting && sess.State == StateWaiting {
		ev.State = ""
		ev.Detail = ""
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
	// A finished turn shows the start of the last reply, whichever agent sent it.
	if ev.State == StateIdle && ev.Detail == "" && sess.LastMessage != "" {
		sess.Detail = clip(sess.LastMessage)
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
	sess.addTimeline(ev.At)
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

// addTimeline records the current state if it differs from the last recorded
// one. Entries older than timelineFor are dropped, except the newest of them,
// which still says what state the session was in when the window starts.
func (s *Session) addTimeline(at time.Time) {
	if n := len(s.Timeline); n == 0 || s.Timeline[n-1].State != s.State {
		s.Timeline = append(s.Timeline, StateChange{At: at, State: s.State})
	}
	cut := 0
	for cut+1 < len(s.Timeline) && at.Sub(s.Timeline[cut+1].At) > timelineFor {
		cut++
	}
	if over := len(s.Timeline) - timelineLen; over > cut {
		cut = over
	}
	if cut > 0 {
		s.Timeline = append([]StateChange(nil), s.Timeline[cut:]...)
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

// Remove forgets the given sessions and returns how many existed. A session
// that keeps reporting comes back with its next event.
func (s *Store) Remove(keys ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range keys {
		if _, ok := s.sessions[k]; ok {
			delete(s.sessions, k)
			n++
		}
	}
	if n > 0 {
		s.dirty = true
	}
	return n
}

// RemoveEnded forgets every session that has ended and returns how many.
func (s *Store) RemoveEnded() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, sess := range s.sessions {
		if sess.State == StateEnded {
			delete(s.sessions, k)
			n++
		}
	}
	if n > 0 {
		s.dirty = true
	}
	return n
}

// Save writes the state file if anything changed since the last save.
func (s *Store) Save() error {
	s.mu.Lock()
	if s.path == "" || !s.dirty {
		s.mu.Unlock()
		return nil
	}
	st := stateFile{Sessions: make([]*Session, 0, len(s.sessions)), HostNames: maps.Clone(s.names)}
	for _, sess := range s.sessions {
		c := sess.clone()
		st.Sessions = append(st.Sessions, &c)
	}
	s.dirty = false
	s.mu.Unlock()

	b, err := json.MarshalIndent(st, "", "  ")
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
