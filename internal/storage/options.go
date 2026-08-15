// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

// PutOpts holds the resolved values of all PutOption values applied.
// One field survives the #342 shrink: WithContentType has three real
// callers (sidecar writes, placement, delivery), while the conditional
// family, the range family and the versioned delete had none and went
// with their Capability bits.
type PutOpts struct {
	ContentType string
}

// PutOption configures a Put call.
type PutOption func(*PutOpts)

// WithContentType sets the object's Content-Type. LocalFS ignores it
// (no xattr storage); S3 persists it.
func WithContentType(ct string) PutOption {
	return func(o *PutOpts) {
		o.ContentType = ct
	}
}

// ApplyPut collects opts into a PutOpts.
func ApplyPut(opts []PutOption) PutOpts {
	var o PutOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
