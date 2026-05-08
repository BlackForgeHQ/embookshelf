package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sse"
)

// SendToKindleArgs is the payload for one Send-to-Kindle delivery.
// BookID + UserID are the only inputs — Notifier re-fetches both
// rows so a stale snapshot can't ship the wrong attachment.
type SendToKindleArgs struct {
	BookID string `json:"book_id"`
	UserID string `json:"user_id"`
}

// Kind is the stable job name shared by River and the SQLite queue.
func (SendToKindleArgs) Kind() string { return "kindle.send" }

// SendToKindleDeps groups the seams the worker needs. Notifier owns
// the email build; Books + Users re-validate the row at run time so
// a re-import race or kindle-email change between enqueue and
// dispatch lands the right state.
type SendToKindleDeps struct {
	Notifier *service.Notifier
	Books    *repo.BookRepo
	Users    *repo.UserRepo
	Hub      *sse.Hub
}

// SendToKindle is the worker-side entry point. Failures broadcast a
// kindle.failed SSE event to the originating user; success
// broadcasts kindle.sent. River retries transient errors via its
// own backoff; permanent errors (eligibility, kindle email unset)
// return nil after the SSE so the job doesn't queue forever. ADR-0021.
func SendToKindle(ctx context.Context, args SendToKindleArgs, deps SendToKindleDeps) error {
	user, err := deps.Users.GetByID(ctx, args.UserID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	book, err := deps.Books.GetByID(ctx, user.ID, args.BookID)
	if err != nil {
		return fmt.Errorf("load book: %w", err)
	}

	sendErr := deps.Notifier.SendToKindle(ctx, book, user)
	if sendErr == nil {
		broadcastKindleEvent(deps.Hub, user.ID, book.ID, "kindle.sent", "")
		return nil
	}

	// Permanent: don't retry — the next attempt would fail the same way.
	if errors.Is(sendErr, service.ErrFormatNotSupported) ||
		errors.Is(sendErr, service.ErrFileTooLarge) ||
		errors.Is(sendErr, service.ErrKindleEmailUnset) ||
		errors.Is(sendErr, service.ErrEmailDisabled) {
		broadcastKindleEvent(deps.Hub, user.ID, book.ID, "kindle.failed", sendErr.Error())
		slog.Info("send-to-kindle permanent failure", "book_id", book.ID, "user_id", user.ID, "err", sendErr)
		return nil
	}

	broadcastKindleEvent(deps.Hub, user.ID, book.ID, "kindle.failed", sendErr.Error())
	return sendErr
}

func broadcastKindleEvent(hub *sse.Hub, userID, bookID, eventName, errMsg string) {
	if hub == nil {
		return
	}
	payload := map[string]any{
		"user_id": userID,
		"book_id": bookID,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	hub.Broadcast(sse.Event{Name: eventName, Data: string(data)})
}

// SendToKindleWorker is the River adapter.
type SendToKindleWorker struct {
	river.WorkerDefaults[SendToKindleArgs]
	Deps SendToKindleDeps
}

func (w *SendToKindleWorker) Work(ctx context.Context, job *river.Job[SendToKindleArgs]) error {
	return SendToKindle(ctx, job.Args, w.Deps)
}
