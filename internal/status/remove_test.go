package status

import (
	"testing"
	"time"
)

func TestStoreRemove(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s, _ := NewStore("")
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "a", State: StateWorking, At: t0})
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "b", State: StateEnded, At: t0})
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "c", State: StateEnded, At: t0})
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "d", State: StateIdle, At: t0})

	if n := s.Remove("claude/h/d", "claude/h/missing"); n != 1 {
		t.Errorf("Remove = %d, want 1", n)
	}
	if n := s.RemoveEnded(); n != 2 {
		t.Errorf("RemoveEnded = %d, want 2", n)
	}
	list := s.List(t0)
	if len(list) != 1 || list[0].SessionID != "a" {
		t.Fatalf("left = %+v", list)
	}

	// A removed session that reports again shows up again.
	s.Apply(Event{Agent: "claude", Host: "h", SessionID: "d", State: StateWorking, At: t0.Add(time.Second)})
	if got := len(s.List(t0.Add(time.Second))); got != 2 {
		t.Errorf("after new event len = %d, want 2", got)
	}
}
