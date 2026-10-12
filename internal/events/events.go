// Package events fans server events out to browsers over Server-Sent Events (PRD V-2).
package events

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event types the viewer listens for.
const (
	Model    = "model"    // a new compile: {project, version}
	Job      = "job"      // approval waiting or approved: the job
	Estimate = "estimate" // an estimate finished or failed
	Printer  = "printer"  // printer status
	Settings = "settings" // settings saved
)

type Event struct {
	Type string
	Data []byte
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
	// Heartbeat keeps proxies and browsers from closing an idle stream.
	Heartbeat time.Duration
}

func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}, Heartbeat: 25 * time.Second} }

// Publish sends v as JSON to every subscriber; a subscriber that is too slow misses it.
func (h *Hub) Publish(typ string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- Event{Type: typ, Data: b}:
		default:
		}
	}
}

func (h *Hub) subscribe() chan Event {
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Subscribers is the number of open streams.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := h.subscribe()
	defer h.unsubscribe(ch)
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()
	tick := time.NewTicker(h.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			flusher.Flush()
		}
	}
}
