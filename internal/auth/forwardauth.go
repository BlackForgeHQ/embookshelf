// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
)

// forwardAuthCacheTTL is how long a resolved (subject → user) lookup
// is held in process before being re-validated against the database.
// Bounds the staleness window after a role/email change. ADR-0022.
const forwardAuthCacheTTL = 30 * time.Second

// forwardAuthCacheCap caps the in-process LRU at a size that fits
// every realistic single-instance deployment without growing
// unbounded if a misbehaving proxy injects garbage subjects.
const forwardAuthCacheCap = 1024

// ForwardAuthResolver materialises a `users` row from a trusted
// header set. Returning ErrForwardAuthRejected aborts the request
// with 401 — used when provisioning is disabled and no matching
// user exists. Any other error becomes a 500.
type ForwardAuthResolver interface {
	ResolveProxyIdentity(ctx context.Context, subject, email, name string) (model.User, error)
}

// ErrForwardAuthRejected signals "headers were valid but the
// resolver refuses to admit this identity" — typically a deliberate
// auto-provision off + no email match. The middleware surfaces it
// as 401, not 500.
var ErrForwardAuthRejected = errors.New("forward_auth: identity refused")

// ForwardAuthConfig is the runtime view the middleware needs. It
// mirrors repo.ForwardAuthConfig but pre-parses CIDRs so the hot
// path skips ParseCIDR per request. Callers (cmd/embookshelf,
// settings reload) build it via NewForwardAuthConfig.
type ForwardAuthConfig struct {
	Enabled       bool
	TrustedNets   []*net.IPNet
	UserHeader    string
	EmailHeader   string
	NameHeader    string
	GroupsHeader  string
	HideLocalForm bool
	LogoutURL     string
}

// NewForwardAuthConfig parses the persisted CIDR list once. Returns
// the same validation errors ValidateForwardAuth surfaces so admins
// get a consistent message whether config arrives via boot, settings
// save, or hot-reload.
func NewForwardAuthConfig(enabled bool, cidrs []string, userHdr, emailHdr, nameHdr, groupsHdr, logoutURL string, hideLocal bool) (*ForwardAuthConfig, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		nets = append(nets, n)
	}
	return &ForwardAuthConfig{
		Enabled:       enabled,
		TrustedNets:   nets,
		UserHeader:    userHdr,
		EmailHeader:   emailHdr,
		NameHeader:    nameHdr,
		GroupsHeader:  groupsHdr,
		HideLocalForm: hideLocal,
		LogoutURL:     logoutURL,
	}, nil
}

// ForwardAuth is the middleware. When the request's immediate TCP
// peer falls inside a trusted CIDR AND the configured user header
// is present, the middleware resolves the user (cache-first), pins
// it to the request context, and short-circuits. Otherwise it falls
// through so a chained session-cookie middleware (RequireAuth) can
// take over.
//
// X-Forwarded-For and friends are deliberately ignored — trusting a
// header-derived source address would let any caller forge identity
// from outside the trusted network. Operators must place the
// forward-auth proxy as the immediate upstream of embookshelf.
// ADR-0022.
func ForwardAuth(holder *ForwardAuthHolder, resolver ForwardAuthResolver) gin.HandlerFunc {
	cache := newForwardAuthCache(forwardAuthCacheCap, forwardAuthCacheTTL)
	return func(c *gin.Context) {
		cfg := holder.Get()
		if cfg == nil || !cfg.Enabled {
			c.Next()
			return
		}
		if !remoteAddrTrusted(c.Request.RemoteAddr, cfg.TrustedNets) {
			c.Next()
			return
		}
		subject := strings.TrimSpace(c.GetHeader(cfg.UserHeader))
		if subject == "" {
			c.Next()
			return
		}
		email := strings.ToLower(strings.TrimSpace(c.GetHeader(cfg.EmailHeader)))
		name := strings.TrimSpace(c.GetHeader(cfg.NameHeader))

		if u, ok := cache.get(subject, email); ok {
			attachForwardAuthUser(c, &u)
			c.Next()
			return
		}

		u, err := resolver.ResolveProxyIdentity(c.Request.Context(), subject, email, name)
		if err != nil {
			if errors.Is(err, ErrForwardAuthRejected) {
				unauthorized(c)
				return
			}
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		cache.put(subject, email, u)
		attachForwardAuthUser(c, &u)
		c.Next()
	}
}

// ForwardAuthAttached reports whether the request carries a user
// pinned by the forward-auth middleware. Used by CSRFGuard to skip
// the origin check on proxy-trusted requests (the trusted-IP gate
// already establishes provenance) and by the SPA `/me` endpoint to
// label the session source.
func ForwardAuthAttached(c *gin.Context) bool {
	v, ok := c.Request.Context().Value(forwardAuthCtxKey{}).(bool)
	return ok && v
}

func attachForwardAuthUser(c *gin.Context, u *model.User) {
	ctx := WithUser(c.Request.Context(), u)
	ctx = context.WithValue(ctx, forwardAuthCtxKey{}, true)
	c.Request = c.Request.WithContext(ctx)
}

type forwardAuthCtxKey struct{}

func remoteAddrTrusted(remoteAddr string, nets []*net.IPNet) bool {
	if remoteAddr == "" || len(nets) == 0 {
		return false
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ForwardAuthHolder lets cmd/embookshelf swap the runtime config
// without rebuilding the middleware. Settings save → Set → next
// request sees the new config. atomic.Pointer keeps the read path
// lock-free.
type ForwardAuthHolder struct {
	v atomic.Pointer[ForwardAuthConfig]
}

func NewForwardAuthHolder(cfg *ForwardAuthConfig) *ForwardAuthHolder {
	h := &ForwardAuthHolder{}
	h.v.Store(cfg)
	return h
}

func (h *ForwardAuthHolder) Get() *ForwardAuthConfig { return h.v.Load() }

func (h *ForwardAuthHolder) Set(cfg *ForwardAuthConfig) { h.v.Store(cfg) }

// -----------------------------------------------------------------------------
// LRU cache (subject+email → user)
// -----------------------------------------------------------------------------

type forwardAuthCacheKey struct {
	subject string
	email   string
}

type forwardAuthCacheEntry struct {
	user      model.User
	expiresAt time.Time
}

type forwardAuthCache struct {
	mu  sync.Mutex
	m   map[forwardAuthCacheKey]forwardAuthCacheEntry
	cap int
	ttl time.Duration
}

func newForwardAuthCache(capN int, ttl time.Duration) *forwardAuthCache {
	return &forwardAuthCache{
		m:   make(map[forwardAuthCacheKey]forwardAuthCacheEntry, capN),
		cap: capN,
		ttl: ttl,
	}
}

func (c *forwardAuthCache) get(subject, email string) (model.User, bool) {
	k := forwardAuthCacheKey{subject: subject, email: email}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return model.User{}, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.m, k)
		return model.User{}, false
	}
	return e.user, true
}

func (c *forwardAuthCache) put(subject, email string, u model.User) {
	k := forwardAuthCacheKey{subject: subject, email: email}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.cap {
		// Evict an arbitrary expired entry first; if none, drop one
		// random key. The bound is a safety rail against pathological
		// header values, not a hot-path optimisation, so a true LRU
		// would be over-engineered.
		now := time.Now()
		evicted := false
		for kk, ee := range c.m {
			if now.After(ee.expiresAt) {
				delete(c.m, kk)
				evicted = true
				break
			}
		}
		if !evicted {
			for kk := range c.m {
				delete(c.m, kk)
				break
			}
		}
	}
	c.m[k] = forwardAuthCacheEntry{user: u, expiresAt: time.Now().Add(c.ttl)}
}
