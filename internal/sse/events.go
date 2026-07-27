// SPDX-License-Identifier: AGPL-3.0-or-later

package sse

// The event catalog. Every event this server publishes is declared here as
// a typed payload that names itself and states who should receive it.
//
// It exists because the wire vocabulary used to live in two hand-kept
// lists — string literals scattered across the service and task packages,
// and a hand-typed union in ui/src/api/realtime.ts — that agreed only by
// convention. They drifted: kindle.sent and kindle.failed were published
// for months with no listener on the other side, so Send-to-Kindle
// reported nothing to the user. A Go test now asserts the client union
// matches Catalog, so the next divergence fails the build instead of
// silently dropping a notification.
//
// Audience is part of the declaration rather than a caller's decision.
// The Hub fans out to every subscriber by default, so an event meant for
// one person had to carry its recipient in the payload and rely on
// client-side filtering — which meant the recipient's id travelled to
// every connected browser. Declaring the audience routes the event
// instead, and keeps the routing id off the wire entirely.

// Audience decides which subscribers receive an event. The zero value
// means everyone.
type Audience struct {
	// UserID, when non-empty, restricts delivery to that user's
	// subscriptions — all of them, so a user with several tabs open sees
	// the event in each.
	UserID string
}

// Everyone is the audience for instance-wide events: shared surfaces such
// as BookDrop (no per-user rows) and the public-shelf list.
func Everyone() Audience { return Audience{} }

// User restricts an event to a single user's subscriptions.
func User(id string) Audience { return Audience{UserID: id} }

// Payload is one declared event. Implementations are plain structs whose
// exported fields are the wire shape; fields tagged `json:"-"` are
// routing-only and never reach a browser.
type Payload interface {
	// EventName is the SSE event name the browser listens for. It is a
	// wire contract — renaming one breaks clients.
	EventName() string
	// Audience reports who should receive this instance.
	Audience() Audience
}

// ---------------------------------------------------------------------------
// BookDrop — a shared, instance-wide staging area. Every authenticated user
// can list and act on it (router mounts /bookdrop under `authed`, not
// `admin`), so these are broadcasts, not per-user events.
// ---------------------------------------------------------------------------

// BookDropUpdated fires when a staged item changes state.
type BookDropUpdated struct {
	ID string `json:"id"`
}

func (BookDropUpdated) EventName() string  { return "bookdrop.updated" }
func (BookDropUpdated) Audience() Audience { return Everyone() }

// BookDropCleared fires after a housekeeping sweep removes rows.
type BookDropCleared struct{}

func (BookDropCleared) EventName() string  { return "bookdrop.cleared" }
func (BookDropCleared) Audience() Audience { return Everyone() }

// ReadingGuideUpdated fires when a book's LLM-written reading guide is
// written or replaced (ADR-0024). Instance-wide: guides are a property of
// the book, not of a reader, and a bulk run's progress is worth seeing on
// whatever page is open.
type ReadingGuideUpdated struct {
	BookID string `json:"bookId"`
}

func (ReadingGuideUpdated) EventName() string  { return "guide.updated" }
func (ReadingGuideUpdated) Audience() Audience { return Everyone() }

// ---------------------------------------------------------------------------
// Users / shelves
// ---------------------------------------------------------------------------

// UsersUpdated fires when the admin-visible user list changes.
type UsersUpdated struct{}

func (UsersUpdated) EventName() string  { return "users.updated" }
func (UsersUpdated) Audience() Audience { return Everyone() }

// SharedShelfUpdated fires when an admin publishes or edits a Shared
// shelf. Slug carries the `public:` prefix already applied — the prefix is
// part of the wire contract, so it is stamped here rather than at each
// call site.
type SharedShelfUpdated struct {
	Slug string `json:"slug"`
}

func (SharedShelfUpdated) EventName() string  { return "shelf.public.updated" }
func (SharedShelfUpdated) Audience() Audience { return Everyone() }

// SharedShelfRemoved fires when a Shared shelf is un-published or deleted.
type SharedShelfRemoved struct {
	Slug string `json:"slug"`
}

func (SharedShelfRemoved) EventName() string  { return "shelf.public.removed" }
func (SharedShelfRemoved) Audience() Audience { return Everyone() }

// ---------------------------------------------------------------------------
// Send-to-Kindle — the only genuinely user-scoped events. UserID routes and
// is deliberately absent from the wire.
// ---------------------------------------------------------------------------

// KindleSent reports a successful Send-to-Kindle delivery.
type KindleSent struct {
	UserID string `json:"-"`
	BookID string `json:"book_id"`
}

func (KindleSent) EventName() string    { return "kindle.sent" }
func (e KindleSent) Audience() Audience { return User(e.UserID) }

// KindleFailed reports a failed Send-to-Kindle delivery. Error is the
// user-facing reason.
type KindleFailed struct {
	UserID string `json:"-"`
	BookID string `json:"book_id"`
	Error  string `json:"error,omitempty"`
}

func (KindleFailed) EventName() string    { return "kindle.failed" }
func (e KindleFailed) Audience() Audience { return User(e.UserID) }

// Catalog lists one zero value per declared event. It exists so tests can
// enumerate the vocabulary — in particular the parity test that keeps the
// TypeScript client's union honest.
var Catalog = []Payload{
	BookDropUpdated{},
	BookDropCleared{},
	UsersUpdated{},
	SharedShelfUpdated{},
	SharedShelfRemoved{},
	KindleSent{},
	KindleFailed{},
	ReadingGuideUpdated{},
}
