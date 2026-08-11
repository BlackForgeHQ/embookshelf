// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The adapter pipeline is pinned against a toy domain rather than any of
// the six real ones, so each rule — the guards, the secret loop, the
// error split, the response modes — is provable without dragging a
// surface's own shape into the assertion. The real declarations are
// covered by the per-surface tests that already existed.

type toyConfig struct {
	Name  string
	Token string
}

type toyDTO struct {
	Name     string `json:"name"`
	Token    string `json:"token,omitempty"`
	TokenSet bool   `json:"tokenSet"`
}

// toyState is the toy domain's "store": what get hands back, what save
// receives, and the failures either is told to produce.
type toyState struct {
	stored   toyConfig
	getErr   error
	saveErr  error
	saved    []toyConfig
	reloaded bool
}

func toyDomain(st *toyState) settingsDomain[toyConfig, toyDTO] {
	return settingsDomain[toyConfig, toyDTO]{
		name: "toy settings",
		get: func(context.Context, *Handler) (toyConfig, error) {
			if st.getErr != nil {
				return toyConfig{}, st.getErr
			}
			if len(st.saved) > 0 {
				st.reloaded = true
			}
			return st.stored, nil
		},
		save: func(_ context.Context, _ *Handler, cfg toyConfig) error {
			if st.saveErr != nil {
				return st.saveErr
			}
			st.saved = append(st.saved, cfg)
			st.stored = cfg
			return nil
		},
		toDTO: func(_ *Handler, _ *gin.Context, cfg toyConfig) toyDTO {
			return toyDTO{Name: cfg.Name, TokenSet: cfg.Token != ""}
		},
		merge: func(dto toyDTO, current toyConfig) toyConfig {
			next := current
			next.Name = dto.Name
			return next
		},
		secrets: func(dto *toyDTO, next, current *toyConfig) []settingsSecret {
			return []settingsSecret{{
				incoming: dto.Token,
				set:      dto.TokenSet,
				stored:   current.Token,
				slot:     &next.Token,
			}}
		},
		badRequest: func(err error) bool { return errors.Is(err, errToyInvalid) },
	}
}

var errToyInvalid = errors.New("toy: name required")

func toyHandler() *Handler {
	return &Handler{appSettings: &fakeAppSettings{}}
}

func TestSettingsDomainGuardsANilStore(t *testing.T) {
	st := &toyState{}
	h := &Handler{appSettings: newAppSettingsStore(nil)}

	c, rec := settingsCtx(t, http.MethodGet, "/toy", "")
	settingsGet(c, h, toyDomain(st))
	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("get status = %d, want 503", rec.Code)
	}

	c, rec = settingsCtx(t, http.MethodPut, "/toy", `{}`)
	settingsPut(c, h, toyDomain(st))
	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("put status = %d, want 503", rec.Code)
	}
	if len(st.saved) != 0 {
		t.Fatal("a save happened behind the 503")
	}
}

func TestSettingsDomainReadyGuardExtendsThe503(t *testing.T) {
	st := &toyState{}
	d := toyDomain(st)
	d.ready = func(*Handler) bool { return false }
	h := toyHandler()

	c, rec := settingsCtx(t, http.MethodGet, "/toy", "")
	settingsGet(c, h, d)
	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("get status = %d, want 503", rec.Code)
	}

	c, rec = settingsCtx(t, http.MethodPut, "/toy", `{}`)
	settingsPut(c, h, d)
	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("put status = %d, want 503", rec.Code)
	}
}

func TestSettingsDomainGetHappyPath(t *testing.T) {
	st := &toyState{stored: toyConfig{Name: "kokoro", Token: "sk-1"}}
	c, rec := settingsCtx(t, http.MethodGet, "/toy", "")
	settingsGet(c, toyHandler(), toyDomain(st))
	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"kokoro"`) || !strings.Contains(body, `"tokenSet":true`) {
		t.Errorf("body = %s", body)
	}
	if strings.Contains(body, "sk-1") {
		t.Errorf("a stored secret leaked into the GET body: %s", body)
	}
}

func TestSettingsDomainGetLoadFailureIsA500(t *testing.T) {
	st := &toyState{getErr: errBoom}
	c, rec := settingsCtx(t, http.MethodGet, "/toy", "")
	settingsGet(c, toyHandler(), toyDomain(st))
	if httpStatus(c, rec) != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSettingsDomainPutRejectsBadJSON(t *testing.T) {
	st := &toyState{}
	c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":`)
	settingsPut(c, toyHandler(), toyDomain(st))
	if httpStatus(c, rec) != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(st.saved) != 0 {
		t.Fatal("a save happened despite the bind failure")
	}
}

func TestSettingsDomainPutLoadFailureIsA500(t *testing.T) {
	st := &toyState{getErr: errBoom}
	c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":"x"}`)
	settingsPut(c, toyHandler(), toyDomain(st))
	if httpStatus(c, rec) != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestSettingsDomainSecretLoop pins the one place resolveSecret now runs:
// the adapter's loop over the domain's declared slots.
func TestSettingsDomainSecretLoop(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"typed value replaces", `{"name":"n","token":"fresh","tokenSet":true}`, "fresh"},
		{"blank plus set keeps the stored secret", `{"name":"n","token":"","tokenSet":true}`, "old"},
		{"blank plus unset clears it", `{"name":"n","token":"","tokenSet":false}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &toyState{stored: toyConfig{Name: "was", Token: "old"}}
			c, rec := settingsCtx(t, http.MethodPut, "/toy", tc.body)
			settingsPut(c, toyHandler(), toyDomain(st))
			if httpStatus(c, rec) != http.StatusOK {
				t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
			}
			if len(st.saved) != 1 {
				t.Fatalf("saves = %d, want 1", len(st.saved))
			}
			if got := st.saved[0].Token; got != tc.want {
				t.Errorf("saved token = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSettingsDomainSaveErrorSplit(t *testing.T) {
	t.Run("a declared refusal is the admin's 400, message intact", func(t *testing.T) {
		st := &toyState{saveErr: errToyInvalid}
		c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":""}`)
		settingsPut(c, toyHandler(), toyDomain(st))
		if httpStatus(c, rec) != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), errToyInvalid.Error()) {
			t.Errorf("the refusal's own message was lost: %s", rec.Body.String())
		}
	})
	t.Run("anything else is our 500, message withheld", func(t *testing.T) {
		st := &toyState{saveErr: errBoom}
		c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":"x"}`)
		settingsPut(c, toyHandler(), toyDomain(st))
		if httpStatus(c, rec) != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "boom") {
			t.Errorf("an internal error leaked: %s", rec.Body.String())
		}
	})
}

// TestSettingsDomainRespondsWithTheReloadedRow proves the PUT answer is
// what the store now holds — normalized by the row — not the submission.
func TestSettingsDomainRespondsWithTheReloadedRow(t *testing.T) {
	st := &toyState{stored: toyConfig{Token: "old"}}
	c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":"next","token":"","tokenSet":true}`)
	settingsPut(c, toyHandler(), toyDomain(st))
	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if !st.reloaded {
		t.Fatal("the response was built without re-reading the row")
	}
	if body := rec.Body.String(); !strings.Contains(body, `"name":"next"`) || !strings.Contains(body, `"tokenSet":true`) {
		t.Errorf("body = %s", body)
	}
}

func TestSettingsDomainNoBodyAnswers204(t *testing.T) {
	st := &toyState{}
	d := toyDomain(st)
	d.noBody = true
	c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":"x"}`)
	settingsPut(c, toyHandler(), d)
	if httpStatus(c, rec) != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 carried a body: %s", rec.Body.String())
	}
}

func TestSettingsDomainAfterSaveHook(t *testing.T) {
	t.Run("runs with the reloaded config and can stop the response", func(t *testing.T) {
		st := &toyState{}
		d := toyDomain(st)
		var hookCfg toyConfig
		d.afterSave = func(_ *Handler, c *gin.Context, cfg toyConfig) bool {
			hookCfg = cfg
			writeErrorCode(c, http.StatusBadGateway, "TOY_RELOAD", "downstream said no")
			return false
		}
		c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":"hooked"}`)
		settingsPut(c, toyHandler(), d)
		if httpStatus(c, rec) != http.StatusBadGateway {
			t.Fatalf("status = %d, want the hook's 502", rec.Code)
		}
		if hookCfg.Name != "hooked" {
			t.Errorf("hook saw %+v, want the saved config", hookCfg)
		}
		if len(st.saved) != 1 {
			t.Fatal("the row should already be persisted when the hook fails")
		}
	})
	t.Run("a passing hook leaves the normal response", func(t *testing.T) {
		st := &toyState{}
		d := toyDomain(st)
		d.afterSave = func(*Handler, *gin.Context, toyConfig) bool { return true }
		c, rec := settingsCtx(t, http.MethodPut, "/toy", `{"name":"x"}`)
		settingsPut(c, toyHandler(), d)
		if httpStatus(c, rec) != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}
