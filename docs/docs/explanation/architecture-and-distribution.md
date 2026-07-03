---
title: Architecture and distribution
description: Why mock-oidc is built as a pure hexagonal core with enforced boundaries, and why it ships as a Dockerfile-free, signed, provenance-carrying image.
---

# Architecture and distribution

This page is for evaluators sizing up whether mock-oidc is trustworthy enough to
sit in their test harness, and for contributors who want to understand the shape
of the code before changing it. It is not a runbook. It explains two decisions
that define the project: how the code is *organised*, and how the binary and
image are *built and shipped*. Both decisions are driven by the same underlying
goal — a tool that mints real cryptographic tokens should be legible, and its
key-holding and signing paths should be small enough to audit at a glance.

## A pragmatic hexagonal core

mock-oidc follows ports-and-adapters (hexagonal) architecture, but pragmatically
rather than dogmatically. The point of the pattern here is not layering for its
own sake; it is to keep the part of the system that understands OAuth2 and OIDC
— what a token means, how a grant resolves, what belongs in a discovery document
— completely free of any knowledge about HTTP, chi, Huma, JSON wire formats, or
where signing keys physically live.

That core is `internal/oidc`. It is pure domain logic and depends on nothing in
the adapters. Dependencies point *inward*: the outer rings know about the core,
the core knows nothing about the outer rings. It talks to the outside world only
through interfaces (ports) that it defines and the adapters implement.

Around the core sit two kinds of adapters:

- **Driven adapters** — things the core *calls*. `internal/oidc/signing` is the
  real, key-bearing implementation that produces and verifies signatures.
  `internal/oidc/memory` provides the in-memory stores for issuers, codes, and
  refresh tokens. Because these are DB-less and in-memory, a fresh process boots
  with zero configuration and no external dependencies — which is exactly what a
  test fixture wants.
- **Driving adapters** — things that *call* the core. `internal/oidc/httpapi`
  exposes the OAuth2/OIDC HTTP surface (authorize, token, userinfo, introspect,
  and the rest). `internal/oidc/controlapi` exposes the `/_mock` control plane.
  Both translate an inbound request into a core operation and the core's answer
  back into a response; neither leaks its transport concerns inward.

Underneath them is a deliberately generic transport layer, `internal/adapter/http`:
the chi router, the middleware stack, the RFC 9457 problem+json error rendering,
the infrastructure routes, the OpenAPI export, and a `Registrar` seam that lets
each driving adapter mount its routes without the transport layer knowing what
those routes *are*. Finally `internal/app` is the composition root — the single
place where concrete adapters are constructed and wired into the core. Nothing
else does wiring, so there is exactly one file to read to understand how the
whole object graph is assembled.

### Why the boundary is enforced, not just documented

Architectural intentions rot. A boundary that exists only in a diagram gets
crossed the first time someone reaches for a convenient import. So the core's
isolation here is *enforced by the build*, in two independent ways:

- An **`oidc-core` depguard rule** in `.golangci.yml` fails linting if anything
  under the core imports transport, framework, or key-bearing packages.
- A **`TestCoreImportsAreClean` architecture test** asserts the same invariant
  from inside the test suite, so it fails even a `go test` run that skips the
  linter.

Having both a linter rule and a test is intentional redundancy: they run in
different tools, at different times, and neither alone can be silently disabled
without someone noticing. The payoff is that a reviewer never has to manually
police "is the core still pure?" — a violating pull request goes red on its own.

### JOSE is stdlib-only, on purpose

The most consequential dependency decision is one the project *declined* to make:
signing and verification (the JOSE path) use only the Go standard library, with
no third-party JOSE package. This keeps the amount of external code in the
key-holding path at zero.

There is a real trade-off here. A mature JOSE library is convenient and battle-
tested, and reimplementing signing means owning that code. The project accepts
that cost because the alternative is worse for its specific purpose: an
evaluator who wants to know "what code touches my signing keys?" gets a small,
self-contained answer instead of a transitive dependency tree. For a tool whose
whole job is to hold keys and mint tokens, the surface you can audit matters more
than the convenience you give up. The security reasoning behind the key-holding
path is developed further in the [security model](security-model.md).

## Built without a Dockerfile

The distribution story rhymes with the architecture story: prefer a small,
inspectable, provenance-carrying artifact over a convenient opaque one.

The container image is built **without a Dockerfile**. Instead of `FROM` a base
image and running shell commands as root at build time, the pipeline uses two
declarative tools:

- **melange** compiles the Go binary into a signed Wolfi **apk** package
  (described by `melange.yaml`). Version, commit, and build date are stamped into
  the binary here via a vars file.
- **apko** assembles that apk plus a minimal Wolfi base into an OCI image
  (`apko.yaml`). The result is multi-arch, runs as a **non-root** user
  (uid 65532), carries `ca-certificates` and `tzdata`, and has **no shell** at
  all.

Several properties fall out of this that a Dockerfile makes hard. There is no
build-time shell and no root layer, so there is no accreted state to reason
about — the image contents are exactly the declared package set and nothing
else. "No shell" is not an inconvenience to work around; it is a deliberate
reduction of what an attacker (or a confused test) could do inside a running
container. Each architecture is built **natively** rather than emulated under
QEMU, which keeps builds fast and avoids the subtle correctness questions that
cross-emulation can introduce.

The Wolfi base intentionally **floats to latest** packages rather than pinning
every version by hand. Pinning is the usual instinct for reproducibility, but it
trades security for a false sense of it — pinned bases quietly rot and ship known
vulnerabilities. The project takes the opposite bet: track upstream, and make the
build *self-documenting* instead. Every build records the exact resolved versions
in a per-build **SBOM** and provenance attestation, so "what was actually in this
image?" is always answerable after the fact even though the input floated.

## Provenance and the SLSA Build L3 isolation boundary

A signed, minimal image is only half the trust story; the other half is being
able to prove *where it came from*. Releases carry SLSA provenance, and the
release machinery is arranged specifically so that provenance can reach **Build
Level 3**.

The moving parts:

- **Release Please** maintains the release pull request and, on merge, cuts a
  **draft** GitHub release and tag.
- **GoReleaser** builds the binaries, checksums, and SBOMs.
- The release workflow publishes `ghcr.io/meigma/mock-oidc:vX.Y.Z` as a
  multi-arch manifest via apko, **keyless-cosign-signs** the image, and attaches
  a syft SBOM attestation.
- A **separate, isolated reusable workflow** (`attest.yml`) generates the
  GitHub-native SLSA provenance attestations for *both* the binary checksums and
  the image manifest digest.

The reason attestation lives in its own workflow instead of alongside the build
is the crux of SLSA Build L3. L3 requires that the signing identity used to
produce provenance be **unreachable by the build steps** — otherwise a
compromised build could forge its own provenance and the attestation would prove
nothing. By keeping attestation in an isolated workflow with its own privileges,
the build job never holds the credential that signs the provenance, so it cannot
fabricate a claim about itself. This isolation is the whole point; it is why the
work is split across two workflows that could, superficially, have been one.

Finally, the release is cut as a **draft** so a human inspects it before it goes
public. Automation gets the artifacts, the signatures, and the attestations
right; a person still makes the decision to publish. That human gate is a
deliberate seam, not a missing feature.

!!! note "Verifying, not just trusting"
    The value of signatures and attestations is that you don't have to take any
    of this on faith. The exact `cosign` and `gh attestation verify` commands for
    checking a released image and binary live in
    [Verify released artifacts](../how-to/verify-released-artifacts.md).

## What is deliberately not done

A few omissions are choices worth naming, because their absence is sometimes
mistaken for an oversight:

- **No embedded library API.** Upstream mock-oauth2-server can be driven
  in-process as a JVM library. mock-oidc is container-first instead, and the
  `/_mock` control plane is the equivalent driving surface. This is discussed as
  a parity decision in [Parity with mock-oauth2-server](parity.md).
- **No hand-pinned base image.** As above, floating-plus-SBOM is preferred over
  pinning-and-drifting.
- **No third-party JOSE dependency**, even though one would be less code to
  maintain — the audit surface is worth more than the convenience.

Taken together, the architecture and the distribution pipeline express one
consistent preference: keep the trusted parts small and their boundaries
machine-enforced, and make everything about a build answerable after the fact
rather than asking anyone to trust it blindly.
