package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// errorBody is the JSON shape every non-2xx response uses. Keeping it flat
// (single `error` key) matches the TS `ApiError` type in
// frontend/src/api/client.ts.
type errorBody struct {
	Error string `json:"error"`
}

// writeError sends a JSON error envelope with the given status.
func writeError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, errorBody{Error: msg})
}

// writeServerError logs the underlying error and returns a generic 500. The
// raw error is intentionally withheld from the response body — anything
// revealing (stack, SQL text, internal paths) stays in the log.
func writeServerError(c *gin.Context, op string, err error) {
	slog.Error(op, "err", err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, errorBody{Error: "internal error"})
}

// bindJSON decodes the request body and responds with a 400 on failure.
// Returns ok=false when the caller should stop.
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		writeError(c, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// errIsOneOf is a small helper to branch cleanly on multiple sentinels.
func errIsOneOf(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
