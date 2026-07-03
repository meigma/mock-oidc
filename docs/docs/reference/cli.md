---
title: CLI
description: Reference for the mock-oidc command-line interface and its subcommands.
---

# CLI

The binary is `mock-oidc`. It exposes three subcommands. When invoked with no
subcommand, the binary runs `serve`.

## Subcommands

| Subcommand | Description |
| --- | --- |
| `serve` | Runs the HTTP server. This is the default subcommand; the bare binary runs it. Listens on `:8080` by default. |
| `version` | Prints the version, commit, and build date, then exits. |
| `openapi` | Writes the OpenAPI 3.0.3 specification to standard output, or to a file when `--output`/`-o` is given, then exits. |

## `serve`

Runs the OAuth2/OIDC server and the `/_mock` control plane. Equivalent to
running the binary with no subcommand.

```bash
./bin/mock-oidc serve
```

The flags accepted by `serve` (and their `MOCK_OIDC_*` environment-variable
equivalents) are listed in [Configuration](configuration.md).

## `version`

Prints build metadata and exits. The output contains the version, the commit
the binary was built from, and the build date.

```bash
./bin/mock-oidc version
```

## `openapi`

Writes the OpenAPI 3.0.3 document describing the server's HTTP API. With no
flag the document is written to standard output.

| Flag | Alias | Description |
| --- | --- | --- |
| `--output` | `-o` | Path to write the specification to instead of standard output. |

```bash
./bin/mock-oidc openapi -o docs/docs/openapi.yaml
```

The document produced by this subcommand is the source for the
[API Reference](../api.md).
