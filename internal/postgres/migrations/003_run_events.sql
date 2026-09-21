CREATE TABLE agent_runs (
    user_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    result JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, run_id)
);

CREATE INDEX agent_runs_expires_at_idx ON agent_runs (expires_at);
