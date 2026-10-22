CREATE TABLE quota_pool_config (
    id BIGINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    revision BIGINT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    interval_minutes INTEGER NOT NULL DEFAULT 5 CHECK (interval_minutes BETWEEN 5 AND 1440),
    next_run_at TIMESTAMPTZ
);
INSERT INTO quota_pool_config (id) VALUES (1);

CREATE TABLE quota_pools (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(160) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1
);
CREATE TABLE quota_pool_accounts (
    id BIGSERIAL PRIMARY KEY,
    pool_id BIGINT NOT NULL REFERENCES quota_pools(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL UNIQUE REFERENCES accounts(id)
);
CREATE TABLE quota_pool_shares (
    id BIGSERIAL PRIMARY KEY,
    pool_id BIGINT NOT NULL REFERENCES quota_pools(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    share_percent NUMERIC(6,3) NOT NULL CHECK (share_percent BETWEEN 0 AND 100),
    UNIQUE (pool_id, user_id)
);
CREATE INDEX quota_pool_shares_user_id ON quota_pool_shares(user_id);
CREATE TABLE quota_pool_managed_users (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id),
    managed BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE TABLE quota_pool_predictions (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL UNIQUE REFERENCES accounts(id),
    identity VARCHAR(64) NOT NULL,
    capacity NUMERIC(20,8) NOT NULL CHECK (capacity > 0),
    observed_at TIMESTAMPTZ NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL
);
CREATE TABLE quota_pool_sync_runs (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(160) NOT NULL UNIQUE,
    source VARCHAR(20) NOT NULL,
    status VARCHAR(32) NOT NULL,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE quota_pool_contract_revisions (
    id BIGSERIAL PRIMARY KEY,
    revision BIGINT NOT NULL UNIQUE,
    allocation JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The same stable identity fields are used by QuotaPoolAccountIdentity. Token
-- rotation preserves history when an upstream identity is available.
CREATE FUNCTION quota_pool_source_identity(c JSONB) RETURNS TEXT
LANGUAGE SQL IMMUTABLE AS $$
    SELECT CASE
        WHEN COALESCE(c->>'chatgpt_account_id','') || '|' || COALESCE(c->>'chatgpt_user_id','') || '|' ||
             COALESCE(c->>'email','') || '|' || COALESCE(c->>'organization_id','') = '|||'
        THEN COALESCE(NULLIF(c->>'refresh_token',''),c->>'access_token','')
        ELSE COALESCE(c->>'chatgpt_account_id','') || '|' || COALESCE(c->>'chatgpt_user_id','') || '|' ||
             COALESCE(c->>'email','') || '|' || COALESCE(c->>'organization_id','')
    END
$$;

CREATE FUNCTION invalidate_quota_pool_source() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.platform = 'openai' AND OLD.type = 'oauth' AND (
        NEW.platform IS DISTINCT FROM OLD.platform OR NEW.type IS DISTINCT FROM OLD.type OR
        NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id OR
        quota_pool_source_identity(NEW.credentials) IS DISTINCT FROM quota_pool_source_identity(OLD.credentials)
    ) THEN
        DELETE FROM quota_pool_predictions WHERE account_id = OLD.id;
        NEW.extra = COALESCE(NEW.extra, '{}'::jsonb) - ARRAY[
            'codex_5h_used_percent','codex_5h_reset_at','codex_5h_reset_after_seconds',
            'codex_7d_used_percent','codex_7d_reset_at','codex_7d_reset_after_seconds',
            'codex_usage_updated_at'
        ];
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER accounts_invalidate_quota_pool_source
BEFORE UPDATE OF credentials,platform,type,owner_user_id ON accounts
FOR EACH ROW EXECUTE FUNCTION invalidate_quota_pool_source();
