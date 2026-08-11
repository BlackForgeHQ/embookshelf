// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/blackforge/embookshelf/internal/repo"
)

// Admin "test this connection" diagnostics for the OIDC settings panel.
//
// Split from oidc.go because it shares nothing with the login flow but
// the HTTP client — it is a config checker, not part of authenticating
// anyone, and it was ~19% of a file a reader opens to answer "how does
// login work".
//
// It fetches discovery documents itself rather than going through
// getDiscovery. That is deliberate: the point of a diagnostic is to
// observe the issuer as it is right now, so a cached result would defeat
// it.

// -----------------------------------------------------------------------------
// Test Connection
// -----------------------------------------------------------------------------

// CheckStatus + TestCheck mirror the spec's diagnostic DTO.
type CheckStatus string

const (
	CheckPass CheckStatus = "PASS"
	CheckFail CheckStatus = "FAIL"
	CheckWarn CheckStatus = "WARN"
)

type TestCheck struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

type TestResult struct {
	Success bool        `json:"success"`
	Checks  []TestCheck `json:"checks"`
}

func (t *TestResult) add(name string, status CheckStatus, msg string) {
	t.Checks = append(t.Checks, TestCheck{Name: name, Status: status, Message: msg})
}

// TestProvider runs one provider's connection diagnostic. The slug
// resolves against the same registry as the login flow, and the raw
// request body is handed to the adapter, which owns its override shape
// and the blank-submission fallback (#258). This is the whole test
// endpoint: a lookup plus one call.
func (s *OIDCService) TestProvider(ctx context.Context, slug string, body []byte) (TestResult, error) {
	p, ok := s.provider(slug)
	if !ok {
		return TestResult{}, ErrOIDCUnknownProvider
	}
	return p.test(ctx, body)
}

// testGeneric runs the discovery-based checks.
func testGeneric(ctx context.Context, cfg repo.GenericOIDCConfig) TestResult {
	return testOIDCIssuer(ctx, cfg.IssuerURI, cfg.ClientID)
}

// testGoogle reuses the generic path after filling in Google's issuer.
func testGoogle(ctx context.Context, cfg repo.OAuthPresetConfig) TestResult {
	return testOIDCIssuer(ctx, "https://accounts.google.com", cfg.ClientID)
}

// testGitHub pings the fixed GitHub endpoints (no discovery doc).
func testGitHub(ctx context.Context, cfg repo.OAuthPresetConfig) TestResult {
	out := TestResult{}
	if cfg.ClientID == "" {
		out.add("Client ID", CheckFail, "client id is empty")
		return out
	}
	cli := httpClient()
	for _, ep := range []struct {
		name, url string
	}{
		{"authorize endpoint", "https://github.com/login/oauth/authorize"},
		{"user API", "https://api.github.com/user"},
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ep.url, nil)
		resp, err := cli.Do(req)
		if err != nil {
			out.add(ep.name, CheckFail, err.Error())
			continue
		}
		_ = resp.Body.Close()
		out.add(ep.name, CheckPass, fmt.Sprintf("%s reachable (%d)", ep.url, resp.StatusCode))
	}
	if cfg.ClientSecret == "" {
		out.add("client secret", CheckFail, "GitHub OAuth apps require a client secret")
	} else {
		out.add("client secret", CheckPass, "set")
	}
	out.Success = true
	for _, c := range out.Checks {
		if c.Status == CheckFail {
			out.Success = false
			break
		}
	}
	return out
}

func testOIDCIssuer(ctx context.Context, issuer, clientID string) TestResult {
	out := TestResult{}
	if strings.TrimSpace(issuer) == "" {
		out.add("Issuer URI", CheckFail, "issuer URI is empty")
		return out
	}
	if strings.TrimSpace(clientID) == "" {
		out.add("Client ID", CheckFail, "client id is empty")
		return out
	}
	cli := httpClient()
	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		out.add("Discovery", CheckFail, err.Error())
		return out
	}
	resp, err := cli.Do(req)
	if err != nil {
		out.add("Discovery", CheckFail, fmt.Sprintf("fetch %s: %v", discoveryURL, err))
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		out.add("Discovery", CheckFail, fmt.Sprintf("%s returned %d", discoveryURL, resp.StatusCode))
		return out
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		out.add("Discovery", CheckFail, err.Error())
		return out
	}
	out.add("Discovery", CheckPass, "fetched openid-configuration")

	var doc discoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		out.add("Discovery parse", CheckFail, err.Error())
		return out
	}

	for _, p := range []struct {
		name, value string
	}{
		{"authorization_endpoint", doc.AuthorizationEndpoint},
		{"token_endpoint", doc.TokenEndpoint},
		{"jwks_uri", doc.JWKSURI},
	} {
		if p.value == "" {
			out.add(p.name, CheckFail, "missing")
		} else {
			out.add(p.name, CheckPass, p.value)
		}
	}

	has := map[string]bool{}
	for _, sc := range doc.ScopesSupported {
		has[sc] = true
	}
	for _, required := range []string{"openid", "profile", "email"} {
		if has[required] {
			out.add("scope: "+required, CheckPass, "advertised")
		} else if required == "openid" {
			out.add("scope: openid", CheckFail, "issuer does not advertise openid")
		} else {
			out.add("scope: "+required, CheckWarn, "not advertised — claim mapping may fail")
		}
	}

	codeOk := false
	for _, rt := range doc.ResponseTypesSupported {
		if rt == "code" {
			codeOk = true
		}
	}
	if codeOk {
		out.add("response_type: code", CheckPass, "supported")
	} else {
		out.add("response_type: code", CheckFail, "authorization code flow not supported")
	}

	s256 := false
	for _, m := range doc.CodeChallengeMethodsSupported {
		if m == "S256" {
			s256 = true
		}
	}
	if s256 {
		out.add("PKCE S256", CheckPass, "supported")
	} else {
		out.add("PKCE S256", CheckWarn, "not advertised — BookLore sends S256 anyway")
	}

	if doc.JWKSURI != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, doc.JWKSURI, nil)
		jresp, err := cli.Do(req)
		if err != nil {
			out.add("JWKS fetch", CheckFail, err.Error())
		} else {
			defer func() { _ = jresp.Body.Close() }()
			if jresp.StatusCode != 200 {
				out.add("JWKS fetch", CheckFail, fmt.Sprintf("%s returned %d", doc.JWKSURI, jresp.StatusCode))
			} else {
				var keys struct {
					Keys []json.RawMessage `json:"keys"`
				}
				_ = json.NewDecoder(jresp.Body).Decode(&keys)
				if len(keys.Keys) == 0 {
					out.add("JWKS fetch", CheckWarn, "JWKS has no keys")
				} else {
					out.add("JWKS fetch", CheckPass, fmt.Sprintf("%d keys", len(keys.Keys)))
				}
			}
		}
	}

	out.Success = true
	for _, c := range out.Checks {
		if c.Status == CheckFail {
			out.Success = false
			break
		}
	}
	return out
}

type discoveryDoc struct {
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	JWKSURI                       string   `json:"jwks_uri"`
	ScopesSupported               []string `json:"scopes_supported"`
	ResponseTypesSupported        []string `json:"response_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}
