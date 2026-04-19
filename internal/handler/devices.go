package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// deviceDTO is the wire shape for a registered device. Secret is
// intentionally absent — it never leaves the server.
type deviceDTO struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	LastSentAt string `json:"lastSentAt,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

func toDeviceDTO(d model.Device) deviceDTO {
	out := deviceDTO{
		ID:        d.ID,
		Kind:      string(d.Kind),
		Name:      d.Name,
		LastError: d.LastError,
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
	}
	if d.LastSentAt != nil {
		out.LastSentAt = d.LastSentAt.UTC().Format(time.RFC3339)
	}
	return out
}

// Devices lists the signed-in user's registered devices.
func (h *Handler) Devices(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	devices, err := h.devices.ListForUser(c.Request.Context(), userID)
	if err != nil {
		writeServerError(c, "devices list", err)
		return
	}
	out := make([]deviceDTO, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDeviceDTO(d))
	}
	c.JSON(http.StatusOK, gin.H{"devices": out})
}

type pairDeviceReq struct {
	Kind   string         `json:"kind"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

// DevicePair runs the driver-specific pairing flow and stores the result.
// For reMarkable Paper Pro the request looks like:
//
//	{ "kind": "remarkable-paper-pro",
//	  "name": "My Paper Pro",
//	  "params": { "code": "abcd1234" } }
func (h *Handler) DevicePair(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	var body pairDeviceReq
	if !bindJSON(c, &body) {
		return
	}
	if body.Params == nil {
		body.Params = map[string]any{}
	}
	if body.Name != "" {
		body.Params["name"] = body.Name
	}

	d, err := h.devices.Pair(c.Request.Context(), userID, model.DeviceKind(body.Kind), body.Params)
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, gin.H{"device": toDeviceDTO(d)})
	case errors.Is(err, service.ErrUnsupportedKind):
		writeError(c, http.StatusBadRequest, "unsupported device kind")
	case errors.Is(err, repo.ErrDeviceNameTaken):
		writeError(c, http.StatusConflict, repo.ErrDeviceNameTaken.Error())
	default:
		// Pairing failures (bad code, rM cloud unreachable, weak params)
		// are user-fixable; surface the driver's message directly.
		writeError(c, http.StatusBadRequest, err.Error())
	}
}

// DeviceDelete removes a device from the user's list.
func (h *Handler) DeviceDelete(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	err := h.devices.Delete(c.Request.Context(), userID, c.Param("id"))
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "device not found")
	default:
		writeServerError(c, "device delete", err)
	}
}

// BookSendToDevice pushes one book to one of the user's devices. The
// driver handles the wire protocol; the service records the outcome on
// the device row so the UI can show per-device last-sent state.
func (h *Handler) BookSendToDevice(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	bookID := c.Param("id")
	deviceID := c.Param("deviceId")

	err := h.devices.Send(c.Request.Context(), userID, deviceID, bookID)
	switch {
	case err == nil:
		c.Status(http.StatusAccepted)
	case errors.Is(err, repo.ErrNotFound):
		writeError(c, http.StatusNotFound, "device or book not found")
	case errors.Is(err, service.ErrUnsupportedKind):
		writeError(c, http.StatusBadRequest, "device driver unavailable")
	default:
		// Upload errors can be surprising (reMarkable 5xx, network
		// timeouts, unsupported format). Pass the message through so
		// the user can see what happened.
		writeError(c, http.StatusBadGateway, err.Error())
	}
}
