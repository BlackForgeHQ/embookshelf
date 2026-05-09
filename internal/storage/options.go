// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

// GetOpts holds the resolved values of all GetOption values applied
// to a Get call. Exported so backend packages can inspect requested
// behavior. Field types are stable.
type GetOpts struct {
	RangeSet    bool
	RangeOffset int64
	RangeLength int64 // -1 means "until EOF"
}

// PutOpts holds the resolved values of all PutOption values applied.
type PutOpts struct {
	IfMatch        string
	IfMatchSet     bool
	IfNoneMatch    string
	IfNoneMatchSet bool
	ContentType    string
}

// DeleteOpts holds the resolved values of all DeleteOption values applied.
type DeleteOpts struct {
	VersionID string
}

// GetOption configures a Get call.
type GetOption func(*GetOpts)

// PutOption configures a Put call.
type PutOption func(*PutOpts)

// DeleteOption configures a Delete call.
type DeleteOption func(*DeleteOpts)

// WithRange limits Get to the byte range [offset, offset+length). A
// length of -1 reads from offset to EOF. Backends without CapRange
// return ErrUnsupportedOption.
func WithRange(offset, length int64) GetOption {
	return func(o *GetOpts) {
		o.RangeSet = true
		o.RangeOffset = offset
		o.RangeLength = length
	}
}

// WithIfMatch makes Put conditional on the object's current ETag.
// Returns ErrPreconditionFailed if the ETag does not match.
func WithIfMatch(etag string) PutOption {
	return func(o *PutOpts) {
		o.IfMatch = etag
		o.IfMatchSet = true
	}
}

// WithIfNoneMatch makes Put conditional on the object NOT existing
// when value is "*", or on its ETag NOT matching otherwise.
func WithIfNoneMatch(etag string) PutOption {
	return func(o *PutOpts) {
		o.IfNoneMatch = etag
		o.IfNoneMatchSet = true
	}
}

// WithContentType sets the object's Content-Type. LocalFS ignores it
// (no xattr storage); S3 persists it.
func WithContentType(ct string) PutOption {
	return func(o *PutOpts) {
		o.ContentType = ct
	}
}

// WithVersionID targets a specific historical version on Delete.
// Backends without CapVersioning return ErrUnsupportedOption.
func WithVersionID(id string) DeleteOption {
	return func(o *DeleteOpts) {
		o.VersionID = id
	}
}

// ApplyGet collects opts into a GetOpts. Backend Get implementations
// call this to read what the caller requested.
func ApplyGet(opts []GetOption) GetOpts {
	o := GetOpts{RangeLength: -1}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// ApplyPut collects opts into a PutOpts.
func ApplyPut(opts []PutOption) PutOpts {
	var o PutOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// ApplyDelete collects opts into a DeleteOpts.
func ApplyDelete(opts []DeleteOption) DeleteOpts {
	var o DeleteOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
