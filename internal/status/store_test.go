package status

import (
	"path/filepath"
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
