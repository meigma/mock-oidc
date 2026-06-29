// Package integration holds the project's integration tests: cross-package tests
// that exercise the adapters through their public APIs against real backing
// services. They are gated by the "integration" build tag and bring up real
// dependencies via testcontainers, so they require Docker and are excluded from
// the default `go test ./...`. The package contains no production code.
package integration
