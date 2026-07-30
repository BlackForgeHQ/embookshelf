import { useState } from "react"

import type { ApiError } from "@/api/client"
import type { Audiobook, AudiobookEstimate } from "@/api/audiobooks"
import {
  audiobookEstimateQuery,
  bookAudiobookQuery,
  cancelAudiobook,
  deleteAudiobook,
  generateAudiobook,
  narrationUrl,
  retryAudiobook,
} from "@/api/audiobooks"
import { useApiMutation } from "@/api/mutation"
import { LIVE_POLL_MS, useApiQuery } from "@/api/query"
import { Button } from "@/components/ui/button"
import { Icon } from "@/components/Icon"
import { ProgressBar } from "@/components/ProgressBar"
import type { Viewer } from "@/lib/affordance"
import { affordanceFor, messageForCode, useViewer } from "@/lib/affordance"
import { isNarratableFormat, narratableFormatList } from "@/lib/formats"
import { runView } from "@/lib/audiobookRun"
import type { RunView } from "@/lib/audiobookRun"


function formatDuration(seconds: number): string {
  if (seconds <= 0) return "—"
  const h = Math.floor(seconds / 3600)
  const m = Math.round((seconds % 3600) / 60)
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

// Money is shown to two decimals until it rounds to nothing, because
// "$0.00" for a run that costs three cents reads as free.
function formatCost(usd: number): string {
  if (usd === 0) return "free"
  if (usd < 0.01) return "<$0.01"
  return `$${usd.toFixed(2)}`
}

export function AudiobookPanel({
  bookId,
  format,
}: {
  bookId: string
  format: string
}) {
  const viewer = useViewer()

  const audiobook = useApiQuery(bookAudiobookQuery(bookId), {
    // Polls only while the run is moving and stops on its own. Undefined
    // is a first fetch still in flight and null is a book with no run at
    // all; neither is something to poll about.
    refetchInterval: (q) =>
      q.state.data && runView(q.state.data).phase === "running"
        ? LIVE_POLL_MS
        : false,
  })

  const [confirming, setConfirming] = useState(false)

  if (audiobook.isLoading) return <p className="t-small">Loading…</p>

  const a = audiobook.data

  if (!a) {
    return (
      <EmptyState
        bookId={bookId}
        format={format}
        viewer={viewer}
        confirming={confirming}
        setConfirming={setConfirming}
      />
    )
  }

  // Every branch below reads derived facts. The panel used to compare
  // state strings in four places (#197).
  const view = runView(a)

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 14, maxWidth: 640 }}>
      {view.phase === "running" ? (
        <RunProgress audiobook={a} view={view} bookId={bookId} viewer={viewer} />
      ) : view.phase === "ready" ? (
        <ReadyState audiobook={a} bookId={bookId} />
      ) : (
        <StoppedState audiobook={a} view={view} bookId={bookId} viewer={viewer} />
      )}

      {view.showProvenance && <Provenance audiobook={a} />}

      {viewer.isAdmin && view.canRegenerate && (
        <div style={{ display: "flex", gap: 8 }}>
          <RegenerateButton
            bookId={bookId}
            format={format}
            viewer={viewer}
            confirming={confirming}
            setConfirming={setConfirming}
            hasExisting={view.phase === "ready"}
          />
          <DeleteButton bookId={bookId} />
        </div>
      )}
    </div>
  )
}

function EmptyState({
  bookId,
  format,
  viewer,
  confirming,
  setConfirming,
}: {
  bookId: string
  format: string
  viewer: Viewer
  confirming: boolean
  setConfirming: (v: boolean) => void
}) {
  if (!isNarratableFormat(format)) {
    return (
      <p className="t-small">
        Only {narratableFormatList()} books can be narrated — this one is{" "}
        {format}. Text has to come from somewhere, and no other format in the
        library carries any.
      </p>
    )
  }
  if (!viewer.isAdmin) {
    return (
      <p className="t-small">
        No narration yet. An administrator can generate one — it costs real
        money, so it is theirs to start.
      </p>
    )
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 640 }}>
      <p className="t-small">
        No narration yet. Generating one reads this book aloud with a
        text-to-speech engine and saves the result beside it as an MP3 you
        can download and take with you.
      </p>
      <RegenerateButton
        bookId={bookId}
        format={format}
        viewer={viewer}
        confirming={confirming}
        setConfirming={setConfirming}
        hasExisting={false}
      />
    </div>
  )
}

// RunProgress is the coverage count, not job state: it survives a reload
// and a restart, which matters because nobody watches a job that takes
// tens of minutes.
function RunProgress({
  audiobook,
  view,
  bookId,
  viewer,
}: {
  audiobook: Audiobook
  view: RunView
  bookId: string
  viewer: Viewer
}) {
  const cancelMut = useApiMutation(cancelAudiobook, {
    successToast: "Narration cancelled.",
  })

  const pct = view.percent

  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between" style={{ gap: 12 }}>
        <span className="t-small">
          Narrating — {audiobook.segmentsDone} of {audiobook.segmentsTotal}{" "}
          sections
          {audiobook.segmentsFailed > 0
            ? `, ${audiobook.segmentsFailed} failed`
            : ""}
        </span>
        <span className="t-small tabular-nums">{pct}%</span>
      </div>
      <ProgressBar value={pct / 100} label="Narration progress" />
      <p className="t-small" style={{ marginTop: 8 }}>
        This runs in the background — you can leave this page.
      </p>
      {viewer.isAdmin && view.canCancel && (
        <div style={{ marginTop: 10 }}>
          <Button
            variant="outline"
            size="sm"
            disabled={cancelMut.isPending}
            onClick={() => cancelMut.mutate(bookId)}
          >
            Stop
          </Button>
        </div>
      )}
    </div>
  )
}

function ReadyState({
  audiobook,
  bookId,
}: {
  audiobook: Audiobook
  bookId: string
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <audio
        controls
        preload="none"
        src={narrationUrl(bookId)}
        style={{ width: "100%" }}
      />
      <p className="t-small" style={{ margin: 0 }}>
        {formatDuration(audiobook.durationSeconds)} · chapter marks are
        embedded, so a podcast player will show them too.
      </p>
      <div>
        <a
          className="t-small"
          href={narrationUrl(bookId, { download: true })}
          download
        >
          Download MP3
        </a>
      </div>
    </div>
  )
}

// StoppedState covers failed and cancelled, which are deliberately not
// the same thing: a failure kept every section it already paid for, so
// Retry is cheap, while a cancel threw the partial away.
function StoppedState({
  audiobook,
  view,
  bookId,
  viewer,
}: {
  audiobook: Audiobook
  view: RunView
  bookId: string
  viewer: Viewer
}) {
  const retryMut = useApiMutation(retryAudiobook, {
    successToast: "Picking up where it stopped.",
  })

  // Retry is offered on a failure and not on a cancellation, which is
  // also what distinguishes the two sentences below.
  const failed = view.canRetry
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <p
        className="t-small"
        style={{ margin: 0, color: failed ? "var(--color-warn, #92400e)" : undefined }}
      >
        {failed ? (
          <>
            Narration failed after {audiobook.segmentsDone} of{" "}
            {audiobook.segmentsTotal} sections.
            {audiobook.error ? ` ${audiobook.error}` : ""}
          </>
        ) : (
          <>Narration was cancelled.</>
        )}
      </p>
      {failed && (
        <p className="t-small" style={{ margin: 0 }}>
          The sections that finished were kept — retrying only pays for the
          ones that did not.
        </p>
      )}
      {viewer.isAdmin && failed && (
        <div>
          <Button
            variant="outline"
            size="sm"
            disabled={retryMut.isPending}
            onClick={() => retryMut.mutate(bookId)}
          >
            Retry the rest
          </Button>
        </div>
      )}
    </div>
  )
}

function Provenance({ audiobook }: { audiobook: Audiobook }) {
  return (
    <p
      className="t-small"
      style={{
        margin: 0,
        paddingTop: 10,
        borderTop: "1px dashed var(--color-rule-soft)",
        color: audiobook.stale ? "var(--color-warn, #92400e)" : undefined,
      }}
    >
      {audiobook.stale ? (
        <>
          Generated from an older copy of this book — the file has changed
          since. Regenerate to match the current text.{" "}
        </>
      ) : null}
      Read by {audiobook.voice || "an unnamed voice"}
      {audiobook.engine ? ` · ${audiobook.engine}` : ""}
    </p>
  )
}

// RegenerateButton is where money is actually committed, so it never
// fires on the first click: the estimate is fetched and shown, and only
// the second click starts a run (ADR-0028 §2).
function RegenerateButton({
  bookId,
  format,
  viewer,
  confirming,
  setConfirming,
  hasExisting,
}: {
  bookId: string
  format: string
  viewer: Viewer
  confirming: boolean
  setConfirming: (v: boolean) => void
  hasExisting: boolean
}) {
  // Only ask once the user has signalled intent.
  const estimate = useApiQuery(audiobookEstimateQuery(bookId), {
    enabled: confirming,
  })

  const generateMut = useApiMutation(generateAudiobook, {
    successToast: "Narrating — this takes a while.",
    // One sentence per code, from lib/affordance.ts, rather than a
    // ternary chain that only this panel knows about (#171).
    errorToast: (err: ApiError) => messageForCode(err.code, err.message, viewer),
    onSuccess: () => setConfirming(false),
  })

  if (!isNarratableFormat(format)) {
    // A format nobody can change explains itself rather than vanishing
    // (lib/affordance.ts): the feature stays discoverable and says why
    // it does not apply here. EmptyState says the same thing at more
    // length before it ever renders this, so in practice this is the
    // re-import case — a book whose format changed under an existing
    // narration.
    const refusal = affordanceFor("FORMAT_NOT_NARRATABLE", viewer)
    if (refusal.kind === "hidden") return null
    return (
      <Button variant="outline" size="sm" disabled title={refusal.reason}>
        <Icon name="sparkle" size={14} />{" "}
        {hasExisting ? "Regenerate" : "Generate narration"}
      </Button>
    )
  }

  if (!confirming) {
    return (
      <Button variant="outline" size="sm" onClick={() => setConfirming(true)}>
        <Icon name="sparkle" size={14} />{" "}
        {hasExisting ? "Regenerate" : "Generate narration"}
      </Button>
    )
  }

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 8,
        padding: 12,
        border: "1px solid var(--color-rule-soft)",
        borderRadius: 8,
      }}
    >
      <ConfirmBody estimate={estimate.data} loading={estimate.isLoading} hasExisting={hasExisting} />
      <div style={{ display: "flex", gap: 8 }}>
        <Button
          size="sm"
          disabled={generateMut.isPending || estimate.isLoading || !!estimate.error}
          onClick={() => generateMut.mutate({ id: bookId })}
        >
          {hasExisting ? "Replace narration" : "Start"}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => setConfirming(false)}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function ConfirmBody({
  estimate,
  loading,
  hasExisting,
}: {
  estimate?: AudiobookEstimate
  loading: boolean
  hasExisting: boolean
}) {
  if (loading) return <p className="t-small" style={{ margin: 0 }}>Measuring the book…</p>
  if (!estimate) {
    return (
      <p className="t-small" style={{ margin: 0 }}>
        Could not estimate this book. Check the engine settings.
      </p>
    )
  }
  return (
    <>
      <p className="t-small" style={{ margin: 0 }}>
        <strong>{estimate.chars.toLocaleString()}</strong> characters ≈{" "}
        <strong>{formatDuration(estimate.audioSeconds)}</strong> of audio,{" "}
        <strong>{formatCost(estimate.costUsd)}</strong> at the configured rate.
      </p>
      <p className="t-small" style={{ margin: 0 }}>
        {estimate.segments} section{estimate.segments === 1 ? "" : "s"} via{" "}
        {estimate.engine} · {estimate.voice}.
      </p>
      {hasExisting && (
        <p className="t-small" style={{ margin: 0, color: "var(--color-warn, #92400e)" }}>
          This replaces the existing narration. The old audio is deleted.
        </p>
      )}
    </>
  )
}

function DeleteButton({ bookId }: { bookId: string }) {
  const [armed, setArmed] = useState(false)
  const deleteMut = useApiMutation(deleteAudiobook, {
    successToast: "Narration deleted.",
    onSuccess: () => setArmed(false),
  })

  if (!armed) {
    return (
      <Button variant="ghost" size="sm" onClick={() => setArmed(true)}>
        Delete
      </Button>
    )
  }
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        disabled={deleteMut.isPending}
        onClick={() => deleteMut.mutate(bookId)}
      >
        Delete the audio
      </Button>
      <Button variant="ghost" size="sm" onClick={() => setArmed(false)}>
        Keep
      </Button>
    </>
  )
}
