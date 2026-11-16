package status

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMachinesOnlineAndPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.json")
	ms, err := NewMachines(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	ms.Beat("pc", "192.168.1.5", t0)
	ms.Beat("pc", "192.168.1.5", t0.Add(time.Minute))

	l := ms.List(t0.Add(2 * time.Minute))
	if len(l) != 1 || !l[0].Online || !l[0].FirstBeat.Equal(t0) {
		t.Fatalf("online: %+v", l)
	}
	if l := ms.List(t0.Add(5 * time.Minute)); l[0].Online {
		t.Fatalf("should be offline 4 min after the last beat: %+v", l)
	}

	// Back after a pause: a new run of heartbeats starts.
	back := t0.Add(time.Hour)
	ms.Beat("pc", "192.168.1.6", back)
	if l := ms.List(back); !l[0].Online || !l[0].FirstBeat.Equal(back) || l[0].IP != "192.168.1.6" {
		t.Fatalf("back online: %+v", l)
	}

	if err := ms.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := NewMachines(path)
	if err != nil {
		t.Fatal(err)
	}
	if l := again.List(back.Add(10 * time.Minute)); len(l) != 1 || l[0].Online || !l[0].LastBeat.Equal(back) {
		t.Fatalf("reloaded: %+v", l)
	}
}
