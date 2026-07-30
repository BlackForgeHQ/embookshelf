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
		{
			jobs.AudiobookSegmentArgs{BookID: "b1", Seq: 7, Generation: 2},
			"audiobook.segment",
			`{"book_id":"b1","seq":7,"generation":2}`,
		},
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

// A segment job enqueued before generations existed still decodes, and
// decodes to generation 0.
//
// This is the deploy story and it rests entirely on a zero value, which
// is exactly the kind of thing that looks accidental to the next reader
// and gets "tidied" into a pointer or a required field. The rows are
// real: a deployment upgraded mid-run has river_job rows whose args JSON
// has no generation key at all. Zero is what they must decode to, because
// zero is also what the run row's column defaults to — so such a job
// matches a run nothing has restarted since the deploy, which is a run it
// genuinely belongs to. The other half, that it then claims its segment,
// is TestAJobEnqueuedBeforeGenerationsExistedStillClaimsItsSegment in
// internal/task.
func TestAudiobookSegmentArgsFromBeforeGenerationsDecodeToZero(t *testing.T) {
	const stored = `{"book_id":"b1","seq":7}`

	var args jobs.AudiobookSegmentArgs
	if err := json.Unmarshal([]byte(stored), &args); err != nil {
		t.Fatalf("decode a job enqueued before the field existed: %v", err)
	}
	if args.BookID != "b1" || args.Seq != 7 {
		t.Errorf("args = %+v, want the book and seq the row stored", args)
	}
	if args.Generation != 0 {
		t.Errorf("Generation = %d, want 0 — the run rows such a job addresses are still at the column default",
			args.Generation)
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

// Attempt.Last is the whole of the retry verdict a worker can reach
// without importing River, so what it says at each end of the range is
// worth stating — including at the zero value, which is the case a reader
// is most likely to mistake for an oversight.
//
// A zero Attempt means no queue told this worker anything. Last says yes,
// because there is no retry to wait for: a segment worker that recorded
// "retrying" on it would leave a row nothing is ever going to move, and
// a run that never concludes (ADR-0032).
func TestAttemptLastIsTrueOnTheLastAttemptAndAtTheZeroValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		attempt jobs.Attempt
		want    bool
	}{
		{"first of many", jobs.Attempt{Number: 1, Max: 25}, false},
		{"one to go", jobs.Attempt{Number: 24, Max: 25}, false},
		{"the last one", jobs.Attempt{Number: 25, Max: 25}, true},
		{"a job that gets one attempt", jobs.Attempt{Number: 1, Max: 1}, true},
		{"nothing told us", jobs.Attempt{}, true},
	}
	for _, c := range cases {
		if got := c.attempt.Last(); got != c.want {
			t.Errorf("%s: Attempt%+v.Last() = %v, want %v", c.name, c.attempt, got, c.want)
		}
	}
}
