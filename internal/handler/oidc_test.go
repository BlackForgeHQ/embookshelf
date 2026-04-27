package handler

import (
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/service"
)

func TestOIDCErrorCodeMaps(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{service.ErrOIDCStateMismatch, "stateMismatch"},
		{service.ErrOIDCLoginNotAllowed, "userNotProvisioned"},
		{service.ErrOIDCDisabled, "disabled"},
		{service.ErrOIDCNotConfigured, "notConfigured"},
		{service.ErrOIDCUnknownProvider, "notConfigured"},
		{service.ErrOIDCPendingApproval, "pendingApproval"},
		{errors.New("anything else"), "unknown"},
	}
	for _, tc := range cases {
		if got := oidcErrorCode(tc.err); got != tc.want {
			t.Errorf("oidcErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
