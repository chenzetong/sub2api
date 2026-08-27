-- Force all OpenAI OAuth-like accounts to full Codex fingerprint convergence.
UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{codex_fingerprint_mode}',
    '"full"'::jsonb,
    true
)
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type IN ('oauth', 'setup-token')
  AND COALESCE(extra->>'codex_fingerprint_mode', '') <> 'full';

-- Every converged OpenAI OAuth-like account needs one stable per-account seed.
UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{codex_fingerprint_seed}',
    to_jsonb(gen_random_uuid()::text),
    true
)
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type IN ('oauth', 'setup-token')
  AND (
      extra->>'codex_fingerprint_seed' IS NULL
      OR btrim(extra->>'codex_fingerprint_seed') = ''
      OR NOT (
          extra->>'codex_fingerprint_seed' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          AND extra->>'codex_fingerprint_seed' <> '00000000-0000-0000-0000-000000000000'
      )
  );
