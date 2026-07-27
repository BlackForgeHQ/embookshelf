// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"testing"
)

type stubProvider struct {
	name   Source
	fields []ConfigField
}

func (p stubProvider) Name() Source { return p.name }
func (p stubProvider) Search(context.Context, Query) ([]Match, error) {
	return nil, nil
}

type schemaStub struct {
	stubProvider
}

func (p schemaStub) ConfigSchema() []ConfigField { return p.fields }

// The password kind is the single declaration behind both the admin-UI
// input and the at-rest encryption slot (ADR-0010), so the walk must pick
// out exactly the password fields.
func TestSecretConfigKeysFindsOnlyPasswordFields(t *testing.T) {
	t.Parallel()

	p := schemaStub{stubProvider{name: "hardcover", fields: []ConfigField{
		{Key: "token", Kind: ConfigFieldPassword},
		{Key: "language", Kind: ConfigFieldText},
		{Key: "cookie", Kind: ConfigFieldPassword},
		{Key: "domain", Kind: ConfigFieldSelect},
	}}}

	got := SecretConfigKeys(p)
	if len(got) != 2 || got[0] != "token" || got[1] != "cookie" {
		t.Fatalf("SecretConfigKeys = %v, want [token cookie]", got)
	}
}

// A provider with no schema declares no secrets — nothing to encrypt, and
// the repo must leave its config blob alone.
func TestSecretConfigKeysWithoutSchemaIsEmpty(t *testing.T) {
	t.Parallel()

	if got := SecretConfigKeys(stubProvider{name: "open_library"}); len(got) != 0 {
		t.Fatalf("SecretConfigKeys = %v, want none", got)
	}
}

// The lookup is what the provider_settings repo uses to find its
// encryption slots without importing this package.
func TestSecretKeyLookupResolvesByID(t *testing.T) {
	t.Parallel()

	lookup := SecretKeyLookup([]Provider{
		schemaStub{stubProvider{name: "hardcover", fields: []ConfigField{
			{Key: "token", Kind: ConfigFieldPassword},
		}}},
		stubProvider{name: "open_library"},
	})

	if got := lookup("hardcover"); len(got) != 1 || got[0] != "token" {
		t.Errorf("lookup(hardcover) = %v, want [token]", got)
	}
	if got := lookup("open_library"); len(got) != 0 {
		t.Errorf("lookup(open_library) = %v, want none", got)
	}
	// An id the binary doesn't ship must not panic — the repo calls this
	// for whatever row ids the table happens to hold.
	if got := lookup("not-a-provider"); len(got) != 0 {
		t.Errorf("lookup(unknown) = %v, want none", got)
	}
}
