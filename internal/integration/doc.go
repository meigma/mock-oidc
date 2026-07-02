// Package integration holds the container-backed integration suite, gated by the
// `integration` build tag so the default `go test ./...` stays hermetic. It boots
// the shipped mock-oidc:dev image with testcontainers-go and drives it as a black
// box.
//
// In the skeleton slice (Slice 0) the suite asserts only that the image boots and
// serves the infrastructure routes (R3). Later slices replace and extend it with
// the real OIDC-image parity assertions.
package integration
