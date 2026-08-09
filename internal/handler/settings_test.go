// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// The settings surfaces are tested against this fake, not Postgres.
//
// Before appSettingsStore existed every one of these endpoints reached a
// *repo.AppSettingsRepo, so exercising one meant standing up a database
// and none of them was exercised at all. What went untested was not the
// storage — the rows have their own tests — but the handler-tier rules
// layered on top: the tri-state secret, which repo refusal is the
// admin's 400 and which is our 500, and what the wire shape is allowed
// to carry back.

// fakeAppSettings is a map-backed appSettingsStore: it hands back what
// it was given and records what it was told to store.
//
// It deliberately does not re-implement the rows' normalize-and-validate.
// Those rules belong to repo.Setting and are tested there; what a handler
// test needs is control over *which error comes back*, so each setter has
// an injectable failure. A fake that re-derived the validation would
// prove only that the fake agrees with itself.
type fakeAppSettings struct {
	bools           map[string]bool
	email           repo.EmailConfig
	guide           repo.ReadingGuideConfig
	audiobook       repo.AudiobookConfig
	forwardAuth     repo.ForwardAuthConfig
	converter       repo.ConverterConfig
	google          repo.OAuthPresetConfig
	github          repo.OAuthPresetConfig
	generic         repo.GenericOIDCConfig
	autoProvis      repo.OIDCAutoProvisionDetails
	setEmailErr     error
	setGuideErr     error
	setAudioErr     error
	setFwdErr       error
	setConverterErr error
	emailWrites     int
	audioWrites     int
	guideWrites     int
}

func (f *fakeAppSettings) GetBool(_ context.Context, name string) (bool, error) {
	return f.bools[name], nil
}

func (f *fakeAppSettings) SetBool(_ context.Context, name string, v bool) error {
	if f.bools == nil {
		f.bools = map[string]bool{}
	}
	f.bools[name] = v
	return nil
}

func (f *fakeAppSettings) GetEmail(context.Context) (repo.EmailConfig, error) {
	return f.email, nil
}

func (f *fakeAppSettings) SetEmail(_ context.Context, cfg repo.EmailConfig) error {
	if f.setEmailErr != nil {
		return f.setEmailErr
	}
	f.email, f.emailWrites = cfg, f.emailWrites+1
	return nil
}

func (f *fakeAppSettings) GetReadingGuide(context.Context) (repo.ReadingGuideConfig, error) {
	return f.guide, nil
}

func (f *fakeAppSettings) SetReadingGuide(_ context.Context, cfg repo.ReadingGuideConfig) error {
	if f.setGuideErr != nil {
		return f.setGuideErr
	}
	f.guide, f.guideWrites = cfg, f.guideWrites+1
	return nil
}

func (f *fakeAppSettings) GetAudiobook(context.Context) (repo.AudiobookConfig, error) {
	return f.audiobook, nil
}

func (f *fakeAppSettings) SetAudiobook(_ context.Context, cfg repo.AudiobookConfig) error {
	if f.setAudioErr != nil {
		return f.setAudioErr
	}
	f.audiobook, f.audioWrites = cfg, f.audioWrites+1
	return nil
}

func (f *fakeAppSettings) GetForwardAuth(context.Context) (repo.ForwardAuthConfig, error) {
	return f.forwardAuth, nil
}

func (f *fakeAppSettings) SetForwardAuth(_ context.Context, cfg repo.ForwardAuthConfig) error {
	if f.setFwdErr != nil {
		return f.setFwdErr
	}
	f.forwardAuth = cfg
	return nil
}

func (f *fakeAppSettings) GetOIDCAutoProvision(context.Context) (repo.OIDCAutoProvisionDetails, error) {
	return f.autoProvis, nil
}

func (f *fakeAppSettings) GetGoogle(context.Context) (repo.OAuthPresetConfig, error) {
	return f.google, nil
}

func (f *fakeAppSettings) GetGitHub(context.Context) (repo.OAuthPresetConfig, error) {
	return f.github, nil
}

func (f *fakeAppSettings) GetGenericOIDC(context.Context) (repo.GenericOIDCConfig, error) {
	return f.generic, nil
}

func (f *fakeAppSettings) GetConverter(context.Context) (repo.ConverterConfig, error) {
	return f.converter, nil
}

func (f *fakeAppSettings) SetConverter(_ context.Context, cfg repo.ConverterConfig) error {
	if f.setConverterErr != nil {
		return f.setConverterErr
	}
	f.converter = cfg
	return nil
}

// The fake has to keep satisfying the same seam production does.
var _ appSettingsStore = (*fakeAppSettings)(nil)

// settingsCtx builds a gin context for one settings request.
func settingsCtx(t *testing.T, method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	if body == "" {
		c.Request = httptest.NewRequest(method, target, nil)
		return c, rec
	}
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

// httpStatus reports the status the client would actually see.
//
// Needed because gin defers WriteHeader until the first write, so a
// handler that answers 204 with no body leaves the recorder sitting at
// its 200 default and a test reading rec.Code directly would pass
// against a handler that answered nothing at all.
func httpStatus(c *gin.Context, rec *httptest.ResponseRecorder) int {
	c.Writer.WriteHeaderNow()
	return rec.Code
}

// TestResolveSecretHasThreeArms pins the rule every settings panel's
// credential field depends on. Three lines, no dependencies, and until
// now no test — while the panels that rely on it are the ones an admin
// cannot verify by looking, because a stored secret never comes back.
//
// The clear arm is the one worth stating out loud: without it an empty
// field would always mean "keep", and a credential once stored could
// never be removed through the UI.
func TestResolveSecretHasThreeArms(t *testing.T) {
	cases := []struct {
		name     string
		incoming string
		setFlag  bool
		existing string
		want     string
	}{
		{"replace: a typed value wins", "new", true, "old", "new"},
		{"replace: a typed value wins even with the flag down", "new", false, "old", "new"},
		{"keep: blank plus set means the admin did not retype it", "", true, "old", "old"},
		{"clear: blank plus unset is an explicit removal", "", false, "old", ""},
		{"keep with nothing stored is still empty", "", true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSecret(tc.incoming, tc.setFlag, tc.existing); got != tc.want {
				t.Errorf("resolveSecret(%q, %v, %q) = %q, want %q",
					tc.incoming, tc.setFlag, tc.existing, got, tc.want)
			}
		})
	}
}

// TestNewAppSettingsStoreKeepsAMissingRepoNil guards the interface
// conversion the seam introduced: a nil *AppSettingsRepo assigned
// straight into an interface field is a non-nil interface, which would
// turn every `h.appSettings == nil` degrade into a panic.
func TestNewAppSettingsStoreKeepsAMissingRepoNil(t *testing.T) {
	if store := newAppSettingsStore(nil); store != nil {
		t.Fatal("a nil repo became a non-nil appSettingsStore; the 503 degrades would panic instead")
	}
	if store := newAppSettingsStore(&repo.AppSettingsRepo{}); store == nil {
		t.Fatal("a real repo did not survive the conversion")
	}
}

// TestSettingsDegradeWithoutAStore walks the endpoints that advertise a
// 503 when no settings repo is wired, so the guard and the interface
// conversion above stay in agreement.
func TestSettingsDegradeWithoutAStore(t *testing.T) {
	cases := []struct {
		name string
		call func(*Handler, *gin.Context)
	}{
		{"oidc get", (*Handler).SettingsOIDCGet},
		{"oidc update", (*Handler).SettingsOIDCUpdate},
		{"forward auth get", (*Handler).SettingsForwardAuthGet},
		{"forward auth update", (*Handler).SettingsForwardAuthUpdate},
		{"audiobook get", (*Handler).SettingsAudiobookGet},
		{"audiobook update", (*Handler).SettingsAudiobookUpdate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{appSettings: newAppSettingsStore(nil)}
			c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings", `{}`)
			tc.call(h, c)
			if httpStatus(c, rec) != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestSettingsMetadataRoundTripsThroughTheStore covers the smallest
// settings domain — a single boolean row — end to end over the fake.
func TestSettingsMetadataRoundTripsThroughTheStore(t *testing.T) {
	store := &fakeAppSettings{}
	h := &Handler{appSettings: store}

	c, rec := settingsCtx(t, http.MethodPut, "/api/v1/settings/metadata", `{"autoEnrich":true}`)
	h.SettingsMetadataUpdate(c)
	if httpStatus(c, rec) >= 300 {
		t.Fatalf("PUT status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !store.bools[repo.SettingMetadataAutoEnrich] {
		t.Fatalf("the METADATA_AUTO_ENRICH row was not written: %v", store.bools)
	}

	c, rec = settingsCtx(t, http.MethodGet, "/api/v1/settings/metadata", "")
	h.SettingsMetadataGet(c)
	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("GET status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"autoEnrich":true`) {
		t.Errorf("GET did not read back the write: %s", rec.Body.String())
	}
}

// errBoom is a failure that carries no settings sentinel, so a handler
// that maps it to 400 would be telling the admin to fix our bug.
var errBoom = errors.New("boom")
