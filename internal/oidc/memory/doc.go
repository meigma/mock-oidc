// Package memory is the driven adapter that implements the core's in-memory
// stores under sync primitives. Slice 1 lands the on-demand IssuerRegistry and
// the mutable Clock; the code, refresh-token, callback-queue, and request-capture
// stores land with their ports in later slices.
package memory
