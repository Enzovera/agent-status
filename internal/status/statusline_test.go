package status

import (
	"path/filepath"
	"testing"
	"time"
)

// The full schema example from https://code.claude.com/docs/en/statusline (trimmed).
const statuslineDoc = `{
  "cwd": "/home/me/proj",
  "session_id": "abc123",
  "session_name": "my-session",
  "transcript_path": "/path/to/transcript.jsonl",
  "model": {"id": "claude-opus-5-5", "display_name": "Opus"},
  "workspace": {"current_dir": "/home/me/proj", "project_dir": "/home/me/proj", "added_dirs": []},
  "version": "2.1.90",
  "cost": {"total_cost_usd": 0.01234, "total_duration_ms": 45000, "total_api_duration_ms": 2300,
           "total_lines_added": 156, "total_lines_removed": 23},
  "context_window": {"total_input_tokens": 15500, "total_output_tokens": 1200, "context_window_size": 200000,
    "used_percentage": 8, "remaining_percentage": 92,
    "current_usage": {"input_tokens": 8500, "output_tokens": 1200, "cache_creation_input_tokens": 5000, "cache_read_input_tokens": 2000}},
  "exceeds_200k_tokens": false,
  "rate_limits": {"five_hour": {"used_percentage": 23.5, "resets_at": 1738425600}}
}`

func TestParseStatusline(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	m, err := ParseStatusline("pc", []byte(statuslineDoc), at)
	if err != nil {
		t.Fatal(err)
	}
	want := Statusline{
		Agent: "claude", Host: "pc", SessionID: "abc123", Cwd: "/home/me/proj", Model: "claude-opus-5-5",
		Title: "my-session", At: at, ContextPct: 8, ContextTokens: 15500, ContextLimit: 200000,
		CostUsd: 0.01234, LinesAdded: 156, LinesRemoved: 23,
	}
	if m != want {
		t.Fatalf("got %+v\nwant %+v", m, want)
	}

	// Early in a session: used_percentage and current_usage are null.
	m, err = ParseStatusline("pc", []byte(`{"session_id":"s","workspace":{"current_dir":"C:\\x\\y"},
		"context_window":{"total_input_tokens":0,"context_window_size":200000,"used_percentage":null,"current_usage":null},
		"cost":{"total_cost_usd":0}}`), at)
	if err != nil {
		t.Fatal(err)
	}
	if m.ContextPct != 0 || m.ContextTokens != 0 || m.Cwd != `C:\x\y` || m.CostUsd != 0 {
		t.Fatalf("early session: %+v", m)
	}

	// No used_percentage: computed from the input tokens of current_usage.
	m, _ = ParseStatusline("pc", []byte(`{"session_id":"s","context_window":{"context_window_size":1000,
		"current_usage":{"input_tokens":100,"output_tokens":999,"cache_creation_input_tokens":50,"cache_read_input_tokens":100}}}`), at)
	if m.ContextPct != 25 || m.ContextTokens != 250 || m.ContextLimit != 1000 {
		t.Fatalf("computed pct: %+v", m)
	}

	if _, err := ParseStatusline("pc", []byte(`{"cwd":"/x"}`), at); err == nil {
		t.Fatal("want error without session_id")
	}
	if _, err := ParseStatusline("pc", []byte(`not json`), at); err == nil {
		t.Fatal("want error for bad JSON")
	}
}

func TestApplyStatuslineKeepsState(t *testing.T) {
	st, _ := NewStore("")
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	st.Apply(Event{Agent: "claude", Host: "pc", SessionID: "s1", Cwd: "/p/app", Kind: "UserPromptSubmit",
		State: StateWorking, Detail: "myśli", Prompt: "zrób to", At: t0})

	t1 := t0.Add(20 * time.Minute) // later than staleAfter
	rec, ok := st.ApplyStatusline(Statusline{Agent: "claude", Host: "pc", SessionID: "s1", Cwd: "/p/app",
		Model: "claude-opus-5-5", At: t1, ContextPct: 82.5, ContextTokens: 165000, ContextLimit: 200000,
		CostUsd: 1.5, LinesAdded: 10, LinesRemoved: 2})
	if !ok {
		t.Fatal("first metrics should be archived")
	}
	if rec.Kind != KindStatusline || rec.State != StateWorking || rec.Prev != StateWorking ||
		rec.CostUsd != 1.5 || rec.CostDelta != 1.5 || rec.ContextPct != 82.5 || rec.Project != "app" {
		t.Fatalf("record: %+v", rec)
	}

	ss := st.List(t1)
	if len(ss) != 1 {
		t.Fatalf("sessions: %d", len(ss))
	}
	s := ss[0]
	if s.State != StateWorking || s.Detail != "myśli" || !s.UpdatedAt.Equal(t0) || !s.Stale || len(s.History) != 1 {
		t.Fatalf("statusline changed the session's state: %+v", s)
	}
	if s.ContextPct != 82.5 || s.ContextTokens != 165000 || s.ContextLimit != 200000 || s.CostUsd != 1.5 ||
		s.LinesAdded != 10 || s.LinesRemoved != 2 || s.Model != "claude-opus-5-5" || s.MetricsAt == nil || !s.MetricsAt.Equal(t1) {
		t.Fatalf("metrics: %+v", s)
	}
}

func TestApplyStatuslineArchiveSteps(t *testing.T) {
	st, _ := NewStore("")
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	m := Statusline{Agent: "claude", Host: "pc", SessionID: "s1", Cwd: "/p/app", At: t0}

	// Nothing spent yet: nothing to archive, but the session shows up.
	if _, ok := st.ApplyStatusline(m); ok {
		t.Fatal("zero metrics archived")
	}
	if ss := st.List(t0); len(ss) != 1 || ss[0].State != StateIdle || ss[0].Project != "app" {
		t.Fatalf("new session from statusline: %+v", ss)
	}

	step := func(cost, pct float64, want bool, wantDelta float64) {
		t.Helper()
		m.At = m.At.Add(time.Second)
		m.CostUsd, m.ContextPct = cost, pct
		rec, ok := st.ApplyStatusline(m)
		if ok != want {
			t.Fatalf("cost %v pct %v: archived=%v, want %v", cost, pct, ok, want)
		}
		if ok && rec.CostDelta != wantDelta {
			t.Fatalf("cost %v: delta %v, want %v", cost, rec.CostDelta, wantDelta)
		}
	}
	step(0.05, 10, true, 0.05)   // first metrics
	step(0.055, 10.5, false, 0)  // below both steps
	step(0.059, 11, true, 0.009) // context moved 1 point: delta since the last archived
	step(0.069, 11, true, 0.01)  // a cent more
	step(0.07, 11.2, false, 0)
	step(0.02, 11.2, true, 0.02) // counter restarted: all of it is new
	step(0.03, 5, true, 0.01)    // context dropped (compaction)

	// A session the server doesn't know and without a directory is ignored.
	if _, ok := st.ApplyStatusline(Statusline{Agent: "claude", Host: "pc", SessionID: "nocwd", At: t0, CostUsd: 1}); ok {
		t.Fatal("archived a session without cwd")
	}
	if ss := st.List(t0); len(ss) != 1 {
		t.Fatalf("session without cwd was created: %d sessions", len(ss))
	}
}

func TestStatuslineLoggedSurvivesRestartAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	m := Statusline{Agent: "claude", Host: "pc", SessionID: "s1", Cwd: "/p/app", At: t0, CostUsd: 5, ContextPct: 40}
	if _, ok := st.ApplyStatusline(m); !ok {
		t.Fatal("not archived")
	}
	// The session is removed from the page, the server restarts, then the
	// session is resumed with its cost counter where it was.
	st.Remove("claude/pc/s1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	st, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m.At, m.CostUsd = t0.Add(48*time.Hour), 5.5
	rec, ok := st.ApplyStatusline(m)
	if !ok || rec.CostDelta != 0.5 {
		t.Fatalf("resumed session: ok=%v delta=%v, want 0.5", ok, rec.CostDelta)
	}
}
