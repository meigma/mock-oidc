-- Example seed data, applied by the Docker Compose `seed` step after migrations.
--
-- Drop any *.sql file under hack/sql/ to prepopulate the local database; files
-- are applied in sorted filename order, after the schema exists. This runs
-- against an ephemeral dev database, so it need not be idempotent — writing it
-- with ON CONFLICT just keeps it safe to re-run by hand.
INSERT INTO todos (id, title, status, created_at, completed_at) VALUES
  ('11111111-1111-1111-1111-111111111111', 'Read the README',         'completed', now() - interval '2 days', now() - interval '1 day'),
  ('22222222-2222-2222-2222-222222222222', 'Run docker compose up',   'open',      now() - interval '1 day',  NULL),
  ('33333333-3333-3333-3333-333333333333', 'Build something great',   'open',      now(),                     NULL)
ON CONFLICT (id) DO NOTHING;
