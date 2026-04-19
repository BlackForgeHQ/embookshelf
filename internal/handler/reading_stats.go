package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// readingStatsDTO mirrors service.ReadingStats on the wire. camelCase
// for the TS client; heatmap minutes emitted as a plain int array so
// the Dashboard can feed it straight to the existing cell renderer.
type readingStatsDTO struct {
	HeatmapDays     int   `json:"heatmapDays"`
	HeatmapMinutes  []int `json:"heatmapMinutes"`
	ThisWeekMinutes int   `json:"thisWeekMinutes"`
	CurrentStreak   int   `json:"currentStreak"`
	QuarterMinutes  int   `json:"quarterMinutes"`
	QuarterSessions int   `json:"quarterSessions"`
	AllTimeMinutes  int   `json:"allTimeMinutes"`
}

// ReadingStats returns aggregate reading-session data for the current
// user. Heatmap length is configurable via ?days=<n> (clamped 7..180);
// default is 84 (12 weeks) to match the Dashboard layout.
func (h *Handler) ReadingStats(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	days, _ := strconv.Atoi(c.Query("days"))
	if days <= 0 {
		days = 84
	} else {
		days = clampInt(days, 7, 180)
	}

	s, err := h.readingStats.Collect(c.Request.Context(), userID, days)
	if err != nil {
		writeServerError(c, "reading stats", err)
		return
	}

	minutes := s.HeatmapMinutes
	if minutes == nil {
		// Never emit JSON null — the client expects an array it can
		// `.map()` over on first render.
		minutes = []int{}
	}

	c.JSON(http.StatusOK, gin.H{
		"reading": readingStatsDTO{
			HeatmapDays:     s.HeatmapDays,
			HeatmapMinutes:  minutes,
			ThisWeekMinutes: s.ThisWeekMinutes,
			CurrentStreak:   s.CurrentStreak,
			QuarterMinutes:  s.QuarterMinutes,
			QuarterSessions: s.QuarterSessions,
			AllTimeMinutes:  s.AllTimeMinutes,
		},
	})
}
