// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
)

// fakeOIDCRows stands in for AppSettingsRepo: it prepares rows the way
// the real one does and records what a single Apply committed, so every
// rule below is reachable with no HTTP layer and no database (#195).
type fakeOIDCRows struct {
	committed []repo.SettingRow
	writes    int
	writeErr  error
}

func (f *fakeOIDCRows) PrepareOIDCRows(sub OIDCSubmission) ([]repo.SettingRow, error) {
	rows := make([]repo.SettingRow, 0, 5)
	for _, name := range []string{"GOOGLE", "GITHUB", "GENERIC", "AUTO", "FORCE"} {
		rows = append(rows, repo.SettingRow{Name: name})
	}
	return rows, nil
}

func (f *fakeOIDCRows) SetRows(_ context.Context, rows []repo.SettingRow) error {
	f.writes++
	if f.writeErr != nil {
		return f.writeErr
	}
	f.committed = rows
	return nil
}

type fakeLockoutGuard struct {
	err     error
	checked bool
}

func (g *fakeLockoutGuard) ValidateForceOnlyTransition(_ context.Context, _ bool) error {
	g.checked = true
	return g.err
}

func oidcSubmission() OIDCSubmission {
	return OIDCSubmission{
		Google:  repo.OAuthPresetConfig{Enabled: true, ClientID: "gid", ClientSecret: "gsec"},
		GitHub:  repo.OAuthPresetConfig{Enabled: false},
		Generic: repo.GenericOIDCConfig{Enabled: false},
	}
}

func newOIDCSettings(t *testing.T) (*OIDCSettingsService, *fakeOIDCRows, *fakeLockoutGuard) {
	t.Helper()
	rows, guard := &fakeOIDCRows{}, &fakeLockoutGuard{}
	return NewOIDCSettingsService(rows, guard), rows, guard
}

// A submission is one decision: five rows, one transaction.
func TestApplyOIDCWritesEveryRowInOneGo(t *testing.T) {
	t.Parallel()

	svc, rows, _ := newOIDCSettings(t)

	if err := svc.Apply(context.Background(), oidcSubmission()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if rows.writes != 1 {
		t.Errorf("wrote %d times, want one transaction", rows.writes)
	}
	if len(rows.committed) != 5 {
		t.Errorf("committed %d rows, want all five", len(rows.committed))
	}
}

// Enabling a provider whose credentials are missing is refused, and the
// message names the provider: an admin looking at three panels needs to
// know which one they got wrong.
func TestApplyOIDCRefusesEnablingAProviderWithoutCredentials(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		sub  func(*OIDCSubmission)
		want string
	}{
		{"google without a secret", func(s *OIDCSubmission) {
			s.Google = repo.OAuthPresetConfig{Enabled: true, ClientID: "gid"}
		}, "Google"},
		{"github without a client id", func(s *OIDCSubmission) {
			s.GitHub = repo.OAuthPresetConfig{Enabled: true, ClientSecret: "sec"}
		}, "GitHub"},
		{"generic without an issuer", func(s *OIDCSubmission) {
			s.Generic = repo.GenericOIDCConfig{Enabled: true, ClientID: "cid"}
		}, "Generic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, rows, _ := newOIDCSettings(t)
			sub := oidcSubmission()
			tc.sub(&sub)

			err := svc.Apply(context.Background(), sub)

			if !errors.Is(err, ErrOIDCIncomplete) {
				t.Fatalf("err = %v, want ErrOIDCIncomplete so the handler answers 400", err)
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to name %s", err, tc.want)
			}
			if rows.writes != 0 {
				t.Error("a refused submission still wrote rows")
			}
		})
	}
}

// A provider left disabled needs no credentials at all — that is how an
// admin turns one off.
func TestApplyOIDCAcceptsADisabledProviderWithNothingFilledIn(t *testing.T) {
	t.Parallel()

	svc, rows, _ := newOIDCSettings(t)
	sub := oidcSubmission()
	sub.Google = repo.OAuthPresetConfig{Enabled: false}

	if err := svc.Apply(context.Background(), sub); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows.writes != 1 {
		t.Error("a valid submission did not write")
	}
}

// The lockout guard runs before anything lands. It used to run after
// four of the five rows had already been written, so an admin blocked
// from forcing SSO still had their provider changes applied — a
// configuration nobody asked for.
func TestApplyOIDCChecksTheLockoutGuardBeforeWriting(t *testing.T) {
	t.Parallel()

	svc, rows, guard := newOIDCSettings(t)
	guard.err = ErrOIDCForceOnlyBlocked
	sub := oidcSubmission()
	sub.ForceOnly = true

	err := svc.Apply(context.Background(), sub)

	if !errors.Is(err, ErrOIDCForceOnlyBlocked) {
		t.Fatalf("err = %v, want ErrOIDCForceOnlyBlocked", err)
	}
	if !guard.checked {
		t.Error("the lockout guard never ran")
	}
	if rows.writes != 0 {
		t.Error("rows were written despite the guard refusing — a partial configuration landed")
	}
}

// A guard failure that is not the lockout refusal is a read failure, and
// the caller has to be able to tell those apart: one is the admin's
// mistake, the other is the server's.
func TestApplyOIDCSurfacesAGuardReadFailure(t *testing.T) {
	t.Parallel()

	svc, rows, guard := newOIDCSettings(t)
	guard.err = errors.New("db unavailable")

	err := svc.Apply(context.Background(), oidcSubmission())

	if err == nil {
		t.Fatal("Apply hid a guard failure")
	}
	if errors.Is(err, ErrOIDCForceOnlyBlocked) || errors.Is(err, ErrOIDCIncomplete) {
		t.Errorf("err = %v, want it distinguishable from the admin's own mistakes", err)
	}
	if rows.writes != 0 {
		t.Error("wrote rows after the guard could not be evaluated")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 ||
		indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
