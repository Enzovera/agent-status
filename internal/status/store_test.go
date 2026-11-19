package status

import (
	"fmt"
	"os"
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

func TestStoreTimeline(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(at time.Duration, st State, detail string) {
		s.Apply(Event{Agent: "claude", Host: "pc", SessionID: "a", Kind: "k", State: st, Detail: detail, At: t0.Add(at)})
	}
	ev(0, "", "")                           // first event has no state: the session starts idle
	ev(time.Minute, StateWorking, "Read")   // change
	ev(2*time.Minute, StateWorking, "Bash") // same state: no new entry
	ev(3*time.Minute, StateWaiting, "zgoda")
	ev(4*time.Minute, "", "") // heartbeat
	got := s.List(t0.Add(5 * time.Minute))[0].Timeline
	want := []StateChange{{t0, StateIdle}, {t0.Add(time.Minute), StateWorking}, {t0.Add(3 * time.Minute), StateWaiting}}
	if len(got) != len(want) {
		t.Fatalf("timeline = %+v", got)
	}
	for i := range want {
		if !got[i].At.Equal(want[i].At) || got[i].State != want[i].State {
			t.Errorf("timeline[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A day later the old entries go, but the last one before the cutoff stays.
	ev(30*time.Hour, StateIdle, "")
	got = s.List(t0.Add(30 * time.Hour))[0].Timeline
	if len(got) != 2 || got[0].State != StateWaiting || got[1].State != StateIdle {
		t.Errorf("pruned timeline = %+v", got)
	}
}

func TestHostNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := NewStore(path)
	if err := s.SetHostName("192.168.1.5", "  Laptop   biuro "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHostName("pc", "pc"); err != nil {
		t.Fatal(err)
	}
	if got := s.HostNames(); len(got) != 1 || got["192.168.1.5"] != "Laptop biuro" {
		t.Fatalf("names = %v", got)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.HostNames()["192.168.1.5"]; got != "Laptop biuro" {
		t.Errorf("reloaded name = %q", got)
	}
	_ = s2.SetHostName("192.168.1.5", "")
	if got := s2.HostNames(); len(got) != 0 {
		t.Errorf("after reset = %v", got)
	}
}

func TestLegacyStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `[{"key":"claude/h/x","agent":"claude","host":"h","sessionId":"x","state":"idle","updatedAt":"2026-01-01T00:00:00Z"}]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if list := s.List(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)); len(list) != 1 || list[0].Host != "h" {
		t.Errorf("legacy sessions = %+v", list)
	}
}

// A question asked in the desktop app's closing box keeps the session waiting
// after Stop, through the idle reminder, until the next prompt.
func TestStoreQuestionAtTurnEnd(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	feed := func(i int, body string) {
		ev, err := ParseClaude("pc1", []byte(body), t0.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		s.Apply(ev)
	}
	state := func() (State, string) {
		l := s.List(t0.Add(time.Hour))
		return l[0].State, l[0].Detail
	}

	feed(0, `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"napraw"}`)
	feed(1, `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"mcp__ccd_turn__wrap_up","tool_input":{"recap":"r","ask":"Naprawić teraz?"}}`)
	if st, _ := state(); st != StateWorking {
		t.Fatalf("before Stop: %q", st)
	}
	feed(2, `{"session_id":"s1","hook_event_name":"Stop","last_assistant_message":"Znalazłem błąd."}`)
	if st, d := state(); st != StateWaiting || d != "decyzja: Naprawić teraz?" {
		t.Fatalf("after Stop: %q %q", st, d)
	}
	feed(3, `{"session_id":"s1","hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	if st, _ := state(); st != StateWaiting {
		t.Fatalf("after idle reminder: %q", st)
	}
	feed(4, `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"tak"}`)
	feed(5, `{"session_id":"s1","hook_event_name":"Stop","last_assistant_message":"Gotowe."}`)
	if st, d := state(); st != StateIdle || d != "Gotowe." {
		t.Fatalf("next turn: %q %q", st, d)
	}
}

// Recorded from Claude Code: a question also fires PermissionRequest and a
// permission_prompt notification. The page keeps showing the question.
func TestStoreQuestionNotPermission(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	for i, body := range []string{
		`{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Którą bazę?"}]}}`,
		`{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Którą bazę?"}]}}`,
		`{"session_id":"s1","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use AskUserQuestion"}`,
		`{"session_id":"s2","hook_event_name":"PermissionRequest","tool_name":"Edit","tool_input":{"file_path":"/a/b.go"}}`,
		`{"session_id":"s2","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Edit"}`,
		`{"session_id":"s3","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`,
	} {
		ev, err := ParseClaude("pc1", []byte(body), t0.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		s.Apply(ev)
	}
	want := map[string]struct {
		detail string
		perms  int
	}{
		"s1": {"pytanie: Którą bazę?", 0},
		"s2": {"zgoda: Edit: /a/b.go", 1},
		"s3": {"Claude needs your permission to use Bash", 0},
	}
	for _, sess := range s.List(t0.Add(time.Minute)) {
		w := want[sess.SessionID]
		if sess.State != StateWaiting || sess.Detail != w.detail || sess.Permissions != w.perms {
			t.Errorf("%s: state=%q detail=%q permissions=%d, want waiting %q %d", sess.SessionID, sess.State, sess.Detail, sess.Permissions, w.detail, w.perms)
		}
	}
}

func TestStoreSubagents(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(min int, e Event) {
		e.Agent, e.Host, e.SessionID, e.At = "claude", "pc1", "a", t0.Add(time.Duration(min)*time.Minute)
		s.Apply(e)
	}
	start := func(id, typ string) Event {
		return Event{Kind: "SubagentStart", Subagent: typ, SubagentID: id, SubagentStarted: true, Detail: "podagent: " + typ}
	}
	stop := func(id string) Event { return Event{Kind: "SubagentStop", SubagentID: id, SubagentStopped: true} }

	ev(0, Event{Kind: "UserPromptSubmit", State: StateWorking, Prompt: "x"})
	ev(1, start("a1", "Explore"))
	ev(2, start("a2", "Plan"))
	ev(3, start("a1", "Explore")) // resumed: neither listed nor counted twice
	ev(4, stop("unknown"))        // internal agents stop without a start
	sess := s.List(t0.Add(5 * time.Minute))[0]
	if sess.SubagentRuns != 2 || len(sess.Subagents) != 2 ||
		sess.Subagents[0] != (Subagent{ID: "a1", Type: "Explore", Since: t0.Add(time.Minute), LastSeen: t0.Add(3 * time.Minute)}) {
		t.Fatalf("runs=%d running=%+v", sess.SubagentRuns, sess.Subagents)
	}
	if sess.State != StateWorking || sess.Detail != "podagent: Explore" {
		t.Errorf("state=%q detail=%q", sess.State, sess.Detail)
	}

	// The returned copy must not share memory with the store.
	sess.Subagents[0].Type = "x"
	if s.List(t0.Add(5 * time.Minute))[0].Subagents[0].Type != "Explore" {
		t.Error("List leaks subagents")
	}

	ev(6, stop("a1"))
	sess = s.List(t0.Add(7 * time.Minute))[0]
	if len(sess.Subagents) != 1 || sess.Subagents[0].ID != "a2" || sess.SubagentRuns != 2 {
		t.Errorf("after stop: runs=%d running=%+v", sess.SubagentRuns, sess.Subagents)
	}

	// A stop that never arrives stops mattering after subagentFor.
	if got := s.List(t0.Add(2*time.Minute + subagentFor + time.Second))[0].Subagents; len(got) != 0 {
		t.Errorf("expired subagent still listed: %+v", got)
	}

	ev(8, start("a3", "general-purpose"))
	ev(9, Event{Kind: "SessionEnd", State: StateEnded})
	sess = s.List(t0.Add(10 * time.Minute))[0]
	if len(sess.Subagents) != 0 || sess.SubagentRuns != 3 {
		t.Errorf("after end: runs=%d running=%+v", sess.SubagentRuns, sess.Subagents)
	}
}

func TestStoreTodos(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(e Event) {
		e.Agent, e.Host, e.SessionID, e.At = "claude", "pc1", "a", t0
		s.Apply(e)
	}
	created := func(id string) Event { return Event{Kind: "TaskCreated", TodoID: id, Todo: "t" + id} }
	done := func(id string) Event {
		return Event{Kind: "TaskCompleted", TodoID: id, Todo: "t" + id, TodoDone: true, Detail: "zrobione: t" + id}
	}
	ev(created("1"))
	ev(created("2"))
	ev(created("1")) // repeat
	ev(done("1"))
	ev(done("1")) // repeat
	ev(done("3")) // creation not seen: counts as created too
	sess := s.List(t0)[0]
	if sess.TodosTotal != 3 || sess.TodosDone != 2 {
		t.Errorf("todos %d/%d, want 2 of 3", sess.TodosDone, sess.TodosTotal)
	}
	sess.TodoIDs["2"] = true
	if s.List(t0)[0].TodoIDs["2"] {
		t.Error("List leaks todo ids")
	}
}

func TestStoreCountersAndQuietEvents(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(sec int, e Event) Record {
		e.Agent, e.Host, e.SessionID, e.At = "claude", "pc1", "a", t0.Add(time.Duration(sec)*time.Second)
		rec, _ := s.Apply(e)
		return rec
	}
	ev(0, Event{Kind: "UserPromptSubmit", State: StateWorking, Prompt: "x"})
	ev(1, Event{Kind: "PreToolUse", State: StateWorking, Detail: "Bash: go test", Tool: "Bash"})
	before := s.List(t0.Add(time.Minute))[0]

	// PostToolUse comes after every tool call: it must not touch what is shown.
	rec := ev(2, Event{Kind: "PostToolUse", Reason: "Bash", DurationMs: 812})
	sess := s.List(t0.Add(time.Minute))[0]
	if sess.Detail != "Bash: go test" || len(sess.History) != len(before.History) || sess.ToolCalls != 1 || sess.State != StateWorking {
		t.Errorf("PostToolUse changed the session: %+v", sess)
	}
	if rec.Kind != "PostToolUse" || rec.Reason != "Bash" || rec.DurationMs != 812 || rec.Tool != "" || rec.State != StateWorking || rec.Prev != StateWorking {
		t.Errorf("PostToolUse record: %+v", rec)
	}

	rec = ev(3, Event{Kind: "PostToolUseFailure", Reason: "Bash", DurationMs: 40, Failed: true, Detail: "błąd narzędzia: Bash: Exit code 1"})
	if !rec.Failed || rec.DurationMs != 40 || rec.Reason != "Bash" {
		t.Errorf("failure record: %+v", rec)
	}
	ev(4, Event{Kind: "PostToolUseFailure", Reason: "Bash", Failed: true, ErrorKind: "interrupt", Detail: "przerwane: Bash"})
	ev(5, Event{Kind: "PreCompact", Reason: "auto", Detail: "kompaktuje kontekst (auto)"})
	rec = ev(6, Event{Kind: "PostCompact", Reason: "auto", Detail: "kontekst skompaktowany", Compacted: true})
	if rec.Reason != "auto" || rec.Detail != "kontekst skompaktowany" {
		t.Errorf("compaction record: %+v", rec)
	}
	ev(7, Event{Kind: "PostCompact", Reason: "manual", Detail: "kontekst skompaktowany", Compacted: true})
	rec = ev(8, Event{Kind: "SubagentStart", Subagent: "Explore", SubagentID: "x1", SubagentStarted: true, Detail: "podagent: Explore"})
	if rec.Subagent != "Explore" {
		t.Errorf("subagent record: %+v", rec)
	}
	rec = ev(9, Event{Kind: "TaskCompleted", TodoID: "7", Todo: "Auth", TodoDone: true, Detail: "zrobione: Auth"})
	if rec.TodoID != "7" || rec.Todo != "Auth" {
		t.Errorf("todo record: %+v", rec)
	}

	sess = s.List(t0.Add(time.Minute))[0]
	if sess.ToolFailures != 2 || sess.Compactions != 2 || sess.State != StateWorking || sess.Detail != "zrobione: Auth" {
		t.Errorf("failures=%d compactions=%d state=%q detail=%q", sess.ToolFailures, sess.Compactions, sess.State, sess.Detail)
	}

	rec = ev(10, Event{Kind: "StopFailure", State: StateError, ErrorKind: "rate_limit", Detail: "błąd: limit zapytań"})
	if rec.ErrorKind != "rate_limit" || rec.State != StateError || rec.Prev != StateWorking {
		t.Errorf("stop failure record: %+v", rec)
	}
	rec = ev(11, Event{Kind: "SessionEnd", State: StateEnded, Reason: "other"})
	if rec.Reason != "other" {
		t.Errorf("session end record: %+v", rec)
	}
}

func TestStoreSideNotesKeepQuestion(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	ev := func(e Event) {
		e.Agent, e.Host, e.SessionID, e.At = "claude", "pc1", "a", t0
		s.Apply(e)
	}
	ev(Event{Kind: "PermissionRequest", State: StateWaiting, Detail: "zgoda: Bash: rm", Permission: true})
	ev(Event{Kind: "SubagentStart", Subagent: "Explore", SubagentID: "x", SubagentStarted: true, Detail: "podagent: Explore"})
	sess := s.List(t0)[0]
	if sess.State != StateWaiting || sess.Detail != "zgoda: Bash: rm" {
		t.Errorf("state=%q detail=%q", sess.State, sess.Detail)
	}
	if h := sess.History[len(sess.History)-1]; h.Detail != "podagent: Explore" {
		t.Errorf("side note missing from history: %+v", h)
	}
}

func TestStoreNewCountersPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	t0 := time.Now()
	s, _ := NewStore(path)
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", Kind: "SubagentStart", Subagent: "Plan", SubagentID: "p1", SubagentStarted: true, At: t0})
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", Kind: "TaskCreated", TodoID: "1", At: t0})
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", Kind: "PostCompact", Compacted: true, At: t0})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, k := range []string{`"subagents"`, `"subagentRuns": 1`, `"compactions": 1`, `"toolFailures": 0`, `"todosTotal": 1`, `"todosDone": 0`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("state file lacks %s", k)
		}
	}
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.Apply(Event{Agent: "claude", Host: "h", SessionID: "x", Kind: "TaskCreated", TodoID: "1", At: t0})
	got := s2.List(t0)[0]
	if got.SubagentRuns != 1 || len(got.Subagents) != 1 || got.Compactions != 1 || got.TodosTotal != 1 {
		t.Errorf("after reload: %+v", got)
	}
}

// Claude Code sends nothing when you approve a permission; the end of the same
// tool call is what ends the wait. A background subagent's tool must not.
func TestPermissionWaitEndsWithItsTool(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	post := func(body string, at time.Time) {
		ev, err := ParseClaude("pc", []byte(body), at)
		if err != nil {
			t.Fatal(err)
		}
		s.Apply(ev)
	}
	state := func() Session { return s.List(t0.Add(time.Hour))[0] }

	post(`{"session_id":"a","hook_event_name":"UserPromptSubmit","prompt":"x"}`, t0)
	post(`{"session_id":"a","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"npm test","description":"Run tests"}}`, t0.Add(time.Second))
	if st := state(); st.State != StateWaiting || st.Detail != "zgoda: Bash: npm test" {
		t.Fatalf("after permission request: %s %q", st.State, st.Detail)
	}
	// A subagent's Bash and the main thread's Read finish meanwhile: still waiting.
	post(`{"session_id":"a","hook_event_name":"PostToolUse","tool_name":"Bash","agent_id":"sub1","duration_ms":5}`, t0.Add(2*time.Second))
	post(`{"session_id":"a","hook_event_name":"PostToolUse","tool_name":"Read","duration_ms":5}`, t0.Add(3*time.Second))
	post(`{"session_id":"a","hook_event_name":"PreToolUse","tool_name":"Grep","agent_id":"sub1","tool_input":{"pattern":"x"}}`, t0.Add(4*time.Second))
	if st := state(); st.State != StateWaiting {
		t.Fatalf("other tools must not end the wait: %s", st.State)
	}
	post(`{"session_id":"a","hook_event_name":"PostToolUse","tool_name":"Bash","duration_ms":2000}`, t0.Add(10*time.Second))
	if st := state(); st.State != StateWorking || st.WaitFor != "" {
		t.Fatalf("approved tool finished: %s %q", st.State, st.WaitFor)
	}

	// A question asked with AskUserQuestion ends when the tool returns the answer.
	post(`{"session_id":"a","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"A czy B?"}]}}`, t0.Add(20*time.Second))
	if st := state(); st.State != StateWaiting {
		t.Fatalf("question: %s", st.State)
	}
	post(`{"session_id":"a","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","duration_ms":9000}`, t0.Add(30*time.Second))
	if st := state(); st.State != StateWorking {
		t.Fatalf("answered question: %s", st.State)
	}
}

func TestRecordWorth(t *testing.T) {
	if (Record{Kind: "session.reasoning.delta", State: StateWorking, Prev: StateWorking}).Worth() {
		t.Error("a no-op event should not be archived")
	}
	for _, r := range []Record{
		{Kind: "x", State: StateIdle, Prev: StateWorking},
		{Kind: "PreToolUse", State: StateWorking, Prev: StateWorking, Tool: "Bash"},
		{Kind: "PostToolUse", State: StateWorking, Prev: StateWorking, Reason: "Bash", DurationMs: 5},
	} {
		if !r.Worth() {
			t.Errorf("should be archived: %+v", r)
		}
	}
}

// OpenCode runs a subagent as a child session: session.created names the
// parent, the plugin repeats parentID on the child's later events.
func TestOpenCodeChildSession(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	n := 0
	send := func(typ, data string) {
		t.Helper()
		n++
		ev, err := ParseOpenCode("ip", []byte(`{"host":"mac","directory":"/src/app","type":"`+typ+`","data":`+data+`}`), t0.Add(time.Duration(n)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		s.Apply(ev)
	}
	get := func(id string) Session {
		t.Helper()
		for _, x := range s.List(t0.Add(time.Duration(n) * time.Second)) {
			if x.SessionID == id {
				return x
			}
		}
		t.Fatalf("no session %q", id)
		return Session{}
	}

	send("session.execution.started", `{"sessionID":"P"}`)
	send("session.tool.called", `{"sessionID":"P","id":"t1","name":"subagent","input":{"description":"Find handlers"}}`)
	send("session.created", `{"sessionID":"C","parentID":"P","title":"Find handlers","agent":"explore"}`)
	send("session.created", `{"sessionID":"C","parentID":"P","title":"Find handlers","agent":"explore"}`) // repeated delivery

	c := get("C")
	if c.ParentKey != "opencode/mac/P" || c.SubagentType != "explore" || c.Task != "Find handlers" {
		t.Errorf("child = %+v", c)
	}
	p := get("P")
	if p.SubagentRuns != 1 || len(p.Subagents) != 1 || p.Subagents[0].ID != "C" || p.Subagents[0].Type != "explore" {
		t.Fatalf("parent after create: runs %d, subagents %+v", p.SubagentRuns, p.Subagents)
	}
	if p.ParentKey != "" {
		t.Errorf("parent has a parent: %q", p.ParentKey)
	}
	since := p.Subagents[0].Since

	send("session.execution.started", `{"sessionID":"C","parentID":"P"}`)
	send("session.tool.called", `{"sessionID":"C","parentID":"P","id":"t2","name":"grep","input":{"pattern":"auth"}}`)
	// The parent goes idle while its subagent keeps working in the background.
	send("session.execution.succeeded", `{"sessionID":"P"}`)
	send("permission.asked", `{"sessionID":"C","parentID":"P","action":"bash","resources":["ls"]}`)
	c, p = get("C"), get("P")
	if c.State != StateWaiting {
		t.Errorf("waiting child state = %s", c.State)
	}
	if p.State != StateIdle || len(p.Subagents) != 1 || !p.Subagents[0].Since.Equal(since) || !p.Subagents[0].LastSeen.After(since) {
		t.Errorf("parent while child waits: state %s, subagents %+v", p.State, p.Subagents)
	}

	send("permission.replied", `{"sessionID":"C","parentID":"P","reply":"once"}`)
	send("session.execution.succeeded", `{"sessionID":"C","parentID":"P"}`)
	if p = get("P"); len(p.Subagents) != 0 || p.SubagentRuns != 1 {
		t.Errorf("after child finished: runs %d, subagents %+v", p.SubagentRuns, p.Subagents)
	}

	// A follow-up to the same child lists it again but is not a new run.
	send("session.execution.started", `{"sessionID":"C","parentID":"P"}`)
	if p = get("P"); len(p.Subagents) != 1 || p.SubagentRuns != 1 {
		t.Errorf("follow-up: runs %d, subagents %+v", p.SubagentRuns, p.Subagents)
	}
	send("session.execution.failed", `{"sessionID":"C","parentID":"P","error":{"message":"boom"}}`)
	if p = get("P"); len(p.Subagents) != 0 {
		t.Errorf("failed child still listed: %+v", p.Subagents)
	}

	// Without the parentID repeated (an older plugin), the stored link still works.
	send("session.created", `{"sessionID":"D","parentID":"P","title":"Review","agent":"general"}`)
	send("session.execution.started", `{"sessionID":"D"}`)
	if p = get("P"); p.SubagentRuns != 2 || len(p.Subagents) != 1 || p.Subagents[0].ID != "D" {
		t.Errorf("second child: runs %d, subagents %+v", p.SubagentRuns, p.Subagents)
	}
	send("session.deleted", `{"sessionID":"D"}`)
	if p = get("P"); len(p.Subagents) != 0 {
		t.Errorf("deleted child still listed: %+v", p.Subagents)
	}

	// A link first seen on an idle event (the creation was missed) counts the
	// run but lists nothing; an unknown parent is left alone.
	send("session.idle", `{"sessionID":"E","parentID":"P"}`)
	send("session.execution.started", `{"sessionID":"F","parentID":"nobody"}`)
	if p = get("P"); p.SubagentRuns != 3 || len(p.Subagents) != 0 {
		t.Errorf("late link: runs %d, subagents %+v", p.SubagentRuns, p.Subagents)
	}
	if f := get("F"); f.ParentKey != "opencode/mac/nobody" || f.State != StateWorking {
		t.Errorf("orphan child = %+v", f)
	}
}

// Claude Code sessions never get a parent; their subagents stay in the list.
func TestClaudeHasNoParentKey(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	s.Apply(Event{Agent: "claude", Host: "pc", SessionID: "a", Kind: "SubagentStart", Subagent: "Explore", SubagentID: "x", SubagentStarted: true, At: t0})
	got := s.List(t0)[0]
	if got.ParentKey != "" || got.SubagentType != "" || len(got.Subagents) != 1 {
		t.Errorf("claude session = %+v", got)
	}
}

func claudePost(t *testing.T, s *Store, t0 time.Time) func(body string, sec int) {
	return func(body string, sec int) {
		t.Helper()
		ev, err := ParseClaude("mac", []byte(body), t0.Add(time.Duration(sec)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		s.Apply(ev)
	}
}

func sessionsByKey(l []Session) map[string]Session {
	m := map[string]Session{}
	for _, x := range l {
		m[x.Key] = x
	}
	return m
}

// A Claude Code subagent is a session of its own under the main one: its tool
// calls, permission prompts and end change only its own row, never the main
// session's state or caption.
func TestClaudeSubagentSessions(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	post := claudePost(t, s, t0)
	post(`{"session_id":"m","cwd":"/src/app","hook_event_name":"UserPromptSubmit","prompt":"przejrzyj kod"}`, 0)
	post(`{"session_id":"m","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"description":"Find API uses","subagent_type":"Explore"}}`, 1)
	post(`{"session_id":"m","cwd":"/src/app","hook_event_name":"SubagentStart","agent_id":"ag1","agent_type":"Explore"}`, 2)
	post(`{"session_id":"m","hook_event_name":"Stop","last_assistant_message":"Uruchomiłem podagenta."}`, 3)
	post(`{"session_id":"m","cwd":"/src/app","hook_event_name":"PreToolUse","tool_name":"Grep","agent_id":"ag1","agent_type":"Explore","tool_input":{"pattern":"api"}}`, 4)
	post(`{"session_id":"m","cwd":"/src/app","hook_event_name":"PermissionRequest","tool_name":"Bash","agent_id":"ag1","agent_type":"Explore","tool_input":{"command":"go test ./..."}}`, 5)
	// The generic notification may lack the subagent's id: it must not make the main session wait.
	post(`{"session_id":"m","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`, 6)

	m := sessionsByKey(s.List(t0.Add(time.Minute)))
	main, child := m["claude/mac/m"], m["claude/mac/ag1"]
	if main.State != StateIdle || main.Detail != "Uruchomiłem podagenta." {
		t.Errorf("main: %s %q", main.State, main.Detail)
	}
	if child.ParentKey != "claude/mac/m" || child.SubagentType != "Explore" || child.Task != "Find API uses" {
		t.Errorf("child identity: parent %q type %q task %q", child.ParentKey, child.SubagentType, child.Task)
	}
	if child.State != StateWaiting || child.Detail != "zgoda: Bash: go test ./..." || child.ToolCalls != 1 {
		t.Errorf("child: %s %q tools %d", child.State, child.Detail, child.ToolCalls)
	}
	if len(main.Subagents) != 1 || main.SubagentRuns != 1 || main.ToolCalls != 1 {
		t.Errorf("main lists %+v, runs %d, tools %d", main.Subagents, main.SubagentRuns, main.ToolCalls)
	}

	// Approved: the tool finishes, the subagent works on, then stops.
	post(`{"session_id":"m","hook_event_name":"PostToolUse","tool_name":"Bash","agent_id":"ag1","agent_type":"Explore","duration_ms":800}`, 20)
	if c := sessionsByKey(s.List(t0.Add(time.Minute)))["claude/mac/ag1"]; c.State != StateWorking {
		t.Errorf("after approval: %s", c.State)
	}
	post(`{"session_id":"m","hook_event_name":"SubagentStop","agent_id":"ag1","agent_type":"Explore","last_assistant_message":"3 uses"}`, 30)
	// An internal agent that never started only stops: nothing appears.
	post(`{"session_id":"m","hook_event_name":"SubagentStop","agent_id":"internal9","agent_type":""}`, 31)
	m = sessionsByKey(s.List(t0.Add(time.Minute)))
	if c := m["claude/mac/ag1"]; c.State != StateEnded || c.Detail != "3 uses" {
		t.Errorf("stopped child: %s %q", c.State, c.Detail)
	}
	if _, ok := m["claude/mac/internal9"]; ok || len(m["claude/mac/m"].Subagents) != 0 {
		t.Errorf("after stop: %d sessions, main lists %+v", len(m), m["claude/mac/m"].Subagents)
	}
}

// Subagents started together take tasks in launch order until the Agent tool's
// result names the exact id; then titles swap. A subagent's report handed back
// as a prompt is not the user's prompt.
func TestClaudeSubagentTitles(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 23, 24, 0, 0, time.UTC)
	s, _ := NewStore("")
	post := claudePost(t, s, t0)
	post(`{"session_id":"m","hook_event_name":"UserPromptSubmit","prompt":"zrob review PR"}`, 0)
	post(`{"session_id":"m","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"description":"Review PR 84","subagent_type":"general-purpose"}}`, 1)
	post(`{"session_id":"m","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"description":"Review PR 85","subagent_type":"general-purpose"}}`, 1)
	post(`{"session_id":"m","hook_event_name":"SubagentStart","agent_id":"bbbbbbbbbbbbbbbb2","agent_type":"general-purpose"}`, 2)
	post(`{"session_id":"m","hook_event_name":"SubagentStart","agent_id":"aaaaaaaaaaaaaaaa1","agent_type":"general-purpose"}`, 2)
	post(`{"session_id":"m","hook_event_name":"PostToolUse","tool_name":"Agent","tool_input":{"description":"Review PR 84","subagent_type":"general-purpose"},"tool_response":"Async agent launched successfully.\nagentId: aaaaaaaaaaaaaaaa1 (internal ID)"}`, 3)
	post(`{"session_id":"m","hook_event_name":"UserPromptSubmit","prompt":"<agent-message from=\"b\"> report"}`, 5)

	m := sessionsByKey(s.List(t0.Add(time.Minute)))
	if a, b := m["claude/mac/aaaaaaaaaaaaaaaa1"].Task, m["claude/mac/bbbbbbbbbbbbbbbb2"].Task; a != "Review PR 84" || b != "Review PR 85" {
		t.Errorf("titles: a %q, b %q", a, b)
	}
	main := m["claude/mac/m"]
	if len(main.Launches) != 0 || main.Task != "zrob review PR" || main.Prompts != 1 {
		t.Errorf("main: launches %+v task %q prompts %d", main.Launches, main.Task, main.Prompts)
	}
}
