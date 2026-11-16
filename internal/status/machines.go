package status

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"sync"
	"time"
)

// A machine is online while its heartbeat keeps coming. Clients send one a
// minute; a few missed beats (sleep, network hiccup) are tolerated.
const (
	heartbeatEvery = time.Minute
	offlineAfter   = 3 * heartbeatEvery
	// Machines that have not beaten for this long drop off the list.
	forgetMachineAfter = 30 * 24 * time.Hour
)

// Machine is what the page shows about one reporting computer.
type Machine struct {
	Host string `json:"host"`
	// IP is the address the last heartbeat came from.
	IP string `json:"ip,omitempty"`
	// FirstBeat is when this run of heartbeats started (the machine came online).
	FirstBeat time.Time `json:"firstBeat"`
	// LastBeat is the last heartbeat; Online is false once it is offlineAfter old.
	LastBeat time.Time `json:"lastBeat"`
	Online   bool      `json:"online"`
}

// Machines keeps the last heartbeat of every machine, saved to a small file
// so a machine that is off while the server restarts still shows as offline.
type Machines struct {
	mu    sync.Mutex
	path  string
	m     map[string]*Machine
	dirty bool
}

// NewMachines loads path if it exists; an empty path keeps them in memory only.
func NewMachines(path string) (*Machines, error) {
	ms := &Machines{path: path, m: map[string]*Machine{}}
	if path == "" {
		return ms, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ms, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Machine
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, m := range list {
		if m.Host != "" {
			ms.m[m.Host] = m
		}
	}
	return ms, nil
}

// Save writes the file if a heartbeat arrived since the last save.
func (ms *Machines) Save() error {
	ms.mu.Lock()
	if ms.path == "" || !ms.dirty {
		ms.mu.Unlock()
		return nil
	}
	list := make([]Machine, 0, len(ms.m))
	for _, m := range ms.m {
		list = append(list, *m)
	}
	ms.dirty = false
	ms.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Host < list[j].Host })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := ms.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ms.path)
}

// Beat records a heartbeat from host.
func (ms *Machines) Beat(host, ip string, at time.Time) {
	if host == "" {
		return
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	m, ok := ms.m[host]
	if !ok {
		m = &Machine{Host: host}
		ms.m[host] = m
	}
	if !ok || at.Sub(m.LastBeat) > offlineAfter {
		m.FirstBeat = at
	}
	m.LastBeat = at
	m.IP = ip
	ms.dirty = true
}

// List returns every machine that sent a heartbeat, sorted by host, with
// Online computed for now.
func (ms *Machines) List(now time.Time) []Machine {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	out := make([]Machine, 0, len(ms.m))
	for host, m := range ms.m {
		age := now.Sub(m.LastBeat)
		if age > forgetMachineAfter {
			delete(ms.m, host)
			ms.dirty = true
			continue
		}
		c := *m
		c.Online = age <= offlineAfter
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}
