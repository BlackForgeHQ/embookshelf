// reMarkable cloud driver. The tablet (Paper Pro, RM2, RM1) shares the
// same cloud protocol — pair once with an 8-character code from
// https://my.remarkable.com/device/desktop/connect, store the device
// token, and use it to mint short-lived user tokens for each upload.
//
// Endpoints:
//   - POST https://webapp-prod.cloud.remarkable.engineering/token/json/2/device/new  (pair)
//   - POST https://webapp-prod.cloud.remarkable.engineering/token/json/2/user/new    (refresh user token)
//   - POST https://internal.cloud.remarkable.com/doc/v2/files                        (upload)
//
// reMarkable controls this surface. If they change the base hosts or the
// upload envelope, operators can override endpoints via device config.

package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
)

const (
	rmAuthBaseURL   = "https://webapp-prod.cloud.remarkable.engineering"
	rmUploadBaseURL = "https://internal.cloud.remarkable.com"
	rmDeviceDesc    = "desktop-linux"
)

type RemarkableDriver struct {
	client *http.Client
}

func NewRemarkableDriver() *RemarkableDriver {
	return &RemarkableDriver{
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (d *RemarkableDriver) Kind() model.DeviceKind {
	return model.DeviceRemarkablePaperPro
}

// Pair exchanges a one-time 8-character code for a long-lived device
// token. Expected params:
//   - code  (string, required) — the code from the reMarkable "connect" page.
//   - name  (string, optional) — label to persist; defaults to "reMarkable".
func (d *RemarkableDriver) Pair(ctx context.Context, params map[string]any) (model.Device, error) {
	code, _ := params["code"].(string)
	code = strings.TrimSpace(code)
	if code == "" {
		return model.Device{}, errors.New("pairing code is required")
	}
	name, _ := params["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "reMarkable"
	}

	deviceID := newRandomID()
	reqBody, _ := json.Marshal(map[string]string{
		"code":       code,
		"deviceDesc": rmDeviceDesc,
		"deviceID":   deviceID,
	})

	url := rmAuthBaseURL + "/token/json/2/device/new"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return model.Device{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	// reMarkable rejects requests with a blank Authorization header —
	// they expect the literal "Bearer " with no token on pair.
	req.Header.Set("Authorization", "Bearer ")

	resp, err := d.client.Do(req)
	if err != nil {
		return model.Device{}, fmt.Errorf("pair reMarkable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return model.Device{}, fmt.Errorf("reMarkable pair rejected (HTTP %d): %s", resp.StatusCode, trimBody(body))
	}
	token := strings.TrimSpace(string(body))
	if token == "" {
		return model.Device{}, errors.New("reMarkable pair returned an empty token")
	}

	return model.Device{
		Kind:   model.DeviceRemarkablePaperPro,
		Name:   name,
		Secret: token,
		Config: map[string]any{
			"deviceId": deviceID,
		},
	}, nil
}

// Send uploads one book to the device's inbox using the cloud upload
// endpoint. EPUB and PDF are the supported formats.
func (d *RemarkableDriver) Send(ctx context.Context, dev model.Device, content io.Reader, meta BookMeta) error {
	userToken, err := d.refreshUserToken(ctx, dev.Secret)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read book bytes: %w", err)
	}

	mime, err := remarkableMIME(meta.Format)
	if err != nil {
		return err
	}

	title := meta.Title
	if title == "" {
		title = "Untitled"
	}
	metaJSON, _ := json.Marshal(map[string]any{
		"file_name": title,
		"parent":    "",
	})
	metaHeader := base64.StdEncoding.EncodeToString(metaJSON)

	url := rmUploadBaseURL + "/doc/v2/files"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", mime)
	req.Header.Set("rm-meta", metaHeader)
	req.Header.Set("rm-source", sanitizeFilename(title)+extensionForFormat(meta.Format))
	req.ContentLength = int64(len(body))

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload to reMarkable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reMarkable upload rejected (HTTP %d): %s", resp.StatusCode, trimBody(respBody))
	}
	return nil
}

// refreshUserToken swaps the long-lived device token for a short-lived
// user token, used on every upload. reMarkable's user tokens last a few
// hours; we mint a fresh one per push rather than caching.
func (d *RemarkableDriver) refreshUserToken(ctx context.Context, deviceToken string) (string, error) {
	url := rmAuthBaseURL + "/token/json/2/user/new"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+deviceToken)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh reMarkable user token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("reMarkable user-token rejected (HTTP %d): %s", resp.StatusCode, trimBody(body))
	}
	return strings.TrimSpace(string(body)), nil
}

func remarkableMIME(format string) (string, error) {
	switch strings.ToUpper(format) {
	case "EPUB":
		return "application/epub+zip", nil
	case "PDF":
		return "application/pdf", nil
	default:
		return "", fmt.Errorf("reMarkable accepts EPUB/PDF only — got %s", format)
	}
}

func extensionForFormat(format string) string {
	switch strings.ToUpper(format) {
	case "EPUB":
		return ".epub"
	case "PDF":
		return ".pdf"
	default:
		return ""
	}
}

// sanitizeFilename keeps only characters that are safe across the FAT32
// upload name constraints reMarkable historically enforced.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "book"
	}
	return out
}

// newRandomID is a 16-byte hex string. reMarkable's pairing call only
// requires a stable opaque identifier — not a real RFC-4122 UUID — so
// avoiding the google/uuid dependency is fine here.
func newRandomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// trimBody formats an error body for log/response display. Trims to 240
// chars and replaces newlines so the message stays on one line.
func trimBody(b []byte) string {
	s := strings.ReplaceAll(string(b), "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
