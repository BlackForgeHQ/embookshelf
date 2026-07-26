// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// --- the allow-list, now path-aware -------------------------------------

func TestCoverURLAllowedHostRules(t *testing.T) {
	tests := map[string]bool{
		"https://books.google.com/x.jpg":              true,
		"https://covers.openlibrary.org/b/id/1-L.jpg": true,
		"https://i.gr-assets.com/x.jpg":               true, // suffix entry
		"https://s.gr-assets.com/x.jpg":               true,
		"https://gr-assets.com.evil.io/x.jpg":         false, // suffix must not be a substring match
		"https://evil.example.com/x.jpg":              false,
	}
	for raw, want := range tests {
		if got := coverURLAllowed(mustURL(t, raw)); got != want {
			t.Errorf("coverURLAllowed(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestCoverURLAllowedHonoursPathPrefix — an entry carrying a prefix admits
// only that path, so a host shared by many tenants cannot be used wholesale.
func TestCoverURLAllowedHonoursPathPrefix(t *testing.T) {
	orig := AllowedCoverHosts
	t.Cleanup(func() { AllowedCoverHosts = orig })
	AllowedCoverHosts = map[string]coverHostRule{
		"shared.example.com": {Prefix: "/tenant/"},
	}

	if !coverURLAllowed(mustURL(t, "https://shared.example.com/tenant/cover.jpg")) {
		t.Error("the permitted prefix was rejected")
	}
	for _, raw := range []string{
		"https://shared.example.com/other/cover.jpg",
		"https://shared.example.com/cover.jpg",
		// Traversal out of the prefix must not survive cleaning.
		"https://shared.example.com/tenant/../other/cover.jpg",
	} {
		if coverURLAllowed(mustURL(t, raw)) {
			t.Errorf("%q cleared a prefix it does not match", raw)
		}
	}

	// Cleaning INTO the prefix is fine — the server resolves it the same
	// way, so the request really does land under /tenant/.
	if !coverURLAllowed(mustURL(t, "https://shared.example.com/../tenant/cover.jpg")) {
		t.Error("a path that cleans into the prefix was rejected")
	}
}

// TestCoverURLAllowedEmptyPrefixAdmitsAnyPath keeps the existing entries
// behaving as they did before prefixes existed.
func TestCoverURLAllowedEmptyPrefixAdmitsAnyPath(t *testing.T) {
	if !coverURLAllowed(mustURL(t, "https://books.google.com/any/deep/path.jpg")) {
		t.Error("a host with no prefix rule should admit any path")
	}
}

func TestCoverURLAllowedRequiresHTTPS(t *testing.T) {
	for _, raw := range []string{
		"http://books.google.com/x.jpg",
		"ftp://books.google.com/x.jpg",
	} {
		if coverURLAllowed(mustURL(t, raw)) {
			t.Errorf("%q cleared the scheme check", raw)
		}
	}
}

// --- the redirect escape -------------------------------------------------

// TestCoverRedirectPolicyRejectsEscape is the defect this covers. The host
// allow-list was applied to the URL the caller supplied and to nothing else,
// while the production client used Go's default policy — up to 10 hops,
// scheme downgrade permitted. Any authenticated user could name an
// allow-listed host they control (storage.googleapis.com admits every GCS
// bucket) and have it redirect the server to an internal address.
func TestCoverRedirectPolicyRejectsEscape(t *testing.T) {
	policy := coverRedirectPolicy()
	via := []*http.Request{{URL: mustURL(t, "https://storage.googleapis.com/b/x.jpg")}}

	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",  // link-local, scheme downgrade
		"https://169.254.169.254/latest/meta-data/", // link-local over https
		"http://127.0.0.1:6060/api/v1/settings",     // loopback back into ourselves
		"https://evil.example.com/x.jpg",            // simply off the list
		"http://books.google.com/x.jpg",             // allow-listed host, downgraded
	} {
		req := &http.Request{URL: mustURL(t, target)}
		if err := policy(req, via); err == nil {
			t.Errorf("redirect to %q was permitted", target)
		}
	}
}

func TestCoverRedirectPolicyAllowsListedHost(t *testing.T) {
	policy := coverRedirectPolicy()
	via := []*http.Request{{URL: mustURL(t, "https://books.google.com/x.jpg")}}
	req := &http.Request{URL: mustURL(t, "https://books.googleusercontent.com/x.jpg")}
	if err := policy(req, via); err != nil {
		t.Fatalf("a legitimate CDN redirect was blocked: %v", err)
	}
}

// TestCoverRedirectPolicyCapsHops — an allow-listed host that redirects to
// another allow-listed host forever would otherwise spin until Go's own
// limit, holding a connection and a goroutine.
func TestCoverRedirectPolicyCapsHops(t *testing.T) {
	policy := coverRedirectPolicy()
	req := &http.Request{URL: mustURL(t, "https://books.google.com/x.jpg")}

	via := make([]*http.Request, 0, maxCoverRedirects+1)
	for i := 0; i < maxCoverRedirects; i++ {
		via = append(via, &http.Request{URL: mustURL(t, "https://books.google.com/x.jpg")})
		if err := policy(req, via); err != nil && i < maxCoverRedirects-1 {
			t.Fatalf("hop %d rejected early: %v", i+1, err)
		}
	}
	via = append(via, &http.Request{URL: mustURL(t, "https://books.google.com/x.jpg")})
	if err := policy(req, via); err == nil {
		t.Fatalf("no cap: %d hops permitted", len(via))
	} else if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("unhelpful cap error: %v", err)
	}
}

// --- the wiring ----------------------------------------------------------

// TestNewEnrichmentServiceInstallsRedirectPolicy asserts the policy reaches
// the client the service actually fetches with. The shipped defect was not a
// wrong policy — it was no policy: ImportCoverFromURL validated the caller's
// URL and then handed it to a bare &http.Client, whose default follows ten
// redirects and permits a scheme downgrade. A test that only exercises
// coverRedirectPolicy() in isolation passes happily against that.
func TestNewEnrichmentServiceInstallsRedirectPolicy(t *testing.T) {
	svc := NewEnrichmentService(nil, newFakeProviderSettings(), &fakeBookStore{}, &fakeCoverStore{}, nil)

	client, ok := svc.http.(*http.Client)
	if !ok {
		t.Fatalf("service http doer is %T, want *http.Client", svc.http)
	}
	if client.CheckRedirect == nil {
		t.Fatal("client has no CheckRedirect — redirects follow Go's default policy")
	}

	via := []*http.Request{{URL: mustURL(t, "https://books.google.com/x.jpg")}}
	escape := &http.Request{URL: mustURL(t, "http://169.254.169.254/latest/meta-data/")}
	if err := client.CheckRedirect(escape, via); err == nil {
		t.Fatal("the installed policy permitted a redirect to a link-local address")
	}
}
