// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sse is a tiny fan-out hub for Server-Sent Events. Background work
// (the BookDrop worker, library scans, etc.) publishes events here; the
// /events HTTP handler serves each connected client from a per-connection
// channel populated by Broadcast.
package sse

import (
	"sync"
)

// Event is a single message on the wire. Name maps to the browser
// EventSource event name; Data is the (already-serialized) payload.
type Event struct {
	Name string
	Data string
}

// Hub is a concurrency-safe fan-out. Subscribers get a buffered channel that
// drops old events rather than blocking publishers (so a slow client can't
// jam the background pipeline).
type Hub struct {
	mu   sync.Mutex
	subs map[int]chan Event
	next int
}

func NewHub() *Hub {
	return &Hub{subs: make(map[int]chan Event)}
}

// Subscribe registers a new listener. The returned cancel func removes the
// subscription and closes the channel.
func (h *Hub) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 16
	}
	ch := make(chan Event, buffer)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if existing, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(existing)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Broadcast fans an event out to every current subscriber. Non-blocking: if a
// subscriber's buffer is full, we drop the event for that subscriber only.
func (h *Hub) Broadcast(ev Event) {
	h.mu.Lock()
	subs := make([]chan Event, 0, len(h.subs))
	for _, ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribers returns the current count — useful for debugging/monitoring.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
