package storage

import "fmt"

// Resolver maps a logical context (library_id, backend_id, or both)
// to a concrete Storage instance. The scan worker, bookdrop ingest,
// hashing backfill, and sidecar reader/writer all take a Resolver
// instead of a single Storage so they can target the right backend
// for each library.
type Resolver interface {
	// Resolve returns the Storage for the given backend id. The
	// backend id is what's stored in libraries.backend_id; an empty
	// string returns the default Storage (the one that doesn't
	// belong to any specific backend, used during the transition
	// before backfill assigns backend_id to every library).
	Resolve(backendID string) (Storage, error)
}

// ResolverFunc adapts a plain function to the Resolver interface.
type ResolverFunc func(backendID string) (Storage, error)

func (f ResolverFunc) Resolve(backendID string) (Storage, error) { return f(backendID) }

// MapResolver dispatches by backend id. The empty string maps to a
// configured default (used pre-backfill).
type MapResolver struct {
	Default  Storage
	Backends map[string]Storage // keyed by storage_backends.id
}

func (r *MapResolver) Resolve(backendID string) (Storage, error) {
	if backendID == "" {
		if r.Default == nil {
			return nil, fmt.Errorf("storage: no default backend configured")
		}
		return r.Default, nil
	}
	s, ok := r.Backends[backendID]
	if !ok {
		return nil, fmt.Errorf("storage: unknown backend id %q", backendID)
	}
	return s, nil
}

// ConstantResolver returns the same Storage for every Resolve call.
// Used by the boot code when only a default backend exists.
type ConstantResolver struct{ S Storage }

func (r ConstantResolver) Resolve(_ string) (Storage, error) {
	if r.S == nil {
		return nil, fmt.Errorf("storage: no backend configured")
	}
	return r.S, nil
}
