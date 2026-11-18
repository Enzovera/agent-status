package web

import (
	"crypto/sha256"
	"sync"
	"time"
)

// dupWindow: a byte-identical event from the same machine within this time is
// the same event delivered twice. OpenCode can run the plugin more than once
// (seen on a Mac: every event four times), which would count every tool call,
// prompt and permission several times over.
const dupWindow = 2 * time.Second

type dedupe struct {
	mu   sync.Mutex
	seen map[[32]byte]time.Time
	last time.Time // when old entries were last swept
}

// fresh reports whether this payload was not already seen within dupWindow,
// and remembers it.
func (d *dedupe) fresh(path, host string, body []byte, now time.Time) bool {
	h := sha256.New()
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write([]byte(host))
	h.Write([]byte{0})
	h.Write(body)
	var k [32]byte
	copy(k[:], h.Sum(nil))

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[[32]byte]time.Time{}
	}
	if now.Sub(d.last) > dupWindow {
		for key, at := range d.seen {
			if now.Sub(at) > dupWindow {
				delete(d.seen, key)
			}
		}
		d.last = now
	}
	if at, ok := d.seen[k]; ok && now.Sub(at) <= dupWindow {
		return false
	}
	d.seen[k] = now
	return true
}
