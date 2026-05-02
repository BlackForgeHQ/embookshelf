package service

// Effects is the plan returned by DecideEffects: the set of side
// effects the edit-side write pipeline will attempt for one Write
// call. Pure data; no I/O.
//
// The matrix encoded here is ADR-0001 §3 (trigger × backend). When
// adding a new trigger or backend, update this function and the table
// test in TestDecideEffects.
type Effects struct {
	// DB indicates the canonical books-row update will fire. Always
	// true for in-scope triggers; the field is explicit so callers
	// reading the plan don't infer.
	DB bool
	// Sidecar indicates the JSON sidecar write will fire. The mode
	// (full vs. spillover) is decided post-execution from Outcome.
	Sidecar bool
	// InFile indicates the in-file embedded write will be attempted.
	// Format-specific dispatch happens at execution time; an
	// unsupported format collapses to InFileWritten=false at runtime
	// without changing the plan.
	InFile bool
}

// DecideEffects returns the side-effect plan for a Write call. The
// triggers in scope are the edit-side ones (manual_edit,
// apply_enrichment, auto_enrichment); approve and scan-reingest
// route around MetadataWriter and have their own (smaller) matrix.
//
// Degraded handles (nil, or with no Storage) collapse to DB-only:
// without storage we cannot write a sidecar or open the source for
// in-file embed. This mirrors the silent-skip behaviour the previous
// scattered nil-checks produced, but lifts it into one place.
func DecideEffects(trigger Trigger, handle *LibraryHandle) Effects {
	if trigger == TriggerAutoEnrichment {
		return Effects{DB: true}
	}
	if handle == nil || handle.Storage == nil {
		return Effects{DB: true}
	}
	e := Effects{DB: true, Sidecar: true}
	if handle.Library.BackendID == nil {
		e.InFile = true
	}
	return e
}
