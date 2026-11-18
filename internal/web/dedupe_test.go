package web

import (
	"testing"
	"time"
)

func TestDedupe(t *testing.T) {
	var d dedupe
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	body := []byte(`{"type":"session.tool.called"}`)
	if !d.fresh("/hook/opencode", "mac", body, t0) {
		t.Fatal("first delivery must pass")
	}
	for i := 0; i < 3; i++ {
		if d.fresh("/hook/opencode", "mac", body, t0.Add(time.Millisecond)) {
			t.Fatal("copy within the window must be dropped")
		}
	}
	if !d.fresh("/hook/opencode", "other", body, t0) {
		t.Error("same body from another machine is another event")
	}
	if !d.fresh("/hook/opencode", "mac", body, t0.Add(3*time.Second)) {
		t.Error("the same body later is a new event")
	}
}
