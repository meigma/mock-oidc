# Changelog

## 0.1.0 (2026-07-03)


### Features

* add authorization code flow with interactive login and id tokens (slice 2) ([#10](https://github.com/meigma/mock-oidc/issues/10)) ([19fb9b0](https://github.com/meigma/mock-oidc/commit/19fb9b0175cb88bf63ecb7c18b46a897069282cf))
* add core token pipeline with discovery, jwks, and client_credentials (slice 1) ([#9](https://github.com/meigma/mock-oidc/issues/9)) ([172b2cf](https://github.com/meigma/mock-oidc/commit/172b2cfeadcc2c3d387d599b80c40598677300d7))
* add multi-issuer support, scenarios, and the /_mock control plane (slice 5) ([#13](https://github.com/meigma/mock-oidc/issues/13)) ([bc6de51](https://github.com/meigma/mock-oidc/commit/bc6de510ad01aa276842f4090607d5debfa14418))
* add token lifecycle services with refresh, userinfo, introspection, and logout (slice 3) ([#11](https://github.com/meigma/mock-oidc/issues/11)) ([813d1ff](https://github.com/meigma/mock-oidc/commit/813d1ff48cdb2c444373feb5af7123a39e670347))
* add token-exchange, jwt-bearer, and password grants (slice 4) ([#12](https://github.com/meigma/mock-oidc/issues/12)) ([db5c92b](https://github.com/meigma/mock-oidc/commit/db5c92b1c5ebe816bbf7bfb3a75b8de4b571095a))
* establish mock-oidc walking skeleton (slice 0) ([#8](https://github.com/meigma/mock-oidc/issues/8)) ([c275a16](https://github.com/meigma/mock-oidc/commit/c275a16aa1b3dae04cde272d7b68e4e9e204a075))
* harden proxy/TLS operation and finish distribution (slice 6) ([#14](https://github.com/meigma/mock-oidc/issues/14)) ([ecaf84a](https://github.com/meigma/mock-oidc/commit/ecaf84ac43890c0c4f174b02e10458716f386d3d))


### Bug Fixes

* **httpapi:** serve index.html directly from the static handler ([#15](https://github.com/meigma/mock-oidc/issues/15)) ([aa05d23](https://github.com/meigma/mock-oidc/commit/aa05d23bd5418b661b758f136b514f1d9ccd476c))
* **oidc:** default the token subject to a per-callback UUID ([#16](https://github.com/meigma/mock-oidc/issues/16)) ([8b8d9e0](https://github.com/meigma/mock-oidc/commit/8b8d9e02f226780fed5df14315a13b1c19148e3e))

## Changelog
