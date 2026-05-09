// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

type fakeProxyResolver struct {
	user     model.User
	err      error
	calls    int
	gotSubj  string
	gotEmail string
	gotName  string
}

func (f *fakeProxyResolver) ResolveProxyIdentity(_ context.Context, subject, email, name string) (model.User, error) {
	f.calls++
	f.gotSubj = subject
	f.gotEmail = email
	f.gotName = name
	return f.user, f.err
}

func mustHolder(t *testing.T, enabled bool, cidrs []string) *ForwardAuthHolder {
	t.Helper()
	cfg, err := NewForwardAuthConfig(enabled, cidrs, "Remote-User", "Remote-Email", "Remote-Name", "Remote-Groups", "", false)
	if err != nil {
		t.Fatalf("NewForwardAuthConfig: %v", err)
	}
	return NewForwardAuthHolder(cfg)
}

func TestForwardAuthDisabledFallsThrough(t *testing.T) {
	holder := mustHolder(t, false, nil)
	resolver := &fakeProxyResolver{}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) {
		if ForwardAuthAttached(c) {
			t.Errorf("attached on disabled config")
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Remote-User", "bohdan")
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if resolver.calls != 0 {
		t.Errorf("resolver called when disabled")
	}
}

func TestForwardAuthRejectsUntrustedSourceIP(t *testing.T) {
	holder := mustHolder(t, true, []string{"10.0.0.0/8"})
	resolver := &fakeProxyResolver{user: model.User{ID: "u1"}}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) {
		if ForwardAuthAttached(c) {
			t.Errorf("attached for untrusted source")
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Remote-User", "bohdan")
	req.RemoteAddr = "8.8.8.8:5555" // not in 10/8
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if resolver.calls != 0 {
		t.Errorf("resolver called for untrusted source")
	}
}

func TestForwardAuthIgnoresXForwardedFor(t *testing.T) {
	// Attacker spoofs XFF claiming to be the trusted proxy. We must
	// not honor it — the gate is RemoteAddr only.
	holder := mustHolder(t, true, []string{"10.0.0.0/8"})
	resolver := &fakeProxyResolver{user: model.User{ID: "u1"}}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Remote-User", "bohdan")
	req.Header.Set("X-Forwarded-For", "10.0.0.42")
	req.Header.Set("X-Real-IP", "10.0.0.42")
	req.RemoteAddr = "8.8.8.8:5555"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if resolver.calls != 0 {
		t.Errorf("XFF spoof was honored — resolver called")
	}
}

func TestForwardAuthAttachesUserOnTrustedHit(t *testing.T) {
	holder := mustHolder(t, true, []string{"10.0.0.0/8"})
	resolver := &fakeProxyResolver{user: model.User{ID: "u1", Email: "b@x.com"}}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) {
		if !ForwardAuthAttached(c) {
			t.Errorf("not attached")
		}
		u := UserFromContext(c.Request.Context())
		if u == nil || u.ID != "u1" {
			t.Errorf("user not pinned: %#v", u)
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Remote-User", "bohdan")
	req.Header.Set("Remote-Email", "B@X.com")
	req.Header.Set("Remote-Name", "Bohdan S")
	req.RemoteAddr = "10.0.0.5:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d", resolver.calls)
	}
	if resolver.gotSubj != "bohdan" || resolver.gotEmail != "b@x.com" || resolver.gotName != "Bohdan S" {
		t.Errorf("subj=%q email=%q name=%q", resolver.gotSubj, resolver.gotEmail, resolver.gotName)
	}
}

func TestForwardAuthRejectsPropagatesAs401(t *testing.T) {
	holder := mustHolder(t, true, []string{"127.0.0.0/8"})
	resolver := &fakeProxyResolver{err: ErrForwardAuthRejected}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Remote-User", "bohdan")
	req.RemoteAddr = "127.0.0.1:1111"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestForwardAuthCachesResolution(t *testing.T) {
	holder := mustHolder(t, true, []string{"127.0.0.0/8"})
	resolver := &fakeProxyResolver{user: model.User{ID: "u1"}}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Remote-User", "bohdan")
		req.Header.Set("Remote-Email", "b@x.com")
		req.RemoteAddr = "127.0.0.1:1111"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
	if resolver.calls != 1 {
		t.Errorf("expected 1 resolver call (cached), got %d", resolver.calls)
	}
}

func TestForwardAuthHolderHotSwap(t *testing.T) {
	holder := mustHolder(t, false, nil)
	resolver := &fakeProxyResolver{user: model.User{ID: "u1"}}
	r := newRouter(ForwardAuth(holder, resolver))
	r.GET("/", func(c *gin.Context) {
		if ForwardAuthAttached(c) {
			c.Status(http.StatusTeapot)
			return
		}
		c.Status(http.StatusOK)
	})

	// First request: disabled, falls through.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("Remote-User", "bohdan")
	req1.RemoteAddr = "127.0.0.1:1111"
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("status1 = %d", w1.Code)
	}

	// Hot-swap to enabled.
	cfg, err := NewForwardAuthConfig(true, []string{"127.0.0.0/8"}, "Remote-User", "Remote-Email", "", "", "", false)
	if err != nil {
		t.Fatalf("rebuild cfg: %v", err)
	}
	holder.Set(cfg)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Remote-User", "bohdan")
	req2.RemoteAddr = "127.0.0.1:1111"
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTeapot {
		t.Fatalf("status2 = %d, want 418 (attached)", w2.Code)
	}
}
