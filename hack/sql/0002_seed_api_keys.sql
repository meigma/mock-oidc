-- ############################################################################
-- # INSECURE MOCK API KEYS — LOCAL DEVELOPMENT ONLY. DO NOT USE IN PRODUCTION.
-- ############################################################################
--
-- These are plaintext placeholder credentials seeded into the EPHEMERAL Docker
-- Compose database so `docker compose up` demonstrates the authorization tier
-- end to end with zero config. They are NOT a security mechanism:
--
--   * The values are public, hard-coded, and committed to the repository.
--   * Only a SHA-256 hash is stored (the shipped store hashes keys at rest); the
--     plaintext values below are still what you send in the X-API-Key or
--     Authorization: Bearer credential. Hashing does not make a public key secret.
--   * They are data, not schema: real deployments run the goose migrations but
--     NEVER apply hack/sql/, so these mock keys can never reach a real database.
--
-- Remove this file (and replace the shipped API-key authenticator with real
-- authn) before going to production — see DELETE_ME.md. Real deployments insert
-- their own api_keys rows out of band.
--
-- The roles below must match the shipped policies: Role::"user" satisfies the
-- todo slice's coarse policy (internal/todo/authz/policy.cedar) and Role::"admin"
-- satisfies the cross-cutting admin override (internal/authz/base.cedar).
--
-- Columns are (key_hash, subject, roles text[]) per migration
-- internal/adapter/postgres/migrations/00002_create_api_keys.sql. key_hash is the
-- lowercase-hex SHA-256 of the key; encode(sha256(...::bytea), 'hex') computes the
-- same digest the Go store does, so the plaintext keys stay readable here.
INSERT INTO api_keys (key_hash, subject, roles) VALUES
  (encode(sha256('dev-user-key'::bytea),  'hex'), 'dev-user',  ARRAY['user']),
  (encode(sha256('dev-admin-key'::bytea), 'hex'), 'dev-admin', ARRAY['admin'])
ON CONFLICT (key_hash) DO NOTHING;
