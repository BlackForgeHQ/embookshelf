// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Healthcheck answers whether this process can serve. It used to return
// a static ok, which told an orchestrator nothing it did not already know
// from the port being open — a process with a dead database passed.
func (h *Handler) Healthcheck(c *gin.Context) {
	if h.platform == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "no database probe configured",
		})
		return
	}
	if _, err := h.platform.Probe(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "database unreachable",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
