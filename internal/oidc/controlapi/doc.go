// Package controlapi is the inbound test-time control plane: a small RFC 9457
// JSON API, mounted under the reserved /_mock prefix, that gives container-based
// tests the powers the upstream in-process library exposed as method calls —
// direct token mint, one-shot scenario enqueue, captured-request inspection, and
// clock control. It depends inward on internal/oidc and declares the narrow
// control-side ports it needs (ScenarioStore, RequestLog, ClockController); the
// composition root satisfies them with the SAME in-memory adapters the OIDC core
// uses, so a minted token is byte-identical to a granted one and an enqueued
// scenario alters a real /token response.
//
// It is an adapter tier peer of httpapi: it never imports httpapi (or any other
// adapter) and httpapi never imports it — the two meet only at the composition
// root. Its errors are always Huma's default application/problem+json (RFC 9457),
// never the OAuth2 error shape reserved for the protocol surface.
package controlapi
