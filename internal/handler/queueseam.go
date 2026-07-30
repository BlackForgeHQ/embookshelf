// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/queue"
)

// requireQueue resolves the worker-pool seam for a route whose work is
// dispatched rather than done inline. Options documents the pool as
// legitimately nil, so every dispatch site has to answer for its
// absence, and the answer is the same at each of them: 503, the work was
// not accepted.
//
// The degrade lives here rather than being restated per call site
// because restating it is how it drifted — two dispatch sites carried
// the check and Send-to-Kindle dereferenced the nil pool, which is a
// panic on that route instead of a degrade (#223).
//
// A caller gets a pool it can use or false and a response already
// written; the nil never leaves this function.
func (h *Handler) requireQueue(c *gin.Context) (queue.Client, bool) {
	if h.queue == nil {
		writeError(c, http.StatusServiceUnavailable, "queue unavailable")
		return nil, false
	}
	return h.queue, true
}
