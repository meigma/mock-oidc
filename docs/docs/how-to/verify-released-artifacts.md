---
title: Verify released artifacts
description: Verify the SLSA provenance and cosign signature of released mock-oidc images and binaries before use.
---

# Verify released artifacts

Every `mock-oidc` release publishes a signed multi-arch container image and
signed binaries, each carrying a GitHub-native SLSA provenance attestation.
Verify them before use to prove they were built and signed by this repository's
release pipeline — not tampered with or rebuilt by a third party.

You need the [`gh`](https://cli.github.com/) CLI (authenticated: `gh auth login`)
and [`cosign`](https://github.com/sigstore/cosign). Throughout, replace the
version and platform placeholders with the real release values:

```sh
VERSION=X.Y.Z        # e.g. 1.4.0 — the released tag, without the leading "v"
OS=<os>              # linux | darwin
ARCH=<arch>          # amd64 | arm64
```

## Verify the container image's provenance

Confirm the image manifest was built by this repository's release workflow:

```sh
gh attestation verify "oci://ghcr.io/meigma/mock-oidc:v${VERSION}" \
  --repo meigma/mock-oidc
#   => Loaded digest sha256:... for oci://ghcr.io/meigma/mock-oidc:vX.Y.Z
#   => ✓ Verification succeeded!
```

A pass proves the image manifest carries a valid SLSA provenance attestation
issued by this repository's release pipeline.

## Verify the image's cosign signature

The image is also signed with **keyless cosign**. Verify that the signer is the
release workflow's ephemeral Sigstore identity:

```sh
cosign verify "ghcr.io/meigma/mock-oidc:v${VERSION}" \
  --certificate-identity-regexp '^https://github.com/meigma/mock-oidc/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
#   => Verification for ghcr.io/meigma/mock-oidc:vX.Y.Z --
#   => The following checks were performed on each of these signatures:
#   =>   - The cosign claims were validated
#   =>   - Existence of the claims in the transparency log was verified offline
#   =>   - The code-signing certificate was verified using trusted certificate authority certificates
```

The `--certificate-identity-regexp` pins the Fulcio certificate to
`release.yml` in this repository, and `--certificate-oidc-issuer` pins it to the
GitHub Actions OIDC issuer. Both must match, so a signature minted by any other
workflow or repository is rejected.

!!! tip "Pin, don't trust, the tag"
    The image tag `vX.Y.Z` is convenient, but a tag can be re-pointed. To pin
    the exact artifact you verified, resolve and use its digest
    (`ghcr.io/meigma/mock-oidc@sha256:...`) — both commands above accept a
    `name@sha256:...` reference in place of the tag.

## Verify a downloaded binary

For a binary downloaded from the GitHub release, verify its provenance against
the dedicated attestation workflow:

```sh
gh attestation verify "./mock-oidc_${VERSION}_${OS}_${ARCH}" \
  --repo meigma/mock-oidc \
  --signer-workflow meigma/mock-oidc/.github/workflows/attest.yml
#   => Loaded digest sha256:... for file://./mock-oidc_X.Y.Z_<os>_<arch>
#   => ✓ Verification succeeded!
```

`--signer-workflow` requires the provenance to have been issued by `attest.yml`,
the reusable workflow that attests the release's binary checksums. A pass proves
the file on disk is byte-for-byte the artifact that workflow attested.

!!! note "Offline verification"
    `gh attestation verify` can run against a bundle fetched earlier. Download
    the attestation once with `gh attestation download`, then pass
    `--bundle <file>` to verify without network access on subsequent runs.

## What to do when verification fails

A non-zero exit or a message other than `Verification succeeded!` means the
artifact does not match the expected identity. Do not use it. Re-check that:

- the `VERSION`, `OS`, and `ARCH` values match a real published release;
- for the image, you are verifying the digest you actually pulled (a moved tag
  can point at a different manifest);
- your `gh` and `cosign` versions are current, so they trust the right Sigstore
  and GitHub attestation roots.

To understand how the provenance chain is built — the melange/apko image build,
the GoReleaser binaries, keyless cosign signing, and the isolated `attest.yml`
workflow — see [Architecture and distribution](../explanation/architecture-and-distribution.md).
