// Package memory is the driven adapter that implements the core's in-memory
// stores under sync primitives. Slice 1 lands the on-demand IssuerRegistry and
// the mutable Clock; Slice 2 adds the single-use CodeStore; Slice 3 the
// RefreshTokenStore; Slice 5 the one-shot CallbackQueue (scenario queue) and the
// bounded per-issuer RequestRecorder (capture log), and extends the Clock with
// the control-plane Freeze/Advance/State facet. Several types satisfy both a
// narrow domain read port and a narrow control-plane write facet, so two
// non-cooperating adapters share one backing store without importing each other.
package memory
