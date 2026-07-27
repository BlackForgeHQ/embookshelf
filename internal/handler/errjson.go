// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Error codes are the machine-readable half of the envelope: a client
// branches on these, where the message is only fit for display. They are
// a wire contract — renaming one breaks the UI's handling of that case.
//
// Declared as constants so a writer cannot typo one silently, and listed
// in AllErrorCodes so tests can enumerate them.
const (
	// CodeEmailDisabled — the admin has not configured email delivery, so
	// password reset, invites, and Send-to-Kindle are unavailable.
	CodeEmailDisabled = "EMAIL_DISABLED"
	// CodeKindleEmailUnset — the user has no kindle_email configured.
	CodeKindleEmailUnset = "KINDLE_EMAIL_UNSET"
	// CodeFormatNotSupported — the book's format is outside the
	// Send-to-Kindle eligible set (ADR-0021).
	CodeFormatNotSupported = "FORMAT_NOT_SUPPORTED"
	// CodeEmailReloadFailed — the EMAIL row saved, but rebuilding the
	// SMTP sender from it failed; the admin sees why inline.
	CodeEmailReloadFailed = "EMAIL_RELOAD_FAILED"
	// CodeSMTPError — a test send reached the SMTP server and it refused.
	CodeSMTPError = "SMTP_ERROR"
	// CodeGuidesDisabled — no LLM endpoint is configured, so reading
	// guides cannot be generated (ADR-0024).
	CodeGuidesDisabled = "GUIDES_DISABLED"
	// CodeAudiobooksDisabled — no TTS engine is configured, so narration
	// cannot be generated (ADR-0026).
	CodeAudiobooksDisabled = "AUDIOBOOKS_DISABLED"
	// CodeFormatNotNarratable — the book's format is outside the
	// Narratable set, which is EPUB alone (ADR-0028 §4). Distinct from
	// CodeFormatNotSupported, which is Send-to-Kindle's different gate.
	CodeFormatNotNarratable = "FORMAT_NOT_NARRATABLE"
)

// AllErrorCodes lists every declared code. Kept beside the constants so a
// new code is one edit, and so tests can assert their shape.
var AllErrorCodes = []string{
	CodeEmailDisabled,
	CodeKindleEmailUnset,
	CodeFormatNotSupported,
	CodeEmailReloadFailed,
	CodeSMTPError,
	CodeGuidesDisabled,
	CodeAudiobooksDisabled,
	CodeFormatNotNarratable,
}

// errorBody is the JSON shape every non-2xx response uses.
//
// It is flat by design, and that is a contract with the TS `ApiError`
// type in ui/src/api/client.ts, which reads `error` as a string. Five
// handlers used to nest `{code, message}` under `error` instead, so the
// client assigned an object into a string-typed field — the code was
// unreadable and the message rendered as "[object Object]" anywhere it
// reached a toast. Code now travels beside the message rather than
// displacing it, and is omitted entirely when absent, so the several
// hundred existing flat responses are byte-identical.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// writeError sends a JSON error envelope with the given status.
func writeError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, errorBody{Error: msg})
}

// writeErrorCode is writeError plus a machine-readable code for cases the
// client needs to branch on rather than merely display.
func writeErrorCode(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, errorBody{Error: msg, Code: code})
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
