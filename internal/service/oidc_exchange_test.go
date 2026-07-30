// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// Exchange is the callback half of both auth flows the OIDC provider
// serves: it dispatches on the state's intent, runs the token exchange,
// and then either performs a Panel-driven link or hands the External
// identity to the Provisioner and mints a session.
//
// Everything here runs against fakes: the four stores are the narrow
// interfaces NewOIDCService takes, and the token exchange itself is a
// stub registered in the provider registry — the one seam that would
// otherwise need a live IdP.

const (
	testIdPSlug = "test-idp"
	testIssuer  = "https://idp.test"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeOIDCUsers is the user store: the Provisioner's view (reused from
// provisioning_test.go) plus the profile refresh the callback does.
type fakeOIDCUsers struct {
	*fakeProvUsers
	synced []syncedProfile
}

type syncedProfile struct{ userID, name, avatarURL string }

func newFakeOIDCUsers() *fakeOIDCUsers {
	return &fakeOIDCUsers{fakeProvUsers: newFakeProvUsers()}
}

func (f *fakeOIDCUsers) SyncOIDCProfile(_ context.Context, userID, name, avatarURL string) error {
	f.synced = append(f.synced, syncedProfile{userID, name, avatarURL})
	return nil
}

// fakeOIDCSessions is the session minter.
type fakeOIDCSessions struct {
	created []model.Session
	err     error
}

func (f *fakeOIDCSessions) Create(_ context.Context, userID, userAgent string, ttl time.Duration) (model.Session, error) {
	if f.err != nil {
		return model.Session{}, f.err
	}
	s := model.Session{
		ID:        "sess-1",
		UserID:    userID,
		UserAgent: userAgent,
		ExpiresAt: time.Now().Add(ttl),
	}
	f.created = append(f.created, s)
	return s, nil
}

// fakeOIDCIdentities records the full argument list of Insert, which the
// link arm's assertions need; everything else comes from the
// Provisioner's fake.
type fakeOIDCIdentities struct {
	*fakeProvIdentities
	insertedRows []insertedIdentity
}

type insertedIdentity struct{ userID, provider, issuer, subject, email string }

func newFakeOIDCIdentities() *fakeOIDCIdentities {
	return &fakeOIDCIdentities{fakeProvIdentities: newFakeProvIdentities()}
}

func (f *fakeOIDCIdentities) Insert(ctx context.Context, userID, provider, issuer, subject, email string) (model.Identity, error) {
	f.insertedRows = append(f.insertedRows, insertedIdentity{userID, provider, issuer, subject, email})
	return f.fakeProvIdentities.Insert(ctx, userID, provider, issuer, subject, email)
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type exchangeFixture struct {
	svc      *OIDCService
	settings *fakeOIDCSettings
	users    *fakeOIDCUsers
	sessions *fakeOIDCSessions
	idents   *fakeOIDCIdentities

	// claims is what the stubbed token exchange returns.
	claims      resolvedClaims
	callbackErr error
	callbacks   int
}

func newExchangeFixture(t *testing.T, provision repo.OIDCAutoProvisionDetails) *exchangeFixture {
	t.Helper()

	f := &exchangeFixture{
		settings: &fakeOIDCSettings{provision: provision},
		users:    newFakeOIDCUsers(),
		sessions: &fakeOIDCSessions{},
		idents:   newFakeOIDCIdentities(),
		claims: resolvedClaims{
			Subject: "sub-1",
			Email:   "reader@example.com",
			Name:    "Reader",
			Picture: "https://idp.test/avatar.png",
		},
	}
	f.svc = NewOIDCService(f.settings, f.users, f.sessions, f.idents, "https://books.example")

	// Stub the one operation that would otherwise talk to an IdP. The
	// registry is the service's only dispatch point, so registering a
	// slug here exercises Exchange exactly as a real provider would.
	f.svc.providers[testIdPSlug] = oidcProvider{
		callback: func(context.Context, string, stateEntry, string) (resolvedClaims, string, error) {
			f.callbacks++
			if f.callbackErr != nil {
				return resolvedClaims{}, "", f.callbackErr
			}
			return f.claims, testIssuer, nil
		},
	}
	return f
}

// seedState mints a state as an authorize redirect would have, and
// returns the opaque value the callback carries back.
func (f *exchangeFixture) seedState(t *testing.T, intent, linkUserID string) string {
	t.Helper()
	state, _, _, err := f.svc.issueStateWithIntent(testIdPSlug, "https://books.example/api/v1/auth/oidc/callback", intent, linkUserID)
	if err != nil {
		t.Fatalf("issueStateWithIntent: %v", err)
	}
	return state
}

// ---------------------------------------------------------------------------
// Login intent — the resolved arm
// ---------------------------------------------------------------------------

// The happy path: a known identity belonging to an active user gets a
// session, and the profile is refreshed from the IdP's claims.
func TestExchangeLoginResolvedMintsSession(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	state := f.seedState(t, IntentLogin, "")
	out, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if out.Intent != IntentLogin {
		t.Errorf("intent = %q, want login", out.Intent)
	}
	if out.User.ID != "u1" {
		t.Errorf("user = %q, want u1", out.User.ID)
	}
	if out.Session.ID == "" || out.Session.UserID != "u1" {
		t.Errorf("no session minted for the resolved user: %+v", out.Session)
	}
	if len(f.sessions.created) != 1 || f.sessions.created[0].UserAgent != "curl/8" {
		t.Errorf("session store calls = %+v, want one carrying the user agent", f.sessions.created)
	}
	if len(f.users.synced) != 1 || f.users.synced[0].userID != "u1" || f.users.synced[0].name != "Reader" {
		t.Errorf("profile sync = %+v, want the claims refreshed onto u1", f.users.synced)
	}
}

// ---------------------------------------------------------------------------
// Login intent — the non-resolved provisioning arms
// ---------------------------------------------------------------------------

// A pending user is one the Provisioning policy created (or found) but an
// admin has not approved. Exchange returns the user *and* an error: the
// handler needs the user to render the "awaiting approval" landing page,
// but no session may be minted. That pairing is the documented exception
// on ExchangeOutcome, and the sweep below is the other half of it.
func TestExchangeLoginPendingApprovalReturnsUserAndRefuses(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u-pending", Email: "reader@example.com", Status: model.UserStatusPending})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u-pending"}

	state := f.seedState(t, IntentLogin, "")
	out, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "")

	if !errors.Is(err, ErrOIDCPendingApproval) {
		t.Fatalf("err = %v, want ErrOIDCPendingApproval", err)
	}
	if out.User.ID != "u-pending" {
		t.Errorf("user = %q, want u-pending — the landing page has nothing to render without it", out.User.ID)
	}
	if len(f.sessions.created) != 0 {
		t.Errorf("a session was minted for an unapproved user: %+v", f.sessions.created)
	}
}

// Pending-approval is the *only* error Exchange returns alongside a
// populated outcome — ExchangeOutcome's doc says so, because a caller
// following the ordinary Go habit of discarding the value on a non-nil
// error would drop the user the approval landing page renders. An
// exception is only safe while it stays exactly one, so this sweeps
// every other refusal and pins the zero outcome.
func TestExchangeReturnsAZeroOutcomeForEveryRefusalButPendingApproval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		run  func(f *exchangeFixture) (ExchangeOutcome, error)
	}{
		{
			name: "unknown state",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				return f.svc.Exchange(context.Background(), "the-code", "never-minted", "curl/8", "")
			},
		},
		{
			name: "link callback on another session",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				return f.svc.Exchange(context.Background(), "the-code", f.seedState(t, IntentLink, "u1"), "curl/8", "u2")
			},
		},
		{
			name: "denied user",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				f.users.add(model.User{ID: "u-denied", Email: "reader@example.com", Status: model.UserStatusDenied})
				f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u-denied"}
				return f.svc.Exchange(context.Background(), "the-code", f.seedState(t, IntentLogin, ""), "curl/8", "")
			},
		},
		{
			name: "policy refusal",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				f.users.add(model.User{ID: "someone-else", Email: "other@example.com", Status: model.UserStatusActive})
				return f.svc.Exchange(context.Background(), "the-code", f.seedState(t, IntentLogin, ""), "curl/8", "")
			},
		},
		{
			name: "no email claim",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				f.claims.Email = ""
				return f.svc.Exchange(context.Background(), "the-code", f.seedState(t, IntentLogin, ""), "curl/8", "")
			},
		},
		{
			name: "token exchange failed",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				f.callbackErr = errors.New("token exchange: boom")
				return f.svc.Exchange(context.Background(), "the-code", f.seedState(t, IntentLogin, ""), "curl/8", "")
			},
		},
		{
			name: "session store failed",
			run: func(f *exchangeFixture) (ExchangeOutcome, error) {
				f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
				f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}
				f.sessions.err = errors.New("sessions: down")
				return f.svc.Exchange(context.Background(), "the-code", f.seedState(t, IntentLogin, ""), "curl/8", "")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
			out, err := tc.run(f)
			if err == nil {
				t.Fatalf("outcome %+v came back with no error — this case must refuse", out)
			}
			if out != (ExchangeOutcome{}) {
				t.Errorf("outcome = %+v, want zero: only pending-approval may carry one", out)
			}
		})
	}
}

// A denied user was explicitly refused by an admin. The refusal is
// flattened to ErrOIDCLoginNotAllowed — the login page must not tell an
// attacker whether the account exists and was denied, or never existed.
func TestExchangeLoginDeniedRefuses(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u-denied", Email: "reader@example.com", Status: model.UserStatusDenied})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u-denied"}

	state := f.seedState(t, IntentLogin, "")
	out, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "")

	if !errors.Is(err, ErrOIDCLoginNotAllowed) {
		t.Fatalf("err = %v, want ErrOIDCLoginNotAllowed", err)
	}
	if out.User.ID != "" || out.Session.ID != "" {
		t.Errorf("a denied login leaked an outcome: %+v", out)
	}
	if len(f.sessions.created) != 0 {
		t.Errorf("a session was minted for a denied user: %+v", f.sessions.created)
	}
}

// Policy refusal: no identity row, no email match, and auto-provisioning
// is off on an instance that already has users — so no account may be
// created. Same flattened error as denied.
func TestExchangeLoginNotAllowedRefuses(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails()) // EnableAutoProvisioning=false
	f.users.add(model.User{ID: "someone-else", Email: "other@example.com", Status: model.UserStatusActive})

	state := f.seedState(t, IntentLogin, "")
	_, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "")

	if !errors.Is(err, ErrOIDCLoginNotAllowed) {
		t.Fatalf("err = %v, want ErrOIDCLoginNotAllowed", err)
	}
	if len(f.users.created) != 0 || len(f.users.createdPending) != 0 {
		t.Errorf("a user was created with auto-provisioning off: %+v %+v", f.users.created, f.users.createdPending)
	}
}

// An IdP that returns no email claim cannot have a users row created for
// it. The switch has its own arm for this so the admin sees a
// configuration problem rather than a generic "not allowed" — which
// means the refusal has to be a sentinel the handler can match, not a
// string it would have to grep.
func TestExchangeLoginEmailRequiredRefuses(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.OIDCAutoProvisionDetails{EnableAutoProvisioning: true, DefaultRole: "user"})
	f.users.add(model.User{ID: "someone-else", Email: "other@example.com", Status: model.UserStatusActive})
	f.claims.Email = "" // the IdP returned no email claim

	state := f.seedState(t, IntentLogin, "")
	_, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "")

	if !errors.Is(err, ErrOIDCEmailClaimMissing) {
		t.Fatalf("err = %v, want ErrOIDCEmailClaimMissing", err)
	}
	if errors.Is(err, ErrOIDCLoginNotAllowed) {
		t.Fatalf("email_required collapsed into the not-allowed arm: %v", err)
	}
	if len(f.users.created) != 0 {
		t.Errorf("a user was created without an email: %+v", f.users.created)
	}
}

// ---------------------------------------------------------------------------
// The refusal mapping itself
// ---------------------------------------------------------------------------

// Denied and not-allowed are flattened into one refusal on purpose: the
// login page must not tell an attacker whether the account exists and was
// refused, or never existed at all. Asserted on the mapping rather than
// through Exchange because indistinguishability is the property — the two
// have to produce the same bytes, not merely both fail.
func TestRefuseLoginKeepsDeniedAndNotAllowedIndistinguishable(t *testing.T) {
	t.Parallel()

	denied, dErr := refuseLogin(ProvisionResult{Status: ProvisionDenied})
	notAllowed, nErr := refuseLogin(ProvisionResult{Status: ProvisionNotAllowed})

	if !errors.Is(dErr, ErrOIDCLoginNotAllowed) || !errors.Is(nErr, ErrOIDCLoginNotAllowed) {
		t.Fatalf("denied = %v, not-allowed = %v, want both ErrOIDCLoginNotAllowed", dErr, nErr)
	}
	if dErr.Error() != nErr.Error() {
		t.Errorf("denied says %q and not-allowed says %q — the difference tells a caller the account exists", dErr, nErr)
	}
	if denied != (ExchangeOutcome{}) || notAllowed != (ExchangeOutcome{}) {
		t.Errorf("outcomes %+v / %+v, want both zero", denied, notAllowed)
	}
}

// A provisioning status this switch has no arm for is a bug in whoever
// added it, not a login refusal. Absorbing it into the not-allowed arm
// would turn that bug into a silently rejected user; it fails loudly
// instead — loudly enough for a log, still opaque to the browser.
func TestRefuseLoginFailsLoudlyOnAnUnknownStatus(t *testing.T) {
	t.Parallel()

	out, err := refuseLogin(ProvisionResult{
		Status: ProvisionStatus("quarantined"),
		User:   model.User{ID: "u-secret", Email: "secret@example.com"},
	})

	if !errors.Is(err, ErrOIDCUnknownProvisionStatus) {
		t.Fatalf("err = %v, want ErrOIDCUnknownProvisionStatus", err)
	}
	if errors.Is(err, ErrOIDCLoginNotAllowed) {
		t.Fatal("a status nobody wrote a rule for was absorbed into the ordinary refusal")
	}
	if !strings.Contains(err.Error(), "quarantined") {
		t.Errorf("err = %v, want it to name the status nobody handled", err)
	}
	// The status is a programming fact; the user behind it is not. An
	// unknown arm must not become the one place a refusal says who it was.
	if strings.Contains(err.Error(), "u-secret") || strings.Contains(err.Error(), "secret@example.com") {
		t.Errorf("err = %v leaks the account the status belonged to", err)
	}
	if out != (ExchangeOutcome{}) {
		t.Errorf("outcome = %+v, want zero", out)
	}
}

// An empty Intent on the state is the pre-link-flow shape and must be
// read as a login, not fall through to the link arm.
func TestExchangeTreatsEmptyIntentAsLogin(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	state := f.seedState(t, "", "")
	out, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if out.Intent != IntentLogin || out.Session.ID == "" {
		t.Errorf("an intentless state did not log in: %+v", out)
	}
}

// ---------------------------------------------------------------------------
// State — the CSRF boundary in front of everything above
// ---------------------------------------------------------------------------

// A callback carrying a state nobody minted is refused before the token
// exchange runs — a forged callback must not reach the IdP or the stores.
func TestExchangeRejectsUnknownState(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())

	_, err := f.svc.Exchange(context.Background(), "the-code", "never-minted", "curl/8", "")
	if !errors.Is(err, ErrOIDCStateMismatch) {
		t.Fatalf("err = %v, want ErrOIDCStateMismatch", err)
	}
	if f.callbacks != 0 {
		t.Errorf("the token exchange ran for a forged state (%d calls)", f.callbacks)
	}
}

// A state carrying no redirect URL cannot complete a token exchange: the
// redirect_uri replayed to the token endpoint would be empty and the IdP
// answers with something opaque that names nothing an operator can fix.
// Both authorize builders already refuse with ErrOIDCNotConfigured before
// minting such a state, so today this is only reachable by minting one
// directly — as this test does. Exchange refuses with the same sentinel
// rather than trusting that every future caller of issueStateWithIntent
// remembered the guard.
func TestExchangeRefusesAStateWithNoRedirectURL(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	state, _, _, err := f.svc.issueStateWithIntent(testIdPSlug, "", IntentLogin, "")
	if err != nil {
		t.Fatalf("issueStateWithIntent: %v", err)
	}

	if _, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", ""); !errors.Is(err, ErrOIDCNotConfigured) {
		t.Fatalf("err = %v, want ErrOIDCNotConfigured — the same refusal both authorize builders give", err)
	}
	if f.callbacks != 0 {
		t.Errorf("the token exchange ran with an empty redirect_uri (%d calls)", f.callbacks)
	}
	if len(f.sessions.created) != 0 {
		t.Errorf("a session was minted off an unconfigured redirect: %+v", f.sessions.created)
	}
}

// ---------------------------------------------------------------------------
// Link intent — the Panel-driven link
// ---------------------------------------------------------------------------

// The ordinary link: a signed-in user connects a provider they have not
// used before, and the identity is attached to them. No session is
// minted — they already have one.
func TestExchangeLinkAttachesIdentityToSessionUser(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})

	state := f.seedState(t, IntentLink, "u1")
	out, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "u1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if out.Intent != IntentLink || out.UserID != "u1" || out.Provider != testIdPSlug {
		t.Errorf("outcome = %+v, want a link outcome for u1 on %s", out, testIdPSlug)
	}
	if out.Email != "reader@example.com" {
		t.Errorf("email = %q, want the claim echoed back for the panel", out.Email)
	}
	if len(f.idents.insertedRows) != 1 {
		t.Fatalf("identity inserts = %+v, want exactly one", f.idents.insertedRows)
	}
	got := f.idents.insertedRows[0]
	if got.userID != "u1" || got.issuer != testIssuer || got.subject != "sub-1" {
		t.Errorf("inserted %+v, want the IdP-attested (issuer, subject) bound to u1", got)
	}
	if len(f.sessions.created) != 0 {
		t.Errorf("the link flow minted a session: %+v", f.sessions.created)
	}
}

// Re-connecting a provider already linked to the same user is idempotent:
// no duplicate row, just a last-login touch.
func TestExchangeLinkIsIdempotentForSameUser(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	state := f.seedState(t, IntentLink, "u1")
	out, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "u1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if out.Intent != IntentLink || out.UserID != "u1" {
		t.Errorf("outcome = %+v, want an idempotent link success", out)
	}
	if len(f.idents.insertedRows) != 0 {
		t.Errorf("a duplicate identity row was inserted: %+v", f.idents.insertedRows)
	}
	if len(f.idents.touched) != 1 || f.idents.touched[0] != "i1" {
		t.Errorf("TouchLastLogin = %v, want [i1]", f.idents.touched)
	}
}

// The foreign-identity rule: an (issuer, subject) pair already claimed by
// another user may never be re-pointed by a link. Without this an admin
// could graft someone else's SSO account onto their own.
func TestExchangeLinkRefusesForeignIdentity(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	f.idents.byIssuerSubject[testIssuer+"|sub-1"] = model.Identity{ID: "i-alice", UserID: "alice"}

	state := f.seedState(t, IntentLink, "u1")
	_, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "u1")

	if !errors.Is(err, repo.ErrIdentityForeignUser) {
		t.Fatalf("err = %v, want repo.ErrIdentityForeignUser", err)
	}
	if len(f.idents.insertedRows) != 0 {
		t.Errorf("a foreign identity was re-pointed: %+v", f.idents.insertedRows)
	}
	if len(f.idents.touched) != 0 {
		t.Errorf("someone else's identity was touched: %v", f.idents.touched)
	}
}

// A link callback that comes back on a different session than the one
// that started it is refused before the token exchange — otherwise a
// re-login mid-flow would attach the identity to whoever is signed in now.
func TestExchangeLinkRefusesMismatchedSessionUser(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())

	state := f.seedState(t, IntentLink, "u1")
	_, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "u2")

	if !errors.Is(err, ErrOIDCLinkUserMismatch) {
		t.Fatalf("err = %v, want ErrOIDCLinkUserMismatch", err)
	}
	if f.callbacks != 0 {
		t.Errorf("the token exchange ran before the user check (%d calls)", f.callbacks)
	}
	if len(f.idents.insertedRows) != 0 {
		t.Errorf("an identity was attached across a session change: %+v", f.idents.insertedRows)
	}
}

// A link flow whose token exchange fails must surface the failure, not
// half-link anything.
func TestExchangeSurfacesCallbackFailure(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t, repo.DefaultOIDCAutoProvisionDetails())
	f.callbackErr = errors.New("token exchange: boom")

	state := f.seedState(t, IntentLink, "u1")
	_, err := f.svc.Exchange(context.Background(), "the-code", state, "curl/8", "u1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the provider's failure surfaced", err)
	}
	if len(f.idents.insertedRows) != 0 {
		t.Errorf("an identity was inserted despite a failed exchange: %+v", f.idents.insertedRows)
	}
}
