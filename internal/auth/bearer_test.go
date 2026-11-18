// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

type fakeTokenResolver struct {
	users map[string]model.User
	err   error
	seen  []string
}

func (f *fakeTokenResolver) UserByToken(_ context.Context, raw string) (model.User, error) {
	f.seen = append(f.seen, raw)
	if f.err != nil {
		return model.User{}, f.err
	}
	u, ok := f.users[raw]
	if !ok {
		return model.User{}, ErrInvalidToken
	}
	return u, nil
}

func bearerRequest(t *testing.T, resolver TokenResolver, header string) (*httptest.ResponseRecorder, *model.User) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var got *model.User
	r := gin.New()
	r.GET("/mcp", BearerToken(resolver), func(c *gin.Context) {
		got = UserFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec, got
}

func TestBearerTokenAttachesTheOwner(t *testing.T) {
	res := &fakeTokenResolver{users: map[string]model.User{"ebk_good": {ID: "u1"}}}

	for _, header := range []string{"Bearer ebk_good", "bearer ebk_good", "  Bearer   ebk_good  "} {
		rec, u := bearerRequest(t, res, header)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%q: status %d, want 204", header, rec.Code)
		}
		if u == nil || u.ID != "u1" {
			t.Fatalf("%q: context user = %v, want u1", header, u)
		}
	}
}

func TestBearerTokenChallengesWithoutAUsableCredential(t *testing.T) {
	res := &fakeTokenResolver{users: map[string]model.User{}}
	cases := map[string]string{
		"absent":          "",
		"basic scheme":    "Basic dXNlcjpwYXNz",
		"scheme only":     "Bearer",
		"blank token":     "Bearer   ",
		"unknown token":   "Bearer ebk_nope",
		"token no scheme": "ebk_good",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			rec, u := bearerRequest(t, res, header)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="embookshelf"` {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
			if u != nil {
				t.Fatal("handler ran after a refused credential")
			}
		})
	}
}

func TestBearerTokenDoesNotReadTheSessionCookie(t *testing.T) {
	res := &fakeTokenResolver{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/mcp", BearerToken(res), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "a-live-session"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only request: status %d, want 401", rec.Code)
	}
	if len(res.seen) != 0 {
		t.Fatalf("resolver consulted with %v for a cookie-only request", res.seen)
	}
}

// An outage must not look like a bad token: a 401 tells the client to
// discard a credential that works.
func TestBearerTokenAnswersAnOutageWith500(t *testing.T) {
	res := &fakeTokenResolver{err: errors.New("connection refused")}
	rec, u := bearerRequest(t, res, "Bearer ebk_good")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("an outage carried an auth challenge")
	}
	if u != nil {
		t.Fatal("handler ran during an outage")
	}
}
