import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react"

import type { Chapter } from "@/api/books"

export type AudioProgress = {
  percent: number // 0..1, normalized to duration
  seconds: number // current playback position
  duration: number // total duration in seconds (0 until metadata loads)
}

export type AudioReaderHandle = {
  play: () => void
  pause: () => void
  toggle: () => void
  seekTo: (seconds: number) => void
  skip: (delta: number) => void
  setRate: (rate: number) => void
}

type Props = {
  url: string
  initialSeconds?: number
  initialRate?: number
  chapters?: Array<Chapter>
  // Book metadata for the OS-level Media Session UI (lockscreen,
  // headphones, CarPlay-style integrations). All optional.
  title?: string
  author?: string
  artworkURL?: string
  onReady?: (meta: { duration: number }) => void
  onProgress?: (p: AudioProgress) => void
  onChapterChange?: (chapterIndex: number) => void
  // Reports the element's real play state. Emitted from the element's own
  // play/pause events rather than from whoever called toggle(), so playback
  // started or stopped from outside the page — a headphone button, the lock
  // screen, a media key, or the browser pausing us — still reaches the shell.
  onPlayingChange?: (playing: boolean) => void
  onError?: (err: unknown) => void
}

// AudioReader wraps an HTML5 <audio> element with the controls + Media
// Session integration the audiobook reader shell needs. It deliberately
// owns NO chrome (play/pause buttons, scrubber, chapter list) — the
// shell composes those around it. The component emits progress events
// every ~250 ms during playback so the shell's debounced persist matches
// the cadence other readers use.
export const AudioReader = forwardRef<AudioReaderHandle, Props>(
  function AudioReaderImpl(
    {
      url,
      initialSeconds,
      initialRate = 1,
      chapters,
      title,
      author,
      artworkURL,
      onReady,
      onProgress,
      onChapterChange,
      onPlayingChange,
      onError,
    },
    ref
  ) {
    const audioRef = useRef<HTMLAudioElement | null>(null)
    const [duration, setDuration] = useState(0)
    const [chapterIndex, setChapterIndex] = useState(-1)
    // Track the seek-to-initial step separately so an external seekTo() call
    // on a freshly-mounted element doesn't fight the resume logic.
    const initialApplied = useRef(false)
    const onProgressRef = useRef(onProgress)
    onProgressRef.current = onProgress
    const onChapterChangeRef = useRef(onChapterChange)
    onChapterChangeRef.current = onChapterChange
    const onPlayingChangeRef = useRef(onPlayingChange)
    onPlayingChangeRef.current = onPlayingChange

    useImperativeHandle(
      ref,
      () => ({
        play: () => {
          void audioRef.current?.play().catch((err) => onError?.(err))
        },
        pause: () => audioRef.current?.pause(),
        toggle: () => {
          const a = audioRef.current
          if (!a) return
          if (a.paused) void a.play().catch((err) => onError?.(err))
          else a.pause()
        },
        seekTo: (seconds: number) => {
          const a = audioRef.current
          if (!a) return
          const clamped = Math.max(0, Math.min(duration || a.duration || 0, seconds))
          a.currentTime = clamped
        },
        skip: (delta: number) => {
          const a = audioRef.current
          if (!a) return
          a.currentTime = Math.max(
            0,
            Math.min(duration || a.duration || 0, a.currentTime + delta)
          )
        },
        setRate: (rate: number) => {
          const a = audioRef.current
          if (!a) return
          a.playbackRate = rate
        },
      }),
      // eslint-disable-next-line react-hooks/exhaustive-deps
      [duration]
    )

    // Set up Media Session metadata once we know the title/author. The
    // browser drops the metadata when the audio element pauses for too
    // long, but it'll re-pick-up on the next play().
    useEffect(() => {
      if (!("mediaSession" in navigator)) return
      const ms = navigator.mediaSession
      try {
        ms.metadata = new MediaMetadata({
          title: title ?? "",
          artist: author ?? "",
          album: title ?? "",
          artwork: artworkURL
            ? [{ src: artworkURL, sizes: "512x512", type: "image/jpeg" }]
            : [],
        })
        ms.setActionHandler("play", () => audioRef.current?.play())
        ms.setActionHandler("pause", () => audioRef.current?.pause())
        ms.setActionHandler("seekbackward", (e) => {
          const a = audioRef.current
          if (!a) return
          a.currentTime = Math.max(0, a.currentTime - (e.seekOffset ?? 15))
        })
        ms.setActionHandler("seekforward", (e) => {
          const a = audioRef.current
          if (!a) return
          a.currentTime = Math.min(
            a.duration || 0,
            a.currentTime + (e.seekOffset ?? 30)
          )
        })
      } catch {
        // Older browsers without all action handlers — fail open.
      }
    }, [title, author, artworkURL])

    // Reset when the URL changes (e.g. switching audiobooks within the
    // same shell mount).
    useEffect(() => {
      setDuration(0)
      setChapterIndex(-1)
      initialApplied.current = false
    }, [url])

    return (
      <audio
        ref={audioRef}
        src={url}
        preload="metadata"
        playsInline
        // crossOrigin not needed — the file endpoint is same-origin via
        // the Vite proxy in dev and the embedded SPA in prod.
        onLoadedMetadata={(e) => {
          const a = e.currentTarget
          const d = Number.isFinite(a.duration) ? a.duration : 0
          setDuration(d)
          onReady?.({ duration: d })
          if (initialRate !== 1) a.playbackRate = initialRate
          if (
            !initialApplied.current &&
            initialSeconds !== undefined &&
            initialSeconds > 0 &&
            initialSeconds < d
          ) {
            a.currentTime = initialSeconds
          }
          initialApplied.current = true
        }}
        onTimeUpdate={(e) => {
          const a = e.currentTarget
          if (!a.duration || !Number.isFinite(a.duration)) return
          const seconds = a.currentTime
          const percent = a.duration > 0 ? seconds / a.duration : 0
          onProgressRef.current?.({
            seconds,
            duration: a.duration,
            percent,
          })
          // Compute current chapter purely client-side. Chapters are a
          // sorted list — a small linear scan is fine; a binary search
          // would optimize past 50+ chapters but no audiobook has that.
          if (chapters && chapters.length > 0) {
            let idx = -1
            for (let i = 0; i < chapters.length; i++) {
              const c = chapters[i]
              if (!c) break
              if (seconds >= c.startS) idx = i
              else break
            }
            if (idx !== chapterIndex) {
              setChapterIndex(idx)
              onChapterChangeRef.current?.(idx)
            }
          }
        }}
        onPlay={() => onPlayingChangeRef.current?.(true)}
        onPause={() => onPlayingChangeRef.current?.(false)}
        onEnded={() => onPlayingChangeRef.current?.(false)}
        onError={(e) => onError?.(new Error(`Audio error: ${e.type}`))}
        style={{ display: "none" }}
      />
    )
  }
)
