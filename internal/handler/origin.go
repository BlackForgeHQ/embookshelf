// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// "What is my public origin?" has exactly one answer per request, and
// this file is where it lives. It used to be answered separately by the
// OIDC login flow, the admin panel's display of the redirect URI to
// register at the IdP, and the OPDS feed base — and the answers
// differed. Behind a proxy that rewrites the Host header, the panel
// told the admin to register one redirect URI while the login flow sent
// another, and the IdP rejected the login with redirect_uri_mismatch
// and no explanation visible inside embookshelf.

// requestOrigin returns "scheme://host" inferred from the incoming
// request. Honors X-Forwarded-Proto and X-Forwarded-Host so reverse-
// proxy deployments that forgot to set APP_URL still get a usable
// redirect_uri. Used as a fallback when cfg.AppURL is empty.
//
// These headers are not an authentication signal, so they carry no
// trust gate: ADR-0022 reserves the trusted-proxy CIDR check for the
// identity headers that decide who the caller is. Getting the origin
// wrong produces a broken link or a redirect_uri the IdP refuses; it
// never admits anybody. Links that leave the request — the ones
// embedded in outbound email — deliberately do not come from here and
// use the configured public URL instead, because a spoofed host in a
// password-reset mail is a phish vector.
func requestOrigin(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if xf := c.GetHeader("X-Forwarded-Proto"); xf != "" {
		// Proto header can be comma-separated; first hop wins.
		if i := strings.IndexByte(xf, ','); i >= 0 {
			xf = xf[:i]
		}
		scheme = strings.TrimSpace(xf)
	}
	host := c.Request.Host
	if xh := c.GetHeader("X-Forwarded-Host"); xh != "" {
		if i := strings.IndexByte(xh, ','); i >= 0 {
			xh = xh[:i]
		}
		host = strings.TrimSpace(xh)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}
