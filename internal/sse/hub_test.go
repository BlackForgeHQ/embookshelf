// SPDX-License-Identifier: AGPL-3.0-or-later

package sse

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// recv reads one event with a short deadline. Routing bugs otherwise show
// up as a hung test rather than a failure.
func recv(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(time.Second):
		return Event{}, false
	}
}

// mustNotRecv asserts nothing arrives — the assertion that makes routing
// meaningful, as opposed to "everyone gets everything and filters".
func mustNotRecv(t *testing.T, ch <-chan Event, who string) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("%s received %q but should not have", who, ev.Name)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestPublishBroadcastReachesEverySubscriber(t *testing.T) {
	t.Parallel()
	h := NewHub()

	alice, cancelA := h.Subscribe("alice", 4)
	bob, cancelB := h.Subscribe("bob", 4)
	defer cancelA()
	defer cancelB()

	if err := h.Publish(BookDropUpdated{ID: "item-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for _, c := range []struct {
		name string
		ch   <-chan Event
	}{{"alice", alice}, {"bob", bob}} {
		ev, ok := recv(t, c.ch)
		if !ok {
			t.Fatalf("%s did not receive the broadcast", c.name)
		}
		if ev.Name != "bookdrop.updated" {
			t.Errorf("%s got event %q, want bookdrop.updated", c.name, ev.Name)
		}
	}
}

// The point of the audience: a user-scoped event must not reach anyone
// else, rather than reaching everyone and being filtered client-side.
func TestPublishUserScopedReachesOnlyThatUser(t *testing.T) {
	t.Parallel()
	h := NewHub()

	alice, cancelA := h.Subscribe("alice", 4)
	bob, cancelB := h.Subscribe("bob", 4)
	defer cancelA()
	defer cancelB()

	if err := h.Publish(KindleSent{UserID: "alice", BookID: "book-9"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ev, ok := recv(t, alice)
	if !ok {
		t.Fatal("alice did not receive her own kindle event")
	}
	if ev.Name != "kindle.sent" {
		t.Errorf("event = %q, want kindle.sent", ev.Name)
	}
	mustNotRecv(t, bob, "bob")
}

// A user with several tabs open has several subscriptions; all of them
// should see the event.
func TestPublishUserScopedReachesEveryTab(t *testing.T) {
	t.Parallel()
	h := NewHub()

	tab1, cancel1 := h.Subscribe("alice", 4)
	tab2, cancel2 := h.Subscribe("alice", 4)
	defer cancel1()
	defer cancel2()

	if err := h.Publish(KindleFailed{UserID: "alice", BookID: "b", Error: "too large"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i, ch := range []<-chan Event{tab1, tab2} {
		if _, ok := recv(t, ch); !ok {
			t.Fatalf("tab %d did not receive the event", i+1)
		}
	}
}

// The routing id must not travel to the browser — that leak is why the
// audience exists.
func TestUserScopedPayloadOmitsRoutingID(t *testing.T) {
	t.Parallel()
	h := NewHub()

	alice, cancel := h.Subscribe("alice", 4)
	defer cancel()

	if err := h.Publish(KindleSent{UserID: "alice", BookID: "book-9"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ev, ok := recv(t, alice)
	if !ok {
		t.Fatal("no event received")
	}

	if strings.Contains(ev.Data, "alice") {
		t.Errorf("payload leaked the routing user id: %s", ev.Data)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if got["book_id"] != "book-9" {
		t.Errorf("book_id = %v, want book-9", got["book_id"])
	}
}

func TestPublishEmptyPayloadIsAnObject(t *testing.T) {
	t.Parallel()
	h := NewHub()
	ch, cancel := h.Subscribe("alice", 4)
	defer cancel()

	if err := h.Publish(BookDropCleared{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ev, ok := recv(t, ch)
	if !ok {
		t.Fatal("no event received")
	}
	if ev.Data != "{}" {
		t.Errorf("Data = %q, want {} — clients JSON.parse every payload", ev.Data)
	}
}

// Preserved from the original Hub: a slow subscriber must never block a
// publisher, it just misses events.
func TestPublishDoesNotBlockOnAFullSubscriber(t *testing.T) {
	t.Parallel()
	h := NewHub()

	_, cancelSlow := h.Subscribe("slow", 1)
	defer cancelSlow()
	fast, cancelFast := h.Subscribe("fast", 8)
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			_ = h.Publish(BookDropUpdated{ID: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full subscriber buffer")
	}
	if _, ok := recv(t, fast); !ok {
		t.Fatal("fast subscriber received nothing")
	}
}

func TestCancelRemovesTheSubscription(t *testing.T) {
	t.Parallel()
	h := NewHub()

	ch, cancel := h.Subscribe("alice", 4)
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	if n := h.Subscribers(); n != 0 {
		t.Errorf("Subscribers() = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Catalog ↔ client parity
// ---------------------------------------------------------------------------

const clientFile = "../../ui/src/api/realtime.ts"

// clientUnion reads the RealtimeEvent union members from the TypeScript
// client.
//
// It parses the union specifically rather than searching the whole file:
// every event name also appears as a key in the handlers map, so a
// substring search would still find a name after its union entry was
// deleted — a check that cannot fail is worse than no check. This parser
// was verified by deleting a union member and watching the test go red.
func clientUnion(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile(clientFile)
	if err != nil {
		t.Skipf("cannot read %s: %v", clientFile, err)
	}

	names := map[string]bool{}
	inUnion := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type RealtimeEvent") {
			inUnion = true
			continue
		}
		if !inUnion {
			continue
		}
		// Members are one per line: | "some.event"
		if strings.HasPrefix(trimmed, `| "`) {
			names[strings.Trim(strings.TrimPrefix(trimmed, "|"), " \"")] = true
			continue
		}
		if trimmed != "" {
			break // union block ended
		}
	}

	if len(names) == 0 {
		t.Fatalf("parsed no members from the RealtimeEvent union in %s — "+
			"the union's shape changed and this test is no longer checking anything", clientFile)
	}
	return names
}

// The drift this module exists to prevent: an event published by the
// server that the client's union never listed. kindle.sent and
// kindle.failed were exactly that for months, and nothing failed.
func TestCatalogMatchesClientUnion(t *testing.T) {
	t.Parallel()
	union := clientUnion(t)

	for _, p := range Catalog {
		if name := p.EventName(); !union[name] {
			t.Errorf(`event %q is published by the server but absent from the
RealtimeEvent union in %s — the client will never receive it.
Add it to the union and give it a handler.`, name, clientFile)
		}
	}
}

// The reverse: a name the client listens for that nothing publishes is
// dead code, and a rename leaves one behind on each side.
func TestClientUnionHasNoUnknownEvents(t *testing.T) {
	t.Parallel()
	union := clientUnion(t)

	known := map[string]bool{}
	for _, p := range Catalog {
		known[p.EventName()] = true
	}
	for name := range union {
		if !known[name] {
			t.Errorf("%s lists event %q, which no server event publishes", clientFile, name)
		}
	}
}
