package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

type fakeVerifier struct {
	user      model.User
	err       error
	gotEmail  string
	gotPasswd string
}

func (f *fakeVerifier) Verify(_ context.Context, email, password string) (model.User, error) {
	f.gotEmail = email
	f.gotPasswd = password
	return f.user, f.err
}

func basicHeader(email, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+password))
}

func TestBasicAuthMissingHeader(t *testing.T) {
	v := &fakeVerifier{err: errors.New("never called")}
	r := newRouter(BasicAuth(v, "test-realm"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, `realm="test-realm"`) {
		t.Errorf("WWW-Authenticate = %q", got)
	}
}

func TestBasicAuthInvalidBase64(t *testing.T) {
	r := newRouter(BasicAuth(&fakeVerifier{}, "r"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic !!!not-base64!!!")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestBasicAuthMissingColon(t *testing.T) {
	r := newRouter(BasicAuth(&fakeVerifier{}, "r"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("nocolon")))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestBasicAuthVerifyFails(t *testing.T) {
	v := &fakeVerifier{err: errors.New("bad creds")}
	r := newRouter(BasicAuth(v, "r"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", basicHeader("a@b.c", "wrong"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if v.gotEmail != "a@b.c" || v.gotPasswd != "wrong" {
		t.Errorf("verifier saw email=%q passwd=%q", v.gotEmail, v.gotPasswd)
	}
}

func TestBasicAuthSuccessAttachesUser(t *testing.T) {
	want := model.User{ID: "u1", Email: "a@b.c", Role: model.RoleUser}
	v := &fakeVerifier{user: want}
	r := newRouter(BasicAuth(v, "r"))

	var seen *model.User
	r.GET("/", func(c *gin.Context) {
		seen = UserFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", basicHeader("a@b.c", "pw"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if seen == nil || seen.ID != "u1" {
		t.Fatalf("user in context = %+v, want id=u1", seen)
	}
}

func TestBasicAuthHandlesPasswordWithColon(t *testing.T) {
	v := &fakeVerifier{user: model.User{ID: "u1"}}
	r := newRouter(BasicAuth(v, "r"))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", basicHeader("a@b.c", "pa:ss:word"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if v.gotPasswd != "pa:ss:word" {
		t.Errorf("password parsed as %q, want %q", v.gotPasswd, "pa:ss:word")
	}
}
