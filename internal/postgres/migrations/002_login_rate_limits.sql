CREATE TABLE agent_login_rate_limits (
    limit_key TEXT PRIMARY KEY,
    failure_count INTEGER NOT NULL,
    window_started_at TIMESTAMPTZ NOT NULL,
    locked_until TIMESTAMPTZ
);
