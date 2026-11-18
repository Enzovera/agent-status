package status

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is one applied event as kept in the archive: what happened and the
// state the session was in afterwards. Unlike the store, the archive keeps
// every record forever, so statistics can look back over weeks.
// Prompts and replies are clipped; full texts never reach the archive.
type Record struct {
	At        time.Time `json:"at"`
	Key       string    `json:"key"`
	Agent     string    `json:"agent"`
	Host      string    `json:"host"`
	SessionID string    `json:"session"`
	Project   string    `json:"project,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Kind      string    `json:"kind"`
	// State is the session's state after the event; Prev is the state before it
	// (equal to State when the event changed nothing).
	State  State  `json:"state"`
	Prev   State  `json:"prev,omitempty"`
	Title  string `json:"title,omitempty"` // session title / task
	Detail string `json:"detail,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Prompt string `json:"prompt,omitempty"` // first maxText runes of a user prompt
	Model  string `json:"model,omitempty"`
	Mode   string `json:"mode,omitempty"`

	Permission bool `json:"permission,omitempty"`

	// Extras some agents report; see Event.
	Subagent   string `json:"subagent,omitempty"`
	TodoID     string `json:"todoId,omitempty"`
	Todo       string `json:"todo,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Failed     bool   `json:"failed,omitempty"`
	ErrorKind  string `json:"errorKind,omitempty"`
	Reason     string `json:"reason,omitempty"`

	// From Claude Code's statusLine (Kind KindStatusline, State equal to Prev):
	// the session's cost so far, how much it grew since the session's previous
	// such record (sum CostDelta to get the cost of a period), and the context
	// window use.
	CostUsd       float64 `json:"costUsd,omitempty"`
	CostDelta     float64 `json:"costDelta,omitempty"`
	ContextPct    float64 `json:"contextPct,omitempty"`
	ContextTokens int     `json:"contextTokens,omitempty"`
	LinesAdded    int     `json:"linesAdded,omitempty"`
	LinesRemoved  int     `json:"linesRemoved,omitempty"`
}

// Worth reports whether the record says anything beyond "the session is
// alive": a state change, a text, a tool, a prompt or one of the extras.
// Clients that forward every event (streamed reasoning, usage updates) would
// otherwise fill the archive with lines statistics never use.
func (r Record) Worth() bool {
	return r.State != r.Prev || r.Detail != "" || r.Tool != "" || r.Prompt != "" || r.Permission ||
		r.Subagent != "" || r.TodoID != "" || r.DurationMs != 0 || r.Failed || r.ErrorKind != "" || r.Reason != "" ||
		r.Kind == "UserPromptSubmit" || r.Kind == "Stop" || r.Kind == "SessionEnd"
}

// Archive appends records to one JSON-lines file per month:
// <dir>/events-2026-11.jsonl (months in local time).
type Archive struct {
	mu    sync.Mutex
	dir   string
	month string
	f     *os.File
}

// OpenArchive creates dir if needed. Files are opened lazily on first write.
func OpenArchive(dir string) (*Archive, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Archive{dir: dir}, nil
}

func (a *Archive) Dir() string { return a.dir }

func monthOf(t time.Time) string { return t.Local().Format("2006-01") }

func archiveFile(dir, month string) string { return filepath.Join(dir, "events-"+month+".jsonl") }

// Write appends one record. Each record is written with a single write call,
// so a crash can at worst lose or truncate the last line.
func (a *Archive) Write(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	if m := monthOf(r.At); m != a.month || a.f == nil {
		if a.f != nil {
			_ = a.f.Close()
			a.f = nil
		}
		f, err := os.OpenFile(archiveFile(a.dir, m), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		a.f, a.month = f, m
	}
	_, err = a.f.Write(b)
	return err
}

func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

// ReadArchive calls fn for every record with from <= At < to, oldest file
// first (records inside a file are in arrival order). A zero to means "until
// now". Unreadable lines are skipped.
func ReadArchive(dir string, from, to time.Time, fn func(Record) error) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var months []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "events-") && strings.HasSuffix(n, ".jsonl") {
			months = append(months, strings.TrimSuffix(strings.TrimPrefix(n, "events-"), ".jsonl"))
		}
	}
	sort.Strings(months)
	// Files are named by local month; a neighbouring month's file may hold records
	// near the edge of the range, so only files clearly outside it are skipped.
	first := monthOf(from.AddDate(0, -1, 0))
	last := ""
	if !to.IsZero() {
		last = monthOf(to.AddDate(0, 1, 0))
	}
	for _, m := range months {
		if m < first || (last != "" && m > last) {
			continue
		}
		if err := readArchiveFile(archiveFile(dir, m), from, to, fn); err != nil {
			return err
		}
	}
	return nil
}

func readArchiveFile(path string, from, to time.Time, fn func(Record) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.At.Before(from) || (!to.IsZero() && !r.At.Before(to)) {
			continue
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return sc.Err()
}
