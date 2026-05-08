package repo_test

import (
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
)

func TestValidateForwardAuth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     repo.ForwardAuthConfig
		wantErr error
	}{
		{
			name: "disabled with empty config is fine",
			cfg:  repo.ForwardAuthConfig{Enabled: false},
		},
		{
			name: "enabled with no CIDR refuses",
			cfg: repo.ForwardAuthConfig{
				Enabled: true,
				Headers: repo.ForwardAuthHeaders{User: "Remote-User"},
			},
			wantErr: repo.ErrForwardAuthEnabledWithoutCIDR,
		},
		{
			name: "enabled with bad CIDR refuses",
			cfg: repo.ForwardAuthConfig{
				Enabled:           true,
				TrustedProxyCIDRs: []string{"10.0.0.0"},
				Headers:           repo.ForwardAuthHeaders{User: "Remote-User"},
			},
			wantErr: repo.ErrForwardAuthInvalidCIDR,
		},
		{
			name: "enabled with valid CIDR + user header passes",
			cfg: repo.ForwardAuthConfig{
				Enabled:           true,
				TrustedProxyCIDRs: []string{"10.0.0.0/8"},
				Headers:           repo.ForwardAuthHeaders{User: "Remote-User"},
			},
		},
		{
			name: "enabled with bad user header refuses",
			cfg: repo.ForwardAuthConfig{
				Enabled:           true,
				TrustedProxyCIDRs: []string{"10.0.0.0/8"},
				Headers:           repo.ForwardAuthHeaders{User: "Remote User"},
			},
			wantErr: repo.ErrForwardAuthInvalidHeader,
		},
		{
			name: "enabled with bad logout URL refuses",
			cfg: repo.ForwardAuthConfig{
				Enabled:           true,
				TrustedProxyCIDRs: []string{"10.0.0.0/8"},
				Headers:           repo.ForwardAuthHeaders{User: "Remote-User"},
				LogoutURL:         "ftp://x",
			},
			wantErr: repo.ErrForwardAuthInvalidLogoutURL,
		},
		{
			name: "disabled with bad CIDR still rejects",
			cfg: repo.ForwardAuthConfig{
				Enabled:           false,
				TrustedProxyCIDRs: []string{"not a cidr"},
			},
			wantErr: repo.ErrForwardAuthInvalidCIDR,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := repo.ValidateForwardAuth(tc.cfg)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("want nil err, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}
