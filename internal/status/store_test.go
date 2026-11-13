package status

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStoreLifecycle(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")

	s.Apply(Event{Agent: "claude", Host: "pc1", SessionID: "a", Cwd: `C:\code\forma\`, Kind: "UserPromptSubmit", State: StateWorking, Task: "testy", At: t0})
	s.Apply(Event{Agent: "claude", Host: "pc1", SessionID: "a", Kind: "PreToolUse", State: StateWorking, Detail: "Bash", At: t0.Add(time.Second)})
	s.Apply(Event{Agent: "claude", Host: "pc1", SessionID: "a", Kind: "PostCompact", At: t0.Add(2 * time.Second)})
	s.Apply(Event{Agent: "opencode", Host: "mac", SessionID: "b", Cwd: "/src/app", State: StateWaiting, Detail: "zgoda", At: t0})
	s.Apply(Event{Agent: "claude", Host: "pc1", Kind: "x", State: StateError, At: t0}) // no session id: ignored

	list := s.List(t0.Add(time.Minute))
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].SessionID != "b" {
		t.Errorf("waiting session should come first, got %q", list[0].SessionID)
	}
	a := list[1]
	if a.Project != "forma" || a.Task != "testy" || a.Detail != "Bash" || a.State != StateWorking || a.LastEvent != "PostCompact" {
		t.Errorf("session a = %+v", a)
	}
	if !a.StartedAt.Equal(t0) {
		t.Errorf("startedAt = %v", a.StartedAt)
	}
}

func TestStoreStaleAndPrune(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		state     State
		age       time.Duration
		wantKept  bool
		wantStale bool
	}{
		{"fresh working", StateWorking, time.Minute, true, false},
		{"silent working", StateWorking, 11 * time.Minute, true, true},
		{"long waiting is not stale", StateWaiting, time.Hour, true, false},
		{"recently ended", StateEnded, time.Hour, true, false},
		{"old ended", StateEnded, 3 * time.Hour, false, false},
		{"silent for a day", StateIdle, 25 * time.Hour, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewStore("")
			s.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", State: tt.state, At: t0})
			list := s.List(t0.Add(tt.age))
			if (len(list) == 1) != tt.wantKept {
				t.Fatalf("kept = %v", len(list) == 1)
			}
			if tt.wantKept && list[0].Stale != tt.wantStale {
				t.Errorf("stale = %v", list[0].Stale)
			}
		})
	}
}

func TestStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	t0 := time.Now()
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", State: StateWorking, Task: "t", At: t0})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List(t0)
	if len(list) != 1 || list[0].Task != "t" || list[0].State != StateWorking {
		t.Errorf("reloaded = %+v", list)
	}
}

func TestProjectName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\User\Documents\forma`: "forma",
		"/home/p/app/":                  "app",
		"app":                           "app",
		"/":                             "/",
	} {
		if got := projectName(in); got != want {
			t.Errorf("projectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreDetails(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(sec int, e Event) {
		e.Agent, e.Host, e.SessionID, e.At = "claude", "pc1", "a", t0.Add(time.Duration(sec)*time.Second)
		s.Apply(e)
	}
	ev(0, Event{Kind: "SessionStart", State: StateIdle, Model: "opus", Mode: "default"})
	ev(1, Event{Kind: "UserPromptSubmit", State: StateWorking, Prompt: "napraw\ntesty"})
	ev(2, Event{Kind: "PreToolUse", State: StateWorking, Detail: "Bash: go test", Tool: "Bash"})
	ev(3, Event{Kind: "PreToolUse", State: StateWorking, Detail: "Bash: go test", Tool: "Bash"}) // repeat
	ev(4, Event{Kind: "PreToolUse", State: StateWorking, Detail: "Read: a.go", Tool: "Read"})
	ev(5, Event{Kind: "PermissionRequest", State: StateWaiting, Detail: "zgoda: Edit", Permission: true})
	ev(6, Event{Kind: "PostCompact"}) // nothing visible: not in history
	ev(7, Event{Kind: "Stop", State: StateIdle, Detail: "Gotowe.", Message: "Gotowe.\nWszystko działa."})

	got := s.List(t0.Add(time.Minute))[0]
	if got.Model != "opus" || got.Mode != "default" || got.LastPrompt != "napraw\ntesty" || got.LastMessage != "Gotowe.\nWszystko działa." {
		t.Errorf("texts: %+v", got)
	}
	if got.Prompts != 1 || got.ToolCalls != 3 || got.Permissions != 1 || got.Tools["Bash"] != 2 || got.Tools["Read"] != 1 {
		t.Errorf("counters: prompts=%d tools=%d perms=%d %v", got.Prompts, got.ToolCalls, got.Permissions, got.Tools)
	}
	if len(got.History) != 6 {
		t.Fatalf("history len = %d: %+v", len(got.History), got.History)
	}
	if h := got.History[2]; h.Detail != "Bash: go test" || !h.At.Equal(t0.Add(3*time.Second)) {
		t.Errorf("repeated event should refresh time: %+v", h)
	}

	// The returned copy must not share memory with the store.
	got.Tools["Bash"] = 99
	got.History[0].Kind = "x"
	again := s.List(t0.Add(time.Minute))[0]
	if again.Tools["Bash"] != 2 || again.History[0].Kind != "SessionStart" {
		t.Error("List leaks internal state")
	}
}

func TestStoreHistoryCap(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	for i := range historyLen + 10 {
		s.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", Kind: "PreToolUse", State: StateWorking, Detail: fmt.Sprint(i), At: t0})
	}
	h := s.List(t0)[0].History
	if len(h) != historyLen || h[0].Detail != "10" || h[len(h)-1].Detail != fmt.Sprint(historyLen+9) {
		t.Errorf("len=%d first=%q last=%q", len(h), h[0].Detail, h[len(h)-1].Detail)
	}
}

func TestParseClaudeExtras(t *testing.T) {
	at := time.Now()
	long := strings.Repeat("ab\n", 1000)
	ev, _ := ParseClaude("h", []byte(`{"session_id":"s","hook_event_name":"UserPromptSubmit","permission_mode":"plan","prompt":`+strconv.Quote(long)+`}`), at)
	if ev.Mode != "plan" || len([]rune(ev.Prompt)) != maxLong || !strings.Contains(ev.Prompt, "\n") {
		t.Errorf("prompt: mode=%q len=%d", ev.Mode, len([]rune(ev.Prompt)))
	}
	ev, _ = ParseClaude("h", []byte(`{"session_id":"s","hook_event_name":"SessionStart","model":"claude-opus-5-5"}`), at)
	if ev.Model != "claude-opus-5-5" {
		t.Errorf("model string: %q", ev.Model)
	}
	ev, _ = ParseClaude("h", []byte(`{"session_id":"s","hook_event_name":"SessionStart","model":{"id":"x","display_name":"Opus"}}`), at)
	if ev.Model != "Opus" {
		t.Errorf("model object: %q", ev.Model)
	}
	ev, _ = ParseClaude("h", []byte(`{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"a"}}`), at)
	if ev.Tool != "Edit" {
		t.Errorf("tool: %q", ev.Tool)
	}
	ev, _ = ParseClaude("h", []byte(`{"session_id":"s","hook_event_name":"PermissionRequest","tool_name":"Edit"}`), at)
	if !ev.Permission || ev.Tool != "" {
		t.Errorf("permission: %+v", ev)
	}
}

func TestStoreIdleShowsLastReply(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(e Event) {
		e.Agent, e.Host, e.SessionID, e.At = "opencode", "mac", "o1", t0
		s.Apply(e)
	}
	ev(Event{Kind: "session.created", State: StateIdle})
	if d := s.List(t0)[0].Detail; d != "" {
		t.Errorf("no reply yet, detail = %q", d)
	}
	ev(Event{Kind: "session.execution.started", State: StateWorking})
	ev(Event{Kind: "session.text.ended", Detail: "Gotowe. Testy przechodzą.", Message: "Gotowe.\nTesty przechodzą."})
	ev(Event{Kind: "session.execution.succeeded", State: StateIdle})
	if d := s.List(t0)[0].Detail; d != "Gotowe. Testy przechodzą." {
		t.Errorf("idle detail = %q", d)
	}
	ev(Event{Kind: "session.execution.interrupted", State: StateIdle, Detail: "przerwane przez Ciebie"})
	if d := s.List(t0)[0].Detail; d != "przerwane przez Ciebie" {
		t.Errorf("own detail should win, got %q", d)
	}
}
