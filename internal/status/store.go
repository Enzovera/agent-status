// Package status keeps the last known state of every coding-agent session
// reported by hooks (Claude Code) and plugins (OpenCode).
package status

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
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
	// KeepWaiting: the event is only a reminder (a notification) and must not
	// change a session that already waits for the user.
	KeepWaiting bool

	// Extras kept in the archive for statistics (see Record).
	Subagent   string // subagent type (SubagentStart/SubagentStop)
	TodoID     string // task-list item id (TaskCreated/TaskCompleted)
	Todo       string // task-list item subject
	DurationMs int64  // how long a tool ran (PostToolUse/PostToolUseFailure)
	Failed     bool   // the tool call failed
	ErrorKind  string // machine-readable error type (StopFailure "error")
	Reason     string // SessionEnd reason, compaction trigger and the like
	// For Claude Code: PostToolUse/PostToolUseFailure carry the tool name here
	// (Tool would count the call twice), PermissionDenied the denial reason,
	// Notification its type, SessionStart/PostModelSwitch their source.

	// Bookkeeping for the session view only (not archived).
	SubagentID      string // subagent instance id (agent_id)
	SubagentStarted bool   // SubagentStart: SubagentID is now running
	SubagentSeen    bool   // SubagentID called a tool, so it is running
	SubagentDoing   string // what the subagent's tool call does (its own detail)
	// LaunchDesc/LaunchType: the main thread starts a subagent (Agent tool) for
	// this task as this agent; LaunchedID is the id the tool returned for it.
	LaunchDesc      string
	LaunchType      string
	LaunchedID      string
	SubagentStopped bool // SubagentStop: SubagentID has finished
	TodoDone        bool // TodoID was completed (otherwise created)
	Compacted       bool // the context was compacted
	// WaitFor names the tool call a waiting event waits on (see waitKey);
	// Resolves names a finished tool call. Claude Code sends no event when you
	// approve a permission or answer a question, so the end of that same tool
	// call is what tells the wait is over.
	WaitFor  string
	Resolves string
	// AgentID is the subagent the event comes from (empty: the main thread).
	AgentID string
	// ParentID is the session (same agent and host) that started this one as
	// a subagent; OpenCode runs its subagents as child sessions.
	ParentID string
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
	PendingAsk string         `json:"pendingAsk,omitempty"`
	History    []HistoryEntry `json:"history,omitempty"`
	// Timeline holds only state changes, kept much longer than History, so the
	// page can draw what a session was doing over the last hours.
	Timeline []StateChange `json:"timeline,omitempty"`

	// Subagents are the subagents running right now (started, not stopped).
	Subagents []Subagent `json:"subagents,omitempty"`
	// Launches are subagents asked for but not yet matched to a running one.
	Launches []Launch `json:"launches,omitempty"`
	// SubagentRuns counts subagents started over the session.
	SubagentRuns int `json:"subagentRuns"`
	// Compactions counts finished context compactions.
	Compactions int `json:"compactions"`
	// ToolFailures counts tool calls that failed, interrupted ones included.
	ToolFailures int `json:"toolFailures"`
	// TodosTotal and TodosDone count distinct task-list items created and
	// completed; TodoIDs maps each item's id to whether it is done.
	TodosTotal int             `json:"todosTotal"`
	TodosDone  int             `json:"todosDone"`
	TodoIDs    map[string]bool `json:"todoIds,omitempty"`
	// WaitFor is the tool call a waiting session waits on (see Event.WaitFor).
	WaitFor string `json:"waitFor,omitempty"`
	// ParentKey is the key of the session that started this one as a subagent
	// (an OpenCode child session); SubagentType is the agent it runs as.
	ParentKey    string `json:"parentKey,omitempty"`
	SubagentType string `json:"subagentType,omitempty"`

	// Context window use, cost and changed lines, from Claude Code's statusLine
	// command (see statusline.go). MetricsAt is when they last came: a
	// statusline refresh is no sign of work, so it never touches UpdatedAt.
	ContextPct    float64    `json:"contextPct,omitempty"` // 0–100
	ContextTokens int        `json:"contextTokens,omitempty"`
	ContextLimit  int        `json:"contextLimit,omitempty"`
	CostUsd       float64    `json:"costUsd,omitempty"`
	LinesAdded    int        `json:"linesAdded,omitempty"`
	LinesRemoved  int        `json:"linesRemoved,omitempty"`
	MetricsAt     *time.Time `json:"metricsAt,omitempty"`
}

// Subagent is one running subagent of a session.
type Subagent struct {
	ID    string    `json:"id"`
	Type  string    `json:"type"`
	Since time.Time `json:"since"`
	// LastSeen is the subagent's last tool call (or its start).
	LastSeen time.Time `json:"lastSeen,omitempty"`
	// Desc is what it was started for; Doing its latest tool call; Calls how many.
	Desc  string `json:"desc,omitempty"`
	Doing string `json:"doing,omitempty"`
	Calls int    `json:"calls,omitempty"`
}

// active is when the subagent last showed it was alive.
func (a Subagent) active() time.Time {
	if a.LastSeen.After(a.Since) {
		return a.LastSeen
	}
	return a.Since
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
	c.Subagents = append([]Subagent(nil), s.Subagents...)
	c.Launches = append([]Launch(nil), s.Launches...)
	c.TodoIDs = maps.Clone(s.TodoIDs)
	return c
}

const (
	staleAfter    = 10 * time.Minute
	keepEndedFor  = 2 * time.Hour
	keepSilentFor = 24 * time.Hour
	historyLen    = 30
	timelineFor   = 24 * time.Hour
	timelineLen   = 2000
	// A subagent silent for this long (no tool call) stops being shown: its stop
	// never arrived, or the machine has no SubagentStop hook and it only showed
	// up through its tool calls.
	subagentFor  = 20 * time.Minute
	maxSubagents = 50
	maxTodoIDs   = 1000
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
	// logged is the last cost and context use archived per session key. It
	// outlives the session, so a session resumed after it expired (its cost
	// keeps growing from where it was) is not archived twice.
	logged map[string]metricsMark
	dirty  bool
}

// stateFile is the on-disk format. Older versions stored only the session list.
type stateFile struct {
	Sessions  []*Session             `json:"sessions"`
	HostNames map[string]string      `json:"hostNames,omitempty"`
	Logged    map[string]metricsMark `json:"metricsLogged,omitempty"`
}

// NewStore loads state from path if it exists. An empty path disables persistence.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, sessions: map[string]*Session{}, names: map[string]string{}, logged: map[string]metricsMark{}}
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
	maps.Copy(s.logged, st.Logged)
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

// Apply updates the session the event belongs to and returns what to archive.
// ok is false when the event names no session (nothing was applied).
func (s *Store) Apply(ev Event) (rec Record, ok bool) {
	if ev.SessionID == "" {
		return Record{}, false
	}
	key := ev.Agent + "/" + ev.Host + "/" + ev.SessionID

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, known := s.sessions[key]
	if !known {
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
	prev := sess.State
	defer func() { rec = record(ev, sess, prev) }()
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
	if ev.Resolves != "" && sess.State == StateWaiting && sess.WaitFor == ev.Resolves {
		ev.State = StateWorking
	}
	// A subagent running in the background keeps calling tools while the main
	// thread waits for you (or the other way round): that is not an answer.
	if ev.State == StateWorking && ev.Tool != "" && sess.State == StateWaiting && sess.WaitFor != "" &&
		!strings.HasPrefix(sess.WaitFor, ev.AgentID+"|") {
		ev.State = ""
	}
	if ev.State != "" {
		if ev.State == StateWaiting {
			sess.WaitFor = ev.WaitFor
		} else {
			sess.WaitFor = ""
		}
		sess.State = ev.State
		sess.Detail = ev.Detail
	} else if ev.Detail != "" && sess.State != StateWaiting {
		// A side note (a subagent, a finished task item, a compaction) must
		// not hide what the user is being asked; History still gets it.
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
	if ev.Failed {
		sess.ToolFailures++
	}
	if ev.Compacted {
		sess.Compactions++
	}
	sess.trackSubagent(ev)
	sess.trackTodo(ev)
	s.trackChild(sess, ev)
	sess.addHistory(ev)
	sess.addTimeline(ev.At)
	sess.LastEvent = ev.Kind
	sess.UpdatedAt = ev.At
	s.dirty = true
	return Record{}, true
}

// record describes an applied event for the archive. ev is the event as
// Apply resolved it (a deferred question already turned into "waiting").
func record(ev Event, sess *Session, prev State) Record {
	return Record{
		At: ev.At, Key: sess.Key, Agent: sess.Agent, Host: sess.Host, SessionID: sess.SessionID,
		Project: sess.Project, Cwd: sess.Cwd, Kind: ev.Kind,
		State: sess.State, Prev: prev, Title: sess.Task, Detail: ev.Detail, Tool: ev.Tool,
		Prompt: clip(ev.Prompt), Model: sess.Model, Mode: sess.Mode, Permission: ev.Permission,
		Subagent: ev.Subagent, TodoID: ev.TodoID, Todo: ev.Todo, DurationMs: ev.DurationMs,
		Failed: ev.Failed, ErrorKind: ev.ErrorKind, Reason: ev.Reason,
	}
}

// trackChild shows an OpenCode child session (a subagent) among its parent's
// running subagents, like Claude Code's: listed from its creation while it
// works or waits for you, dropped once it goes idle, fails or ends, and
// counted once, when the link is first seen. The child keeps its own row,
// so a child waiting for you still shows as waiting. ev is the event as
// Apply resolved it.
func (s *Store) trackChild(child *Session, ev Event) {
	created := false
	if ev.ParentID != "" && child.ParentKey == "" {
		child.ParentKey = ev.Agent + "/" + ev.Host + "/" + ev.ParentID
		created = true
	}
	if child.ParentKey == "" {
		return
	}
	if ev.Subagent != "" {
		child.SubagentType = ev.Subagent
	}
	parent := s.sessions[child.ParentKey]
	if parent == nil || parent == child {
		return
	}
	if created {
		parent.SubagentRuns++
	}
	i := slices.IndexFunc(parent.Subagents, func(a Subagent) bool { return a.ID == child.SessionID })
	typ := firstNonEmpty(child.SubagentType, child.Task)
	switch {
	// session.created leaves the child idle, but the subagent tool prompts it
	// right away. A link first seen on a later event (the server missed the
	// creation) only lists a child that is busy. A repeated session.created
	// must not drop the child it listed.
	case ev.Kind == "session.created" && (created || i >= 0),
		child.State == StateWorking, child.State == StateWaiting:
		if i < 0 {
			parent.Subagents = append(parent.Subagents, Subagent{ID: child.SessionID, Since: ev.At})
			i = len(parent.Subagents) - 1
		}
		parent.Subagents[i].Type = typ
		parent.Subagents[i].LastSeen = ev.At
	case i < 0:
		return
	case ev.State == StateIdle, ev.State == StateError, ev.State == StateEnded:
		parent.Subagents = slices.Delete(parent.Subagents, i, i+1)
	default:
		parent.Subagents[i].LastSeen = ev.At
	}
	parent.Subagents = runningSubagents(parent.Subagents, ev.At)
	if over := len(parent.Subagents) - maxSubagents; over > 0 {
		parent.Subagents = append([]Subagent(nil), parent.Subagents[over:]...)
	}
}

// trackSubagent keeps the list of running subagents. Claude Code sends
// SubagentStart again when a subagent resumes; one already running is neither
// listed nor counted twice.
func (s *Session) trackSubagent(ev Event) {
	s.trackLaunch(ev)
	switch {
	case ev.State == StateEnded:
		s.Subagents = nil
		s.Launches = nil
		return
	case ev.SubagentStarted, ev.SubagentSeen && ev.SubagentID != "":
		i := -1
		if ev.SubagentID != "" {
			i = slices.IndexFunc(s.Subagents, func(a Subagent) bool { return a.ID == ev.SubagentID })
		}
		if i < 0 && ev.SubagentID != "" {
			s.SubagentRuns++
			a := Subagent{ID: ev.SubagentID, Type: ev.Subagent, Since: ev.At}
			a.Desc = s.takeLaunch(ev.SubagentID, ev.Subagent)
			s.Subagents = append(s.Subagents, a)
			i = len(s.Subagents) - 1
		}
		switch {
		case i >= 0:
			a := &s.Subagents[i]
			if ev.Subagent != "" {
				a.Type = ev.Subagent
			}
			a.LastSeen = ev.At
			if ev.SubagentDoing != "" {
				a.Doing = ev.SubagentDoing
				a.Calls++
			}
		default:
			s.SubagentRuns++ // without an id it can't be listed, only counted
		}
	case ev.SubagentStopped:
		s.Subagents = slices.DeleteFunc(s.Subagents, func(a Subagent) bool { return a.ID == ev.SubagentID })
	default:
		return
	}
	s.Subagents = runningSubagents(s.Subagents, ev.At)
	if over := len(s.Subagents) - maxSubagents; over > 0 {
		s.Subagents = append([]Subagent(nil), s.Subagents[over:]...)
	}
	if len(s.Subagents) == 0 {
		s.Subagents = nil
	}
}

// Launch is a subagent the main thread asked for, waiting to be matched to the
// subagent that then starts (its SubagentStart or first tool call).
type Launch struct {
	ID   string    `json:"id,omitempty"` // known once the Agent tool returned it
	Desc string    `json:"desc"`
	Type string    `json:"type,omitempty"`
	At   time.Time `json:"at"`
}

const (
	launchFor   = 10 * time.Minute // an unmatched launch is forgotten after this
	maxLaunches = 50
)

// trackLaunch remembers what each subagent is started for. The Agent tool call
// names the task; its result names the new subagent's id, which then labels
// that subagent exactly. Until the id is known, subagents take descriptions in
// the order they were launched (same agent type first).
func (s *Session) trackLaunch(ev Event) {
	if ev.LaunchDesc == "" && ev.LaunchedID == "" {
		return
	}
	s.Launches = slices.DeleteFunc(s.Launches, func(l Launch) bool { return ev.At.Sub(l.At) > launchFor })
	if ev.LaunchedID != "" {
		if i := slices.IndexFunc(s.Subagents, func(a Subagent) bool { return a.ID == ev.LaunchedID }); i >= 0 {
			// Running already, maybe with a guessed description: put the right one
			// in, and hand the guessed one back to whoever else may need it.
			if old := s.Subagents[i].Desc; old != ev.LaunchDesc {
				s.Subagents[i].Desc = ev.LaunchDesc
				if j := slices.IndexFunc(s.Subagents, func(a Subagent) bool { return a.ID != ev.LaunchedID && a.Desc == ev.LaunchDesc }); j >= 0 {
					// Another one got this description by guess: it gets the one guessed here.
					s.Subagents[j].Desc = old
				} else {
					s.dropLaunch(ev.LaunchDesc)
					if old != "" {
						s.Launches = append(s.Launches, Launch{Desc: old, Type: s.Subagents[i].Type, At: ev.At})
					}
				}
			}
			return
		}
		// Not started yet: keep the launch, now with its id.
		for i := range s.Launches {
			if s.Launches[i].ID == "" && s.Launches[i].Desc == ev.LaunchDesc {
				s.Launches[i].ID = ev.LaunchedID
				return
			}
		}
		s.Launches = append(s.Launches, Launch{ID: ev.LaunchedID, Desc: ev.LaunchDesc, Type: ev.LaunchType, At: ev.At})
	} else {
		s.Launches = append(s.Launches, Launch{Desc: ev.LaunchDesc, Type: ev.LaunchType, At: ev.At})
	}
	if over := len(s.Launches) - maxLaunches; over > 0 {
		s.Launches = append([]Launch(nil), s.Launches[over:]...)
	}
}

// takeLaunch gives a newly seen subagent its description: the launch with its
// id, else the oldest launch of its type without an id, else the oldest one.
func (s *Session) takeLaunch(id, typ string) string {
	pick := slices.IndexFunc(s.Launches, func(l Launch) bool { return l.ID == id })
	if pick < 0 {
		pick = slices.IndexFunc(s.Launches, func(l Launch) bool { return l.ID == "" && typ != "" && l.Type == typ })
	}
	if pick < 0 {
		pick = slices.IndexFunc(s.Launches, func(l Launch) bool { return l.ID == "" })
	}
	if pick < 0 {
		return ""
	}
	d := s.Launches[pick].Desc
	s.Launches = slices.Delete(s.Launches, pick, pick+1)
	return d
}

func (s *Session) dropLaunch(desc string) {
	if i := slices.IndexFunc(s.Launches, func(l Launch) bool { return l.Desc == desc }); i >= 0 {
		s.Launches = slices.Delete(s.Launches, i, i+1)
	}
}

// runningSubagents drops subagents silent for more than subagentFor,
// so a stop that never arrived can't keep one listed forever. It returns a new
// slice; subs is not modified.
func runningSubagents(subs []Subagent, now time.Time) []Subagent {
	var out []Subagent
	for _, a := range subs {
		if now.Sub(a.active()) <= subagentFor {
			out = append(out, a)
		}
	}
	return out
}

// trackTodo counts distinct task-list items created and completed. An item
// completed without a seen creation counts as created too.
func (s *Session) trackTodo(ev Event) {
	if ev.TodoID == "" {
		return
	}
	done, seen := s.TodoIDs[ev.TodoID]
	if !seen {
		if len(s.TodoIDs) >= maxTodoIDs {
			// Too many ids to remember: count each event as it comes.
			if ev.TodoDone {
				s.TodosDone++
			} else {
				s.TodosTotal++
			}
			return
		}
		if s.TodoIDs == nil {
			s.TodoIDs = map[string]bool{}
		}
		s.TodoIDs[ev.TodoID] = false
		s.TodosTotal++
	}
	if ev.TodoDone && !done {
		s.TodoIDs[ev.TodoID] = true
		s.TodosDone++
	}
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
		c.Subagents = runningSubagents(c.Subagents, now)
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
	st := stateFile{Sessions: make([]*Session, 0, len(s.sessions)), HostNames: maps.Clone(s.names), Logged: maps.Clone(s.logged)}
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
