// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// decodeErr runs a writer against a recorder and returns the decoded body.
// Decoding into a struct with a string Message is the point: if a writer
// ever nests an object under "error" again, this fails to unmarshal
// instead of silently shipping a shape the TypeScript client types as a
// string.
func decodeErr(t *testing.T, write func(*gin.Context)) (int, struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	write(c)

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not the flat {error, code} shape: %v\nbody: %s", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestWriteErrorIsFlatAndOmitsCode(t *testing.T) {
	t.Parallel()

	status, body := decodeErr(t, func(c *gin.Context) {
		writeError(c, http.StatusBadRequest, "bad input")
	})

	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if body.Error != "bad input" {
		t.Errorf("error = %q, want the message", body.Error)
	}
	if body.Code != "" {
		t.Errorf("code = %q, want it absent when not supplied", body.Code)
	}
}

// The whole point of the change: a machine-readable code travels beside
// the message rather than replacing it with an object.
func TestWriteErrorCodeCarriesBothFields(t *testing.T) {
	t.Parallel()

	status, body := decodeErr(t, func(c *gin.Context) {
		writeErrorCode(c, http.StatusServiceUnavailable, CodeEmailDisabled,
			"email delivery is not configured by the admin")
	})

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if body.Error != "email delivery is not configured by the admin" {
		t.Errorf("error = %q, want the human-readable message", body.Error)
	}
	if body.Code != CodeEmailDisabled {
		t.Errorf("code = %q, want %q", body.Code, CodeEmailDisabled)
	}
}

// Codes are absent from the JSON unless set, so adding the field cannot
// change any of the ~289 existing flat responses.
func TestErrorBodyOmitsEmptyCode(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(errorBody{Error: "boom"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); got != `{"error":"boom"}` {
		t.Errorf("body = %s, want no code key at all", got)
	}
}

func TestWriteServerErrorHidesTheUnderlyingError(t *testing.T) {
	t.Parallel()

	_, body := decodeErr(t, func(c *gin.Context) {
		writeServerError(c, "op", errAnnoyinglyRevealing{})
	})

	if body.Error != "internal error" {
		t.Errorf("error = %q, want the generic message", body.Error)
	}
	// The point of writeServerError: internals stay in the log.
	if body.Error == (errAnnoyinglyRevealing{}).Error() {
		t.Error("the underlying error leaked into the response body")
	}
}

type errAnnoyinglyRevealing struct{}

func (errAnnoyinglyRevealing) Error() string {
	return `pq: relation "users" does not exist (host=10.0.0.4)`
}

// Every declared code should be a non-empty, screaming-snake identifier —
// they are a wire contract the client branches on.
func TestErrorCodesAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, code := range AllErrorCodes {
		if code == "" {
			t.Error("empty error code declared")
			continue
		}
		for _, r := range code {
			if (r < 'A' || r > 'Z') && r != '_' {
				t.Errorf("code %q should be SCREAMING_SNAKE_CASE", code)
				break
			}
		}
	}
	if len(AllErrorCodes) == 0 {
		t.Error("no error codes declared — the list is the client's contract")
	}
}

func TestErrorCodesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, code := range AllErrorCodes {
		if seen[code] {
			t.Errorf("duplicate error code %q — clients branch on these", code)
		}
		seen[code] = true
	}
}
