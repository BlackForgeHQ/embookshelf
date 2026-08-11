// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// settingsDomain declares one admin settings surface — the get/put pair
// over a typed app_settings row — so the HTTP pipeline those endpoints
// share is written once, in settingsGet and settingsPut, instead of once
// per panel (#249).
//
// The split of responsibilities follows the row: repo.Setting already
// owns normalize, validate, defaults and at-rest secret encryption, so a
// declaration here carries only what the handler tier adds — the wire
// shape, the keep-versus-clear secret slots, which save refusal is the
// admin's 400, and the side effect a saved row triggers (hot-reload,
// runtime publish, cache invalidation).
//
// Six surfaces are declared: email, reading guide, audiobook, forward
// auth, converter, and OIDC (whose save is the transactional
// service.OIDCSettingsService.Apply rather than a store write — the
// pipeline around it is the same). The metadata surface stays outside
// deliberately: it is a bare GetBool/SetBool, not a typed row, and has
// none of the pipeline this collapses.
type settingsDomain[Cfg, DTO any] struct {
	// name tags log lines and server errors: "email settings save".
	name string
	// ready extends the nil-store 503 with a domain's extra dependency
	// (OIDC needs the settings service). Optional.
	ready func(*Handler) bool
	// get loads the current config; put also uses it for the reload that
	// builds the response, so a PUT answers what the row now holds.
	get func(context.Context, *Handler) (Cfg, error)
	// save persists the merged config. Usually the store's setter; OIDC
	// applies through its service so five rows land in one transaction.
	save func(context.Context, *Handler, Cfg) error
	// toDTO builds the wire shape. Secrets never travel — a "set" flag
	// does. The context is there for the one surface that derives a
	// request-dependent field (OIDC's redirect URI).
	toDTO func(*Handler, *gin.Context, Cfg) DTO
	// merge lays the submission over the current config, non-secret
	// fields only — secret slots are declared below and resolved by the
	// adapter.
	merge func(dto DTO, current Cfg) Cfg
	// secrets enumerates the tri-state credential slots: what the wire
	// carried, its "set" flag, what is stored, and where the resolved
	// plaintext lands in the merged config. The adapter runs
	// resolveSecret over them — the only place it runs — so a domain
	// cannot restate the keep-versus-clear rule, only declare where it
	// applies. Optional.
	secrets func(dto *DTO, next, current *Cfg) []settingsSecret
	// badRequest says which save refusal is the admin's mistake (400,
	// message intact). Everything else is ours (500, message withheld).
	// Optional; nil means every save failure is a 500.
	badRequest func(error) bool
	// afterSave runs the domain's post-persist side effect with the
	// reloaded config. Returning false means it wrote the response
	// (the row is saved either way — these hooks report, not roll back).
	// Optional.
	afterSave func(*Handler, *gin.Context, Cfg) bool
	// noBody makes PUT answer 204 instead of the reloaded DTO.
	noBody bool
}

// settingsSecret is one declared credential slot for the adapter's
// resolveSecret loop.
type settingsSecret struct {
	incoming string  // what the wire carried
	set      bool    // the DTO's "set" flag: blank+set keeps, blank+unset clears
	stored   string  // the current row's plaintext
	slot     *string // where the resolved value lands in the merged config
}

// anySaveRefusalIsA400 is the badRequest policy of the domains whose
// store setter fails only on validation: the row's message is the
// admin's to see. It knowingly sweeps up cipher and database failures —
// the behavior these surfaces always had, kept rather than silently
// reclassified.
func anySaveRefusalIsA400(error) bool { return true }

// settingsReady centralizes the 503 degrade every surface advertises
// when the settings repo (or a domain's extra dependency) is not wired.
func settingsReady[Cfg, DTO any](c *gin.Context, h *Handler, d settingsDomain[Cfg, DTO]) bool {
	if h.appSettings == nil || (d.ready != nil && !d.ready(h)) {
		writeError(c, http.StatusServiceUnavailable, "settings repo unavailable")
		return false
	}
	return true
}

// settingsGet answers a domain's GET: load, project, 200.
func settingsGet[Cfg, DTO any](c *gin.Context, h *Handler, d settingsDomain[Cfg, DTO]) {
	if !settingsReady(c, h, d) {
		return
	}
	cfg, err := d.get(c.Request.Context(), h)
	if err != nil {
		writeServerError(c, d.name+" get", err)
		return
	}
	c.JSON(http.StatusOK, d.toDTO(h, c, cfg))
}

// settingsPut answers a domain's PUT: bind, load the current row, merge,
// resolve declared secrets, save, reload, run the side effect, respond.
func settingsPut[Cfg, DTO any](c *gin.Context, h *Handler, d settingsDomain[Cfg, DTO]) {
	if !settingsReady(c, h, d) {
		return
	}
	var body DTO
	if !bindJSON(c, &body) {
		return
	}
	ctx := c.Request.Context()

	// Load first so a secret the admin did not retype can be kept.
	// Submitting the form without retyping every credential has to be
	// safe, or admins learn to paste keys into fields they can no longer
	// read back.
	current, err := d.get(ctx, h)
	if err != nil {
		writeServerError(c, d.name+" load", err)
		return
	}
	next := d.merge(body, current)
	if d.secrets != nil {
		for _, s := range d.secrets(&body, &next, &current) {
			*s.slot = resolveSecret(s.incoming, s.set, s.stored)
		}
	}

	if err := d.save(ctx, h, next); err != nil {
		if d.badRequest != nil && d.badRequest(err) {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		writeServerError(c, d.name+" save", err)
		return
	}

	// Re-read so the response (and the side effect) sees what the row
	// made of the submission — trimmed, defaults applied — rather than
	// the submission itself.
	saved, err := d.get(ctx, h)
	if err != nil {
		writeServerError(c, d.name+" reload", err)
		return
	}
	if d.afterSave != nil && !d.afterSave(h, c, saved) {
		return
	}
	if d.noBody {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, d.toDTO(h, c, saved))
}
