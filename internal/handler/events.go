// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
)

// Events streams the SSE hub to a single client. Every connection:
//
//  1. Registers with the hub and tears down on client disconnect.
//  2. Sends a silent `: ping` comment every heartbeatInterval so idle
//     proxies (nginx/caddy/cloudflare) don't close the stream at 60 s.
//  3. Forwards each published event verbatim — the payload is already
//     JSON-encoded by the service layer.
//
// The handler is cookie-authed via the `authed` group but deliberately
// sits OUTSIDE any CSRF guard — GETs are safe methods and browsers refuse
// to attach `Origin` to an EventSource request anyway.
func (h *Handler) Events(c *gin.Context) {
	if h.hub == nil {
		writeError(c, http.StatusServiceUnavailable, "realtime hub unavailable")
		return
	}

	// Headers MUST be set before the first write: any intermediate proxy
	// that sees `text/html` once will refuse to switch to streaming mode.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // hint for nginx
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	// The subscription is bound to the authenticated user so the hub can
	// route user-scoped events (Send-to-Kindle results) to their owner
	// instead of fanning them out to every connected browser.
	var userID string
	if u := auth.UserFromContext(c.Request.Context()); u != nil {
		userID = u.ID
	}

	ch, cancel := h.hub.Subscribe(userID, 32)
	defer cancel()

	_, _ = fmt.Fprint(c.Writer, ": connected\n\n")
	c.Writer.Flush()

	const heartbeatInterval = 25 * time.Second
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// SSE frame: `event: name\ndata: payload\n\n`. Multi-line
			// data would need per-line prefixing; service-layer payloads
			// are compact JSON so a single `data:` line is enough today.
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Name, ev.Data); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
