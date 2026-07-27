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
import { meQuery } from "@/api/auth"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { Button } from "@/components/ui/button"
import { Icon } from "@/components/Icon"

// Only EPUB carries text an engine can read. The first of the Narratable
// format's three gates — the handler and the worker hold the other two,
// because a re-import can change a book's format between them.
const NARRATABLE = new Set(["EPUB"])

function isRunning(a: Audiobook): boolean {
  return a.state === "pending" || a.state === "running"
}

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
  const me = useApiQuery(meQuery)
  const isAdmin = me.data?.role === "admin"

  const audiobook = useApiQuery(bookAudiobookQuery(bookId), {
    // Poll only while something is actually moving, and stop on its own
    // once it is not — the same self-terminating shape the guide run
    // uses, so an idle instance is never polled.
    refetchInterval: (q) => {
      const d = q.state.data
      return d && isRunning(d) ? 4000 : false
    },
  })

  const [confirming, setConfirming] = useState(false)

  if (audiobook.isLoading) return <p className="t-small">Loading…</p>

  const a = audiobook.data

  if (!a) {
    return (
      <EmptyState
        bookId={bookId}
        format={format}
        isAdmin={isAdmin}
        confirming={confirming}
        setConfirming={setConfirming}
      />
    )
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 14, maxWidth: 640 }}>
      {isRunning(a) ? (
        <RunProgress audiobook={a} bookId={bookId} isAdmin={isAdmin} />
      ) : a.state === "ready" ? (
        <ReadyState audiobook={a} bookId={bookId} />
      ) : (
        <StoppedState audiobook={a} bookId={bookId} isAdmin={isAdmin} />
      )}

      <Provenance audiobook={a} />

      {isAdmin && !isRunning(a) && (
        <div style={{ display: "flex", gap: 8 }}>
          <RegenerateButton
            bookId={bookId}
            format={format}
            confirming={confirming}
            setConfirming={setConfirming}
            hasExisting={a.state === "ready"}
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
  isAdmin,
  confirming,
  setConfirming,
}: {
  bookId: string
  format: string
  isAdmin: boolean
  confirming: boolean
  setConfirming: (v: boolean) => void
}) {
  if (!NARRATABLE.has(format.toUpperCase())) {
    return (
      <p className="t-small">
        Only EPUB books can be narrated — this one is {format}. Text has to
        come from somewhere, and no other format in the library carries any.
      </p>
    )
  }
  if (!isAdmin) {
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
  bookId,
  isAdmin,
}: {
  audiobook: Audiobook
  bookId: string
  isAdmin: boolean
}) {
  const cancelMut = useApiMutation(cancelAudiobook, {
    successToast: "Narration cancelled.",
  })

  const pct =
    audiobook.segmentsTotal > 0
      ? Math.round((audiobook.segmentsDone / audiobook.segmentsTotal) * 100)
      : 0

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
      <div
        aria-label="Narration progress"
        style={{
          height: 6,
          borderRadius: 3,
          background: "var(--color-rule-soft)",
          overflow: "hidden",
        }}
      >
        <div
          style={{
            width: `${pct}%`,
            height: "100%",
            background: "var(--color-accent, #0f766e)",
            transition: "width .4s ease",
          }}
        />
      </div>
      <p className="t-small" style={{ marginTop: 8 }}>
        This runs in the background — you can leave this page.
      </p>
      {isAdmin && (
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
  bookId,
  isAdmin,
}: {
  audiobook: Audiobook
  bookId: string
  isAdmin: boolean
}) {
  const retryMut = useApiMutation(retryAudiobook, {
    successToast: "Picking up where it stopped.",
  })

  const failed = audiobook.state === "failed"
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
      {isAdmin && failed && (
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
  if (audiobook.state === "pending") return null
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
  confirming,
  setConfirming,
  hasExisting,
}: {
  bookId: string
  format: string
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
    errorToast: (err: ApiError) =>
      err.code === "AUDIOBOOKS_DISABLED"
        ? "No text-to-speech engine is configured. An admin can set one up in Settings."
        : err.code === "FORMAT_NOT_NARRATABLE"
          ? "Only EPUB books can be narrated."
          : err.message,
    onSuccess: () => setConfirming(false),
  })

  if (!NARRATABLE.has(format.toUpperCase())) return null

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
