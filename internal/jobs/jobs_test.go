// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs_test

import (
	"encoding/json"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
)

// Kind strings and json tags are stored values: River persists the kind
// alongside the encoded args, and a running deployment has rows in
// river_job carrying both. Renaming a Go type is free; changing either
// of these orphans every in-flight job of that type, silently, with no
// error anywhere.
//
// So they are pinned here as literals rather than derived from the
// types — a test that asks the type what its kind is would agree with
// any rename.
func TestJobKindsAndPayloadsAreTheStoredOnes(t *testing.T) {
	cases := []struct {
		args jobs.Args
		kind string
		json string
	}{
		{jobs.BookDropIngestArgs{ItemID: "i1"}, "bookdrop.ingest", `{"item_id":"i1"}`},
		{jobs.BookDropAutoEnrichArgs{BookID: "b1"}, "bookdrop.auto_enrich", `{"book_id":"b1"}`},
		{jobs.LibraryScanArgs{LibraryID: "l1"}, "library.scan", `{"library_id":"l1"}`},
		{jobs.SendToKindleArgs{BookID: "b1", UserID: "u1"}, "kindle.send", `{"book_id":"b1","user_id":"u1"}`},
		{jobs.ReadingGuideArgs{BookID: "b1"}, "guide.generate", `{"book_id":"b1"}`},
		{jobs.AudiobookSegmentArgs{BookID: "b1", Seq: 7}, "audiobook.segment", `{"book_id":"b1","seq":7}`},
		{jobs.AudiobookFinalizeArgs{BookID: "b1"}, "audiobook.finalize", `{"book_id":"b1"}`},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if got := tc.args.Kind(); got != tc.kind {
				t.Errorf("kind = %q, want %q — this orphans in-flight jobs", got, tc.kind)
			}
			b, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.json {
				t.Errorf("payload = %s, want %s — this orphans in-flight jobs", b, tc.json)
			}
		})
	}
}

// TestAudiobookQueueIsTheStoredValue pins the queue name itself, the same
// way the kinds above are pinned: River persists "audiobook" in the
// queue column of every river_job row a segment or finalize job leaves
// behind, and the client's polled queue set is derived from this
// constant (internal/queue/queue.go). Both tests above compare against
// jobs.AudiobookQueue rather than the literal, so they would agree with
// any value the constant was changed to; only this literal catches a
// change here. Changing it would leave every existing row with
// queue='audiobook' unpolled forever, silently.
func TestAudiobookQueueIsTheStoredValue(t *testing.T) {
	if jobs.AudiobookQueue != "audiobook" {
		t.Errorf("AudiobookQueue = %q, want %q — existing river_job rows on the old queue would go unpolled",
			jobs.AudiobookQueue, "audiobook")
	}
}

// Only the audiobook jobs declare a queue. A run is tens of long jobs
// per book, and sharing the default pool would stall BookDrop ingest
// and Library scan for as long as the run lasts (ADR-0028 §3).
func TestOnlyAudiobookJobsDeclareTheirOwnQueue(t *testing.T) {
	queued := map[string]string{
		"audiobook.segment":  jobs.AudiobookQueue,
		"audiobook.finalize": jobs.AudiobookQueue,
	}
	all := []jobs.Args{
		jobs.BookDropIngestArgs{}, jobs.BookDropAutoEnrichArgs{}, jobs.LibraryScanArgs{},
		jobs.SendToKindleArgs{}, jobs.ReadingGuideArgs{},
		jobs.AudiobookSegmentArgs{}, jobs.AudiobookFinalizeArgs{},
	}
	for _, a := range all {
		q, declares := a.(jobs.Queued)
		want, shouldDeclare := queued[a.Kind()]
		if declares != shouldDeclare {
			t.Errorf("%s declares a queue = %v, want %v", a.Kind(), declares, shouldDeclare)
			continue
		}
		if declares && q.Queue() != want {
			t.Errorf("%s queue = %q, want %q", a.Kind(), q.Queue(), want)
		}
	}
}
