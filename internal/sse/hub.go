// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sse is a tiny fan-out hub for Server-Sent Events. Background work
// (the BookDrop worker, library scans, etc.) publishes events here; the
// /events HTTP handler serves each connected client from a per-connection
// channel populated by Broadcast.
package sse

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Event is a single message on the wire. Name maps to the browser
// EventSource event name; Data is the serialized payload.
type Event struct {
	Name string
	Data string
}

// subscription is one connected client: its delivery channel plus the user
// it belongs to, so user-scoped events can be routed rather than filtered
// after the fact.
type subscription struct {
	userID string
	ch     chan Event
}

// Hub is a concurrency-safe fan-out. Subscribers get a buffered channel that
// drops old events rather than blocking publishers (so a slow client can't
// jam the background pipeline).
type Hub struct {
	mu   sync.Mutex
	subs map[int]subscription
	next int
}

func NewHub() *Hub {
	return &Hub{subs: make(map[int]subscription)}
}

// Subscribe registers a listener belonging to userID. The returned cancel
// func removes the subscription and closes the channel.
//
// userID is required for routing: an event whose Audience names a user is
// delivered only to that user's subscriptions. One user may hold several
// (a tab each); all of them receive it.
func (h *Hub) Subscribe(userID string, buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 16
	}
	ch := make(chan Event, buffer)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = subscription{userID: userID, ch: ch}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if existing, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(existing.ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Publish serializes a declared event and delivers it to its audience.
//
// This is the only way to emit: it keeps the name, the wire shape, and the
// recipient together in the catalog rather than at each call site, which is
// what let kindle.sent drift out of the client's vocabulary unnoticed.
//
// Delivery is non-blocking — a subscriber whose buffer is full misses this
// event rather than stalling the publisher. An error means the payload
// could not be serialized; nothing was delivered.
func (h *Hub) Publish(p Payload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", p.EventName(), err)
	}
	h.deliver(p.Audience(), Event{Name: p.EventName(), Data: string(data)})
	return nil
}

// deliver fans an event out to the subscriptions its audience selects.
func (h *Hub) deliver(aud Audience, ev Event) {
	h.mu.Lock()
	targets := make([]chan Event, 0, len(h.subs))
	for _, sub := range h.subs {
		if aud.UserID != "" && sub.userID != aud.UserID {
			continue
		}
		targets = append(targets, sub.ch)
	}
	h.mu.Unlock()

	for _, ch := range targets {
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
